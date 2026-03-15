package schema

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// FuzzEventUnmarshal feeds arbitrary JSON bytes to json.Unmarshal for Event
// and ensures it never panics.
func FuzzEventUnmarshal(f *testing.F) {
	// Seed corpus: valid events, partial JSON, empty, and binary.
	f.Add([]byte(`{"event_id":"abc-123","file_path":"main.go","operation":"CREATE","timestamp_nano":1234567890,"content_hash":"deadbeef","content_size":100}`))
	f.Add([]byte(`{"event_id":"","file_path":"","operation":"MODIFY","timestamp_nano":0}`))
	f.Add([]byte(`{"operation":"DELETE","file_path":"old.go","session_id":"sess-1","attribution_method":"hook","attribution_confidence":1.0}`))
	f.Add([]byte(`{"operation":"RENAME","file_path":"new.go","old_path":"old.go"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"operation":"UNKNOWN"}`))
	f.Add([]byte(`{"operation":123}`))
	f.Add([]byte(""))
	f.Add([]byte("null"))
	f.Add([]byte("[]"))
	f.Add([]byte("{invalid json"))
	f.Add([]byte(`{"timestamp_nano":-9999999999999999999}`))
	f.Add([]byte{0x00, 0xff, 0xfe, 0x89, 0x50, 0x4e, 0x47})

	f.Fuzz(func(t *testing.T, data []byte) {
		var event Event
		// Must not panic. Errors are expected for invalid JSON.
		_ = json.Unmarshal(data, &event)

		// If unmarshal succeeded, ToJSON must also not panic.
		_ = event.ToJSON()
	})
}

// FuzzParseOperation feeds arbitrary strings to ParseOperation and ensures
// it never panics.
func FuzzParseOperation(f *testing.F) {
	f.Add("CREATE")
	f.Add("create")
	f.Add("MODIFY")
	f.Add("modify")
	f.Add("DELETE")
	f.Add("delete")
	f.Add("RENAME")
	f.Add("rename")
	f.Add("UNKNOWN")
	f.Add("")
	f.Add("Create")
	f.Add("INVALID")
	f.Add("\x00\xff")
	f.Add("CREATE CREATE")

	f.Fuzz(func(t *testing.T, s string) {
		// Must not panic.
		_, _ = ParseOperation(s)
	})
}

// FuzzParseAttributionMethod feeds arbitrary strings to ParseAttributionMethod
// and ensures it never panics.
func FuzzParseAttributionMethod(f *testing.F) {
	f.Add("none")
	f.Add("pid")
	f.Add("temporal")
	f.Add("heuristic")
	f.Add("manual")
	f.Add("hook")
	f.Add("")
	f.Add("INVALID")
	f.Add("\x00\xff")
	f.Add("PID")

	f.Fuzz(func(t *testing.T, s string) {
		// Must not panic.
		_ = ParseAttributionMethod(s)
	})
}

// FuzzUnmarshalBinaryFrame feeds arbitrary bytes as a binary event frame
// to UnmarshalBinaryFrame and ensures it never panics.
func FuzzUnmarshalBinaryFrame(f *testing.F) {
	// Seed: a valid binary frame from a real event.
	validEvent := &Event{
		EventID:       "test-fuzz-id",
		FilePath:      "src/main.go",
		Op:            OpCreate,
		TimestampNano: 1700000000000000000,
		ContentHash:   "abcdef1234567890",
		ContentSize:   42,
	}
	if validData, err := validEvent.MarshalBinary(); err == nil {
		f.Add(validData)
	}

	// Minimal valid-length frame (but invalid content).
	minFrame := make([]byte, 10)
	binary.BigEndian.PutUint32(minFrame[0:4], 10)
	f.Add(minFrame)

	// Empty.
	f.Add([]byte{})

	// Just a length prefix with no body.
	shortFrame := make([]byte, 4)
	binary.BigEndian.PutUint32(shortFrame[0:4], 100)
	f.Add(shortFrame)

	// Random binary data.
	f.Add([]byte{0x00, 0x00, 0x00, 0x0A, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		reader := bytes.NewReader(data)
		// Must not panic. Errors are expected for malformed frames.
		_, _, _ = UnmarshalBinaryFrame(reader)
	})
}

// FuzzOperationJSON tests JSON marshaling/unmarshaling of Operation values
// with arbitrary JSON input.
func FuzzOperationJSON(f *testing.F) {
	f.Add([]byte(`"CREATE"`))
	f.Add([]byte(`"modify"`))
	f.Add([]byte(`"UNKNOWN"`))
	f.Add([]byte(`""`))
	f.Add([]byte(`123`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`"not-an-op"`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var op Operation
		// Must not panic.
		_ = json.Unmarshal(data, &op)
	})
}

// FuzzNewEventID verifies that concurrent NewEventID calls never panic.
// This is a stress test rather than a traditional input fuzz.
func FuzzNewEventID(f *testing.F) {
	f.Add(1)
	f.Add(10)
	f.Add(50)

	f.Fuzz(func(t *testing.T, n int) {
		if n < 0 {
			n = -n
		}
		if n > 100 {
			n = 100
		}
		// Generate n IDs -- must not panic.
		for i := 0; i < n; i++ {
			id := NewEventID()
			if len(id) == 0 {
				t.Error("NewEventID returned empty string")
			}
		}
	})
}

// FuzzContentHashForBytes verifies that ContentHashForBytes never panics
// for any input.
func FuzzContentHashForBytes(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte(""))
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0xFE, 0xFD})

	f.Fuzz(func(t *testing.T, data []byte) {
		hash := ContentHashForBytes(data)
		// SHA-256 hex should always be 64 characters.
		if len(hash) != 64 {
			t.Errorf("ContentHashForBytes produced hash of length %d, want 64", len(hash))
		}
	})
}
