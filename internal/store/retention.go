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
	// StrategyHourly collapses rapid edits to hourly granularity.
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
	EventsReviewed int            `json:"events_reviewed"`
	EventsKept     int            `json:"events_kept"`
	EventsRemoved  int            `json:"events_removed"`
	BytesFreed     int64          `json:"bytes_freed"`
	TierBreakdown  map[string]int `json:"tier_breakdown"`
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

// rapidEditWindow is the maximum time between consecutive modify events on the same
// file+session to be considered a "rapid edit" burst eligible for warm-tier collapsing.
const rapidEditWindow = 60 * time.Second

// Compactor applies tiered retention compaction to the Belay event store.
type Compactor struct {
	idx       *index.Index
	objStore  *Store
	retention *config.RetentionConfig
	dryRun    bool
	now       time.Time
}

// NewCompactor creates a Compactor with the given dependencies.
func NewCompactor(idx *index.Index, objStore *Store, retention *config.RetentionConfig, dryRun bool) *Compactor {
	return &Compactor{
		idx:       idx,
		objStore:  objStore,
		retention: retention,
		dryRun:    dryRun,
		now:       time.Now(),
	}
}

// RunCompaction applies all compaction tiers in order: purge, archive, cold, warm,
// then enforces the storage limit. Returns a summary of all changes.
func (c *Compactor) RunCompaction() (*CompactionResult, error) {
	result := &CompactionResult{
		TierBreakdown: make(map[string]int),
	}

	// Phase 1: Purge events older than archive tier
	purged, err := c.purge()
	if err != nil {
		return nil, fmt.Errorf("purge: %w", err)
	}
	result.EventsRemoved += purged
	result.TierBreakdown["purged"] = purged

	// Phase 2: Archive tier compaction (daily snapshots)
	archiveRemoved, err := c.compactArchive()
	if err != nil {
		return nil, fmt.Errorf("archive compaction: %w", err)
	}
	result.EventsRemoved += archiveRemoved
	result.TierBreakdown["archive_compacted"] = archiveRemoved

	// Phase 3: Cold tier compaction (session boundaries only)
	coldRemoved, err := c.compactCold()
	if err != nil {
		return nil, fmt.Errorf("cold compaction: %w", err)
	}
	result.EventsRemoved += coldRemoved
	result.TierBreakdown["cold_compacted"] = coldRemoved

	// Phase 4: Warm tier compaction (collapse rapid edits)
	warmRemoved, err := c.compactWarm()
	if err != nil {
		return nil, fmt.Errorf("warm compaction: %w", err)
	}
	result.EventsRemoved += warmRemoved
	result.TierBreakdown["warm_compacted"] = warmRemoved

	// Phase 5: Garbage collect orphaned objects
	gcResult, err := GarbageCollect(c.idx, c.objStore, c.dryRun)
	if err != nil {
		return nil, fmt.Errorf("garbage collect: %w", err)
	}
	result.BytesFreed += gcResult.BytesFreed

	// Phase 6: Enforce storage limit
	storageRemoved, storageFreed, err := c.enforceStorageLimit()
	if err != nil {
		return nil, fmt.Errorf("storage limit: %w", err)
	}
	result.EventsRemoved += storageRemoved
	result.BytesFreed += storageFreed
	if storageRemoved > 0 {
		result.TierBreakdown["storage_limit"] = storageRemoved
	}

	// Count remaining events
	totalEvents, err := c.idx.CountEvents()
	if err != nil {
		return nil, fmt.Errorf("count events: %w", err)
	}
	result.EventsKept = int(totalEvents)
	result.EventsReviewed = result.EventsKept + result.EventsRemoved

	return result, nil
}

// purge deletes all events older than the archive tier (if archive_days > 0).
func (c *Compactor) purge() (int, error) {
	if c.retention.ArchiveDays <= 0 {
		log.Printf("belay: purge skipped (archive_days=0, retain forever)")
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
			log.Printf("belay: [dry-run] would purge %d events older than %d days", count, c.retention.ArchiveDays)
		}
		return count, nil
	}

	deleted, err := c.idx.DeleteEventsBefore(cutoffNano)
	if err != nil {
		return 0, err
	}

	if deleted > 0 {
		log.Printf("belay: purged %d events older than %d days", deleted, c.retention.ArchiveDays)
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

	if len(events) == 0 {
		return 0, nil
	}

	// Group by file path
	byFile := groupByFile(events)

	var toDelete []string
	for _, fileEvents := range byFile {
		// Sort by timestamp ascending
		sort.Slice(fileEvents, func(i, j int) bool {
			return fileEvents[i].TimestampNano < fileEvents[j].TimestampNano
		})

		// Keep one event per file per day (the last event of each day)
		dayKey := func(e *schema.Event) string {
			return e.Timestamp().Format("2006-01-02")
		}

		byDay := make(map[string][]*schema.Event)
		for _, e := range fileEvents {
			key := dayKey(e)
			byDay[key] = append(byDay[key], e)
		}

		for _, dayEvents := range byDay {
			if len(dayEvents) <= 1 {
				continue
			}
			// Keep the last event of the day, remove the rest
			for _, e := range dayEvents[:len(dayEvents)-1] {
				toDelete = append(toDelete, e.EventID)
			}
		}
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	if c.dryRun {
		log.Printf("belay: [dry-run] would compact %d archive-tier events (daily snapshots)", len(toDelete))
		return len(toDelete), nil
	}

	deleted, err := c.idx.DeleteEventsBatch(toDelete)
	if err != nil {
		return 0, err
	}

	log.Printf("belay: compacted %d archive-tier events to daily snapshots", deleted)
	return int(deleted), nil
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

	if len(events) == 0 {
		return 0, nil
	}

	// Group by file+session
	type fileSessionKey struct {
		filePath  string
		sessionID string
	}
	byFileSession := make(map[fileSessionKey][]*schema.Event)
	for _, e := range events {
		key := fileSessionKey{filePath: e.FilePath, sessionID: e.SessionID}
		byFileSession[key] = append(byFileSession[key], e)
	}

	var toDelete []string
	for _, fsEvents := range byFileSession {
		if len(fsEvents) <= 2 {
			continue
		}

		// Sort by timestamp ascending
		sort.Slice(fsEvents, func(i, j int) bool {
			return fsEvents[i].TimestampNano < fsEvents[j].TimestampNano
		})

		// Keep first and last, remove everything in between
		for _, e := range fsEvents[1 : len(fsEvents)-1] {
			toDelete = append(toDelete, e.EventID)
		}
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	if c.dryRun {
		log.Printf("belay: [dry-run] would compact %d cold-tier events (session boundaries)", len(toDelete))
		return len(toDelete), nil
	}

	deleted, err := c.idx.DeleteEventsBatch(toDelete)
	if err != nil {
		return 0, err
	}

	log.Printf("belay: compacted %d cold-tier events to session boundaries", deleted)
	return int(deleted), nil
}

// compactWarm collapses rapid consecutive modify events on the same file+session
// within a short window, keeping only the first previous_hash and last content_hash.
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

	if len(events) == 0 {
		return 0, nil
	}

	// Group by file+session
	type fileSessionKey struct {
		filePath  string
		sessionID string
	}
	byFileSession := make(map[fileSessionKey][]*schema.Event)
	for _, e := range events {
		key := fileSessionKey{filePath: e.FilePath, sessionID: e.SessionID}
		byFileSession[key] = append(byFileSession[key], e)
	}

	var toDelete []string
	for _, fsEvents := range byFileSession {
		if len(fsEvents) < 2 {
			continue
		}

		// Sort by timestamp ascending
		sort.Slice(fsEvents, func(i, j int) bool {
			return fsEvents[i].TimestampNano < fsEvents[j].TimestampNano
		})

		// Identify bursts of rapid modify events
		toDelete = append(toDelete, findRapidEditBursts(fsEvents)...)
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	if c.dryRun {
		log.Printf("belay: [dry-run] would compact %d warm-tier events (rapid edits)", len(toDelete))
		return len(toDelete), nil
	}

	deleted, err := c.idx.DeleteEventsBatch(toDelete)
	if err != nil {
		return 0, err
	}

	log.Printf("belay: compacted %d warm-tier events (rapid edits collapsed)", deleted)
	return int(deleted), nil
}

// findRapidEditBursts identifies consecutive modify events within the rapid edit window
// and returns the event IDs of intermediate events to delete. The first and last event
// of each burst are retained, preserving the first previous_hash and last content_hash.
func findRapidEditBursts(events []*schema.Event) []string {
	if len(events) < 2 {
		return nil
	}

	var toDelete []string

	// Walk through events finding bursts of rapid modifies
	burstStart := 0
	for i := 1; i <= len(events); i++ {
		// Check if this event continues the current burst
		inBurst := false
		if i < len(events) {
			prev := events[i-1]
			curr := events[i]
			// Both must be modify operations and within the rapid edit window
			if prev.Op == schema.OpModify && curr.Op == schema.OpModify {
				gap := curr.Timestamp().Sub(prev.Timestamp())
				if gap <= rapidEditWindow {
					inBurst = true
				}
			}
		}

		if !inBurst {
			// End of burst (or end of events). If burst has > 1 event, mark intermediates.
			burstEnd := i - 1
			if burstEnd > burstStart {
				// Keep burstStart and burstEnd, delete everything in between
				for j := burstStart + 1; j < burstEnd; j++ {
					if events[j].Op == schema.OpModify {
						toDelete = append(toDelete, events[j].EventID)
					}
				}
			}
			burstStart = i
		}
	}

	return toDelete
}

// enforceStorageLimit checks total storage usage and applies increasingly aggressive
// compaction if the max_storage_gb limit is exceeded.
func (c *Compactor) enforceStorageLimit() (int, int64, error) {
	if c.retention.MaxStorageGB <= 0 {
		return 0, 0, nil
	}

	maxBytes := int64(c.retention.MaxStorageGB) * 1024 * 1024 * 1024

	totalBytes, _, err := c.objStore.Size()
	if err != nil {
		return 0, 0, fmt.Errorf("check storage size: %w", err)
	}

	if totalBytes <= maxBytes {
		return 0, 0, nil
	}

	overageBytes := totalBytes - maxBytes
	log.Printf("belay: storage %.2f GB exceeds limit %.2f GB (over by %.2f MB)",
		float64(totalBytes)/(1024*1024*1024),
		float64(maxBytes)/(1024*1024*1024),
		float64(overageBytes)/(1024*1024))

	totalRemoved := 0
	var totalFreed int64

	// Strategy 1: Apply warm-tier rules to the hot tier (shrink hot window to half)
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
		return 0, 0, fmt.Errorf("query aggressive warm events: %w", err)
	}

	if len(aggressiveEvents) > 0 {
		type fileSessionKey struct {
			filePath  string
			sessionID string
		}
		byFS := make(map[fileSessionKey][]*schema.Event)
		for _, e := range aggressiveEvents {
			key := fileSessionKey{filePath: e.FilePath, sessionID: e.SessionID}
			byFS[key] = append(byFS[key], e)
		}

		var aggressiveDelete []string
		for _, fsEvents := range byFS {
			if len(fsEvents) < 2 {
				continue
			}
			sort.Slice(fsEvents, func(i, j int) bool {
				return fsEvents[i].TimestampNano < fsEvents[j].TimestampNano
			})
			aggressiveDelete = append(aggressiveDelete, findRapidEditBursts(fsEvents)...)
		}

		if len(aggressiveDelete) > 0 {
			if c.dryRun {
				log.Printf("belay: [dry-run] storage limit: would aggressively compact %d events from hot tier", len(aggressiveDelete))
				totalRemoved += len(aggressiveDelete)
			} else {
				deleted, delErr := c.idx.DeleteEventsBatch(aggressiveDelete)
				if delErr != nil {
					return totalRemoved, totalFreed, delErr
				}
				totalRemoved += int(deleted)
				log.Printf("belay: storage limit: aggressively compacted %d events from hot tier", deleted)
			}

			// Run GC to free orphaned objects
			gcResult, gcErr := GarbageCollect(c.idx, c.objStore, c.dryRun)
			if gcErr != nil {
				return totalRemoved, totalFreed, gcErr
			}
			totalFreed += gcResult.BytesFreed
		}
	}

	return totalRemoved, totalFreed, nil
}

// groupByFile organizes events into a map keyed by file path.
func groupByFile(events []*schema.Event) map[string][]*schema.Event {
	byFile := make(map[string][]*schema.Event)
	for _, e := range events {
		byFile[e.FilePath] = append(byFile[e.FilePath], e)
	}
	return byFile
}
