// Package eventlog implements an append-only, segmented event log for persistent event storage.
package eventlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

const (
	segmentSuffix = ".log"
	segmentFormat = "20060102-150405"
)

// Writer appends binary-encoded events to time-stamped segment files, rotating when
// the current segment exceeds the configured maximum size.
type Writer struct {
	eventsDir       string
	maxSegmentBytes int64

	mu             sync.Mutex
	currentFile    *os.File
	currentSegment string
	currentSize    int64
}

// NewWriter creates a Writer that writes to segment files in eventsDir, rotating
// segments when they exceed maxSegmentBytes.
func NewWriter(eventsDir string, maxSegmentBytes int64) (*Writer, error) {
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		return nil, fmt.Errorf("create events dir: %w", err)
	}

	w := &Writer{
		eventsDir:       eventsDir,
		maxSegmentBytes: maxSegmentBytes,
	}

	segments, err := listSegments(eventsDir)
	if err != nil {
		return nil, fmt.Errorf("list segments: %w", err)
	}

	if len(segments) > 0 {
		latest := segments[len(segments)-1]
		if err := w.openSegment(latest); err != nil {
			return nil, fmt.Errorf("open latest segment: %w", err)
		}
	} else {
		if err := w.rotateSegment(); err != nil {
			return nil, fmt.Errorf("create first segment: %w", err)
		}
	}

	return w, nil
}

// Append writes a binary-encoded event to the current segment, rotating if needed.
func (w *Writer) Append(event *schema.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	data, err := event.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	if w.currentSize+int64(len(data)) > w.maxSegmentBytes && w.currentSize > 0 {
		if err := w.rotateSegment(); err != nil {
			return fmt.Errorf("rotate segment: %w", err)
		}
	}

	n, err := w.currentFile.Write(data)
	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	w.currentSize += int64(n)

	return nil
}

// Sync flushes the current segment file to disk.
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile != nil {
		return w.currentFile.Sync()
	}
	return nil
}

// Close syncs and closes the current segment file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile != nil {
		if err := w.currentFile.Sync(); err != nil {
			w.currentFile.Close()
			return fmt.Errorf("sync on close: %w", err)
		}
		return w.currentFile.Close()
	}
	return nil
}

// CurrentSegment returns the filename of the currently active segment.
func (w *Writer) CurrentSegment() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.currentSegment
}

// CurrentOffset returns the current byte offset within the active segment.
func (w *Writer) CurrentOffset() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.currentSize
}

func (w *Writer) openSegment(filename string) error {
	path := filepath.Join(w.eventsDir, filename)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open segment %s: %w", filename, err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat segment: %w", err)
	}

	w.currentFile = f
	w.currentSegment = filename
	w.currentSize = info.Size()
	return nil
}

func (w *Writer) rotateSegment() error {
	if w.currentFile != nil {
		if err := w.currentFile.Sync(); err != nil {
			return fmt.Errorf("sync before rotate: %w", err)
		}
		if err := w.currentFile.Close(); err != nil {
			return fmt.Errorf("close before rotate: %w", err)
		}
	}

	filename := time.Now().Format(segmentFormat) + segmentSuffix
	path := filepath.Join(w.eventsDir, filename)

	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		filename = fmt.Sprintf("%s-%d%s", time.Now().Format(segmentFormat), i, segmentSuffix)
		path = filepath.Join(w.eventsDir, filename)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("create segment %s: %w", filename, err)
	}

	w.currentFile = f
	w.currentSegment = filename
	w.currentSize = 0
	return nil
}

func listSegments(eventsDir string) ([]string, error) {
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var segments []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), segmentSuffix) {
			segments = append(segments, entry.Name())
		}
	}

	sort.Strings(segments)
	return segments, nil
}
