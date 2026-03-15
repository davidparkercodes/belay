package eventlog

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/davidparkercodes/belay/internal/schema"
)

// Reader reads events from segment files in the events directory.
type Reader struct {
	eventsDir string
}

// NewReader creates a Reader for the given events directory.
func NewReader(eventsDir string) (*Reader, error) {
	if _, err := os.Stat(eventsDir); err != nil {
		return nil, fmt.Errorf("events dir not found: %w", err)
	}
	return &Reader{eventsDir: eventsDir}, nil
}

// Segments returns the sorted list of segment filenames.
func (r *Reader) Segments() ([]string, error) {
	return listSegments(r.eventsDir)
}

// ReadSegment reads all events from a single segment file.
func (r *Reader) ReadSegment(filename string) ([]*schema.Event, error) {
	path := filepath.Join(r.eventsDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read segment %s: %w", filename, err)
	}

	return readEventsFromBytes(data)
}

// ReadAll reads all events from all segments in chronological order.
func (r *Reader) ReadAll() ([]*schema.Event, error) {
	segments, err := r.Segments()
	if err != nil {
		return nil, err
	}

	var allEvents []*schema.Event
	for _, seg := range segments {
		events, err := r.ReadSegment(seg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: error reading segment %s: %v\n", seg, err)
			continue
		}
		allEvents = append(allEvents, events...)
	}

	return allEvents, nil
}

// ReadFrom reads events from a segment starting at the given byte offset.
func (r *Reader) ReadFrom(segmentFile string, offset int64) ([]*schema.Event, error) {
	path := filepath.Join(r.eventsDir, segmentFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read segment %s: %w", segmentFile, err)
	}

	if offset >= int64(len(data)) {
		return nil, nil
	}

	return readEventsFromBytes(data[offset:])
}

// EventWithOffset pairs a decoded event with its byte offset and size within a segment.
type EventWithOffset struct {
	Event  *schema.Event
	Offset int64
	Size   int
}

// ReadSegmentWithOffsets reads a segment and returns events with their byte offsets.
func (r *Reader) ReadSegmentWithOffsets(filename string) ([]EventWithOffset, error) {
	path := filepath.Join(r.eventsDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read segment %s: %w", filename, err)
	}

	var results []EventWithOffset
	reader := bytes.NewReader(data)
	var offset int64

	for reader.Len() > 0 {
		event, bytesRead, err := schema.UnmarshalBinaryFrame(reader)
		if err != nil {
			break
		}
		results = append(results, EventWithOffset{
			Event:  event,
			Offset: offset,
			Size:   bytesRead,
		})
		offset += int64(bytesRead)
	}

	return results, nil
}

func readEventsFromBytes(data []byte) ([]*schema.Event, error) {
	reader := bytes.NewReader(data)
	var events []*schema.Event

	for reader.Len() > 0 {
		event, _, err := schema.UnmarshalBinaryFrame(reader)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			break
		}
		events = append(events, event)
	}

	return events, nil
}
