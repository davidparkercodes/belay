package store

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

// RetentionTier defines a time-based data retention tier with an associated compaction strategy.
type RetentionTier struct {
	Name     string
	MaxAge   time.Duration
	Strategy CompactionStrategy
}

// CompactionStrategy determines how events are compacted within a retention tier.
type CompactionStrategy int

const (
	// StrategyFull retains every event with no compaction.
	StrategyFull CompactionStrategy = iota
	// StrategyHourly collapses modify events to one per file+session per hour.
	StrategyHourly
	// StrategySessionBoundary keeps only session boundary events.
	StrategySessionBoundary
	// StrategyDaily keeps only daily snapshots.
	StrategyDaily
)

// RetentionPolicy defines the ordered list of retention tiers for event lifecycle management.
type RetentionPolicy struct {
	Tiers []RetentionTier
}

// DefaultRetentionPolicy creates a four-tier retention policy (hot, warm, cold, archive).
func DefaultRetentionPolicy(hotHours, warmDays, coldDays, archiveDays int) *RetentionPolicy {
	return &RetentionPolicy{
		Tiers: []RetentionTier{
			{Name: "hot", MaxAge: time.Duration(hotHours) * time.Hour, Strategy: StrategyFull},
			{Name: "warm", MaxAge: time.Duration(warmDays) * 24 * time.Hour, Strategy: StrategyHourly},
			{Name: "cold", MaxAge: time.Duration(coldDays) * 24 * time.Hour, Strategy: StrategySessionBoundary},
			{Name: "archive", MaxAge: time.Duration(archiveDays) * 24 * time.Hour, Strategy: StrategyDaily},
		},
	}
}

// TierForAge returns the retention tier applicable for the given event age, or nil if expired.
func (p *RetentionPolicy) TierForAge(age time.Duration) *RetentionTier {
	for i := range p.Tiers {
		if age <= p.Tiers[i].MaxAge {
			return &p.Tiers[i]
		}
	}
	return nil
}

// CompactionResult summarizes the outcome of a compaction pass.
type CompactionResult struct {
	EventsReviewed   int                            `json:"events_reviewed"`
	EventsKept       int                            `json:"events_kept"`
	EventsRemoved    int                            `json:"events_removed"`
	BytesFreed       int64                          `json:"bytes_freed"`
	ObjectsFreed     int                            `json:"objects_freed"`
	ObjectBytesFreed int64                          `json:"object_bytes_freed"`
	TierBreakdown    map[string]int                 `json:"tier_breakdown"`
	Segments         *index.SegmentCompactionResult `json:"segments,omitempty"`
	IndexVacuumed    bool                           `json:"index_vacuumed"`
	DurationMs       int64                          `json:"duration_ms"`
}

// GCResult summarizes the outcome of a garbage collection pass.
type GCResult struct {
	OrphanedObjects int   `json:"orphaned_objects"`
	BytesFreed      int64 `json:"bytes_freed"`
	ObjectsScanned  int   `json:"objects_scanned"`
}

// GarbageCollect removes orphaned objects not referenced by any event in the index.
func GarbageCollect(idx *index.Index, objStore *Store, dryRun bool) (*GCResult, error) {
	result := &GCResult{}

	referenced, err := idx.ActiveContentHashes()
	if err != nil {
		return nil, fmt.Errorf("query active hashes: %w", err)
	}

	hashes, err := objStore.ListHashes()
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	result.ObjectsScanned = len(hashes)

	for _, hash := range hashes {
		if !referenced[hash] {
			result.OrphanedObjects++
			size, sizeErr := objStore.ObjectSize(hash)
			if sizeErr == nil {
				result.BytesFreed += size
			}
			if !dryRun {
				if err := objStore.Delete(hash); err != nil {
					return nil, fmt.Errorf("delete orphan %s: %w", hash[:8], err)
				}
			}
		}
	}

	return result, nil
}

// Compactor applies tiered retention compaction to the Belay event store.
type Compactor struct {
	idx       *index.Index
	objStore  *Store
	retention *config.RetentionConfig
	dryRun    bool
	now       time.Time
	eventsDir string
	logf      func(format string, args ...interface{})
}

// NewCompactor creates a Compactor with the given dependencies.
func NewCompactor(idx *index.Index, objStore *Store, retention *config.RetentionConfig, dryRun bool) *Compactor {
	return &Compactor{
		idx:       idx,
		objStore:  objStore,
		retention: retention,
		dryRun:    dryRun,
		now:       time.Now(),
		logf:      log.Printf,
	}
}

// SetEventsDir enables the segment-reclamation phase (subject to retention.compact_segments).
func (c *Compactor) SetEventsDir(dir string) {
	c.eventsDir = dir
}

// SetLogger routes compaction log lines to the given printf-style function.
func (c *Compactor) SetLogger(logf func(format string, args ...interface{})) {
	if logf != nil {
		c.logf = logf
	}
}

// RunCompaction applies all compaction tiers in order: purge, archive, cold, warm,
// per-file version cap, garbage collection, storage limit, then (optionally) sealed-segment
// reclamation and an index vacuum. Returns a summary of all changes.
func (c *Compactor) RunCompaction() (*CompactionResult, error) {
	start := time.Now()
	result := &CompactionResult{
		TierBreakdown: make(map[string]int),
	}

	purged, err := c.purge()
	if err != nil {
		return nil, fmt.Errorf("purge: %w", err)
	}
	result.EventsRemoved += purged
	result.TierBreakdown["purged"] = purged

	archiveRemoved, err := c.compactArchive()
	if err != nil {
		return nil, fmt.Errorf("archive compaction: %w", err)
	}
	result.EventsRemoved += archiveRemoved
	result.TierBreakdown["archive_compacted"] = archiveRemoved

	coldRemoved, err := c.compactCold()
	if err != nil {
		return nil, fmt.Errorf("cold compaction: %w", err)
	}
	result.EventsRemoved += coldRemoved
	result.TierBreakdown["cold_compacted"] = coldRemoved

	warmRemoved, err := c.compactWarm()
	if err != nil {
		return nil, fmt.Errorf("warm compaction: %w", err)
	}
	result.EventsRemoved += warmRemoved
	result.TierBreakdown["warm_compacted"] = warmRemoved

	versionsRemoved, err := c.enforceMaxVersions()
	if err != nil {
		return nil, fmt.Errorf("version cap: %w", err)
	}
	result.EventsRemoved += versionsRemoved
	result.TierBreakdown["version_capped"] = versionsRemoved

	gcResult, err := GarbageCollect(c.idx, c.objStore, c.dryRun)
	if err != nil {
		return nil, fmt.Errorf("garbage collect: %w", err)
	}
	result.ObjectsFreed += gcResult.OrphanedObjects
	result.ObjectBytesFreed += gcResult.BytesFreed
	result.BytesFreed += gcResult.BytesFreed

	storageRemoved, storageObjects, storageFreed, err := c.enforceStorageLimit()
	if err != nil {
		return nil, fmt.Errorf("storage limit: %w", err)
	}
	result.EventsRemoved += storageRemoved
	result.ObjectsFreed += storageObjects
	result.ObjectBytesFreed += storageFreed
	result.BytesFreed += storageFreed
	if storageRemoved > 0 {
		result.TierBreakdown["storage_limit"] = storageRemoved
	}

	if c.eventsDir != "" && c.retention.CompactSegments {
		segResult, err := index.CompactSegments(c.idx, c.eventsDir, c.dryRun, c.logf)
		if err != nil {
			return nil, fmt.Errorf("segment compaction: %w", err)
		}
		result.Segments = segResult
		result.BytesFreed += segResult.BytesFreed
	}

	if !c.dryRun && result.EventsRemoved > 0 {
		vacuumed, err := c.idx.MaybeVacuum()
		if err != nil {
			c.logf("belay: index vacuum skipped: %v", err)
		}
		result.IndexVacuumed = vacuumed
	}

	totalEvents, err := c.idx.CountEvents()
	if err != nil {
		return nil, fmt.Errorf("count events: %w", err)
	}
	result.EventsKept = int(totalEvents)
	result.EventsReviewed = result.EventsKept + result.EventsRemoved
	result.DurationMs = time.Since(start).Milliseconds()

	return result, nil
}

// compactable reports whether an event may be removed by a tier compaction pass.
// Checkpoints and session meta-events are never collapsed; only purge removes them.
func compactable(e *schema.Event) bool {
	return e.Op != schema.OpCheckpoint && e.FilePath != "" && e.FilePath != ".belay/sessions"
}

func (c *Compactor) deleteBatch(toDelete []string, tier, verb string) (int, error) {
	if len(toDelete) == 0 {
		return 0, nil
	}
	if c.dryRun {
		c.logf("belay: [dry-run] would compact %d %s-tier events (%s)", len(toDelete), tier, verb)
		return len(toDelete), nil
	}
	deleted, err := c.idx.DeleteEventsBatch(toDelete)
	if err != nil {
		return 0, err
	}
	c.logf("belay: compacted %d %s-tier events (%s)", deleted, tier, verb)
	return int(deleted), nil
}

// purge deletes all events older than the archive tier (if archive_days > 0).
func (c *Compactor) purge() (int, error) {
	if c.retention.ArchiveDays <= 0 {
		return 0, nil
	}

	cutoff := c.now.Add(-time.Duration(c.retention.ArchiveDays) * 24 * time.Hour)
	cutoffNano := cutoff.UnixNano()

	if c.dryRun {
		events, err := c.idx.QueryEvents(&index.Query{
			Until:     cutoffNano,
			OrderDesc: false,
		})
		if err != nil {
			return 0, err
		}
		count := len(events)
		if count > 0 {
			c.logf("belay: [dry-run] would purge %d events older than %d days", count, c.retention.ArchiveDays)
		}
		return count, nil
	}

	deleted, err := c.idx.DeleteEventsBefore(cutoffNano)
	if err != nil {
		return 0, err
	}

	if deleted > 0 {
		c.logf("belay: purged %d events older than %d days", deleted, c.retention.ArchiveDays)
	}
	return int(deleted), nil
}

// compactArchive keeps only one snapshot per file per day for events in the archive tier
// (older than cold_days but within archive_days).
func (c *Compactor) compactArchive() (int, error) {
	archiveCutoff := c.now.Add(-time.Duration(c.retention.ArchiveDays) * 24 * time.Hour)
	coldCutoff := c.now.Add(-time.Duration(c.retention.ColdDays) * 24 * time.Hour)

	events, err := c.idx.QueryEvents(&index.Query{
		Since:     archiveCutoff.UnixNano(),
		Until:     coldCutoff.UnixNano(),
		OrderDesc: false,
	})
	if err != nil {
		return 0, fmt.Errorf("query archive events: %w", err)
	}

	var toDelete []string
	for _, fileEvents := range groupByFile(events) {
		sort.Slice(fileEvents, func(i, j int) bool {
			return fileEvents[i].TimestampNano < fileEvents[j].TimestampNano
		})

		byDay := make(map[string][]*schema.Event)
		for _, e := range fileEvents {
			key := e.Timestamp().Format("2006-01-02")
			byDay[key] = append(byDay[key], e)
		}

		for _, dayEvents := range byDay {
			for _, e := range dayEvents[:len(dayEvents)-1] {
				toDelete = append(toDelete, e.EventID)
			}
		}
	}

	return c.deleteBatch(toDelete, "archive", "daily snapshots")
}

// compactCold keeps only the first and last event per file per session for events
// in the cold tier (older than warm_days but within cold_days).
func (c *Compactor) compactCold() (int, error) {
	coldCutoff := c.now.Add(-time.Duration(c.retention.ColdDays) * 24 * time.Hour)
	warmCutoff := c.now.Add(-time.Duration(c.retention.WarmDays) * 24 * time.Hour)

	events, err := c.idx.QueryEvents(&index.Query{
		Since:     coldCutoff.UnixNano(),
		Until:     warmCutoff.UnixNano(),
		OrderDesc: false,
	})
	if err != nil {
		return 0, fmt.Errorf("query cold events: %w", err)
	}

	var toDelete []string
	for _, fsEvents := range groupByFileSession(events) {
		if len(fsEvents) <= 2 {
			continue
		}
		for _, e := range fsEvents[1 : len(fsEvents)-1] {
			toDelete = append(toDelete, e.EventID)
		}
	}

	return c.deleteBatch(toDelete, "cold", "session boundaries")
}

// compactWarm collapses modify events to hourly granularity per file+session: within each
// clock hour only the last modify survives. Creates, deletes and renames are never removed.
// Events in the warm tier: older than hot_hours but within warm_days.
func (c *Compactor) compactWarm() (int, error) {
	warmCutoff := c.now.Add(-time.Duration(c.retention.WarmDays) * 24 * time.Hour)
	hotCutoff := c.now.Add(-time.Duration(c.retention.HotHours) * time.Hour)

	events, err := c.idx.QueryEvents(&index.Query{
		Since:     warmCutoff.UnixNano(),
		Until:     hotCutoff.UnixNano(),
		OrderDesc: false,
	})
	if err != nil {
		return 0, fmt.Errorf("query warm events: %w", err)
	}

	var toDelete []string
	for _, fsEvents := range groupByFileSession(events) {
		toDelete = append(toDelete, hourlyCollapse(fsEvents)...)
	}

	return c.deleteBatch(toDelete, "warm", "hourly granularity")
}

// hourlyCollapse returns the IDs of modify events that are not the last modify within their
// clock hour. Input must be sorted ascending by timestamp.
func hourlyCollapse(events []*schema.Event) []string {
	if len(events) < 2 {
		return nil
	}

	var toDelete []string
	lastModifyInHour := make(map[int64]*schema.Event)
	for _, e := range events {
		if e.Op != schema.OpModify {
			continue
		}
		hour := e.TimestampNano / int64(time.Hour)
		if prev, ok := lastModifyInHour[hour]; ok {
			toDelete = append(toDelete, prev.EventID)
		}
		lastModifyInHour[hour] = e
	}
	return toDelete
}

// enforceMaxVersions keeps only the newest max_versions_per_file modify events per file
// beyond the hot tier. Everything inside the hot window and every non-modify event is kept.
func (c *Compactor) enforceMaxVersions() (int, error) {
	limit := c.retention.MaxVersionsPerFile
	if limit <= 0 {
		return 0, nil
	}

	hotCutoff := c.now.Add(-time.Duration(c.retention.HotHours) * time.Hour)
	events, err := c.idx.QueryEvents(&index.Query{
		Until:      hotCutoff.UnixNano(),
		Operations: []string{schema.OpModify.String()},
		OrderDesc:  true,
	})
	if err != nil {
		return 0, fmt.Errorf("query versions: %w", err)
	}

	var toDelete []string
	for _, fileEvents := range groupByFile(events) {
		if len(fileEvents) <= limit {
			continue
		}
		sort.Slice(fileEvents, func(i, j int) bool {
			return fileEvents[i].TimestampNano > fileEvents[j].TimestampNano
		})
		for _, e := range fileEvents[limit:] {
			toDelete = append(toDelete, e.EventID)
		}
	}

	return c.deleteBatch(toDelete, "version-cap", fmt.Sprintf("max %d versions per file", limit))
}

// enforceStorageLimit checks total storage usage and applies increasingly aggressive
// compaction if the max_storage_gb limit is exceeded.
func (c *Compactor) enforceStorageLimit() (int, int, int64, error) {
	if c.retention.MaxStorageGB <= 0 {
		return 0, 0, 0, nil
	}

	maxBytes := int64(c.retention.MaxStorageGB) * 1024 * 1024 * 1024

	totalBytes, _, err := c.objStore.Size()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("check storage size: %w", err)
	}

	if totalBytes <= maxBytes {
		return 0, 0, 0, nil
	}

	overageBytes := totalBytes - maxBytes
	c.logf("belay: storage %.2f GB exceeds limit %.2f GB (over by %.2f MB)",
		float64(totalBytes)/(1024*1024*1024),
		float64(maxBytes)/(1024*1024*1024),
		float64(overageBytes)/(1024*1024))

	shrunkHotHours := c.retention.HotHours / 2
	if shrunkHotHours < 1 {
		shrunkHotHours = 1
	}

	hotCutoff := c.now.Add(-time.Duration(shrunkHotHours) * time.Hour)
	originalHotCutoff := c.now.Add(-time.Duration(c.retention.HotHours) * time.Hour)

	aggressiveEvents, err := c.idx.QueryEvents(&index.Query{
		Since:     originalHotCutoff.UnixNano(),
		Until:     hotCutoff.UnixNano(),
		OrderDesc: false,
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query aggressive warm events: %w", err)
	}

	var aggressiveDelete []string
	for _, fsEvents := range groupByFileSession(aggressiveEvents) {
		aggressiveDelete = append(aggressiveDelete, hourlyCollapse(fsEvents)...)
	}
	if len(aggressiveDelete) == 0 {
		return 0, 0, 0, nil
	}

	removed, err := c.deleteBatch(aggressiveDelete, "storage-limit", "hot tier shrunk to half")
	if err != nil {
		return 0, 0, 0, err
	}

	gcResult, err := GarbageCollect(c.idx, c.objStore, c.dryRun)
	if err != nil {
		return removed, 0, 0, err
	}
	return removed, gcResult.OrphanedObjects, gcResult.BytesFreed, nil
}

// groupByFile organizes compactable events into a map keyed by file path.
func groupByFile(events []*schema.Event) map[string][]*schema.Event {
	byFile := make(map[string][]*schema.Event)
	for _, e := range events {
		if !compactable(e) {
			continue
		}
		byFile[e.FilePath] = append(byFile[e.FilePath], e)
	}
	return byFile
}

type fileSessionKey struct {
	filePath  string
	sessionID string
}

// groupByFileSession organizes compactable events by file+session, each group sorted ascending.
func groupByFileSession(events []*schema.Event) map[fileSessionKey][]*schema.Event {
	groups := make(map[fileSessionKey][]*schema.Event)
	for _, e := range events {
		if !compactable(e) {
			continue
		}
		key := fileSessionKey{filePath: e.FilePath, sessionID: e.SessionID}
		groups[key] = append(groups[key], e)
	}
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool {
			return g[i].TimestampNano < g[j].TimestampNano
		})
	}
	return groups
}
