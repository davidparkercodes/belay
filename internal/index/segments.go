package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/davidparkercodes/belay/internal/schema"
)

// SegmentCompactionResult summarizes a pass over sealed event-log segments.
type SegmentCompactionResult struct {
	SegmentsScanned   int   `json:"segments_scanned"`
	SegmentsRewritten int   `json:"segments_rewritten"`
	SegmentsDeleted   int   `json:"segments_deleted"`
	FramesKept        int   `json:"frames_kept"`
	FramesDropped     int   `json:"frames_dropped"`
	BytesFreed        int64 `json:"bytes_freed"`
}

// ErrSegmentCompactionBusy is returned when another process holds the segment compaction lock.
var ErrSegmentCompactionBusy = errors.New("segment compaction already running in another process")

// sessionMetaPath is the pseudo file path carried by session start/end meta-events, which are
// written to the log but never indexed. They must survive segment rewrites so a rebuild can
// still reconstruct session records.
const sessionMetaPath = ".belay/sessions"

// CompactSegments rewrites every sealed segment (all but the newest, which the daemon is still
// appending to) so that it contains only frames whose event still exists in the index, plus
// session meta-events. Segments left with no frames are deleted. Surviving events get their
// segment_offset updated. This is what makes index-level purge durable: a later Rebuild replays
// only what is left on disk.
func CompactSegments(idx *Index, eventsDir string, dryRun bool, logf func(string, ...interface{})) (*SegmentCompactionResult, error) {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	result := &SegmentCompactionResult{}

	segments, err := listSegmentFiles(eventsDir)
	if err != nil {
		return nil, err
	}
	sort.Strings(segments)
	if len(segments) <= 1 {
		return result, nil
	}
	sealed := segments[:len(segments)-1]

	if !dryRun {
		unlock, err := acquireSegmentLock(filepath.Join(eventsDir, ".compact.lock"))
		if err != nil {
			return nil, err
		}
		defer unlock()
	}

	for _, seg := range sealed {
		if err := compactOneSegment(idx, eventsDir, seg, dryRun, result, logf); err != nil {
			return result, fmt.Errorf("segment %s: %w", seg, err)
		}
	}

	return result, nil
}

// segmentCleanKey is the meta key recording "<live event count>:<file size>" for a segment that
// was verified to contain only live frames. If neither has changed, the segment is skipped
// without being read, which keeps hourly runs cheap once the backlog is gone.
func segmentCleanKey(seg string) string {
	return "segment_clean:" + seg
}

func compactOneSegment(idx *Index, eventsDir, seg string, dryRun bool, result *SegmentCompactionResult, logf func(string, ...interface{})) error {
	segPath := filepath.Join(eventsDir, seg)

	info, err := os.Stat(segPath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	liveCount, err := idx.CountEventsBySegment(seg)
	if err != nil {
		return err
	}
	fingerprint := fmt.Sprintf("%d:%d", liveCount, info.Size())
	if clean, _ := idx.GetMeta(segmentCleanKey(seg)); clean == fingerprint {
		return nil
	}

	result.SegmentsScanned++

	data, err := os.ReadFile(segPath)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	live, err := idx.EventIDsBySegment(seg)
	if err != nil {
		return err
	}

	frames, _ := scanFrames(data)

	var keptBytes int64
	kept := 0
	newOffsets := make(map[string]int64)
	keepFrame := make([]bool, len(frames))
	for i, f := range frames {
		if live[f.Event.EventID] || keepForRebuild(f.Event) {
			keepFrame[i] = true
			if live[f.Event.EventID] {
				newOffsets[f.Event.EventID] = keptBytes
			}
			keptBytes += int64(len(f.Raw))
			kept++
		}
	}

	dropped := len(frames) - kept
	freed := int64(len(data)) - keptBytes
	if dropped == 0 && freed == 0 {
		if !dryRun {
			_ = idx.SetMeta(segmentCleanKey(seg), fingerprint)
		}
		return nil
	}

	result.FramesKept += kept
	result.FramesDropped += dropped
	result.BytesFreed += freed

	if dryRun {
		logf("[dry-run] segment %s: would keep %d frames, drop %d, free %d bytes", seg, kept, dropped, freed)
		return nil
	}

	if kept == 0 {
		if err := os.Remove(segPath); err != nil {
			return fmt.Errorf("remove: %w", err)
		}
		result.SegmentsDeleted++
		_ = idx.DeleteMeta(segmentCleanKey(seg))
		logf("segment %s: no live events, deleted (%d bytes)", seg, len(data))
		return nil
	}

	tmpPath := segPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	for i, f := range frames {
		if !keepFrame[i] {
			continue
		}
		if _, err := tmp.Write(f.Raw); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write temp: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, segPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	syncDir(eventsDir)

	if err := idx.UpdateSegmentOffsets(seg, newOffsets); err != nil {
		return err
	}
	_ = idx.SetMeta(segmentCleanKey(seg), fmt.Sprintf("%d:%d", len(newOffsets), keptBytes))

	result.SegmentsRewritten++
	logf("segment %s: kept %d frames, dropped %d, freed %d bytes", seg, kept, dropped, freed)
	return nil
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// keepForRebuild reports whether an event decoded from a segment should survive a rewrite
// even though it is not in the index.
func keepForRebuild(e *schema.Event) bool {
	return e.FilePath == sessionMetaPath
}
