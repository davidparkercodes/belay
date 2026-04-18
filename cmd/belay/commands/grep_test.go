package commands

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

func TestGrepCmd_BasicProperties(t *testing.T) {
	cmd := newGrepCmd()

	if cmd.Use != "grep PATTERN" {
		t.Errorf("Use = %q, want %q", cmd.Use, "grep PATTERN")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestGrepCmd_Flags(t *testing.T) {
	cmd := newGrepCmd()

	tests := []struct {
		name     string
		defValue string
	}{
		{"session", ""},
		{"file", ""},
		{"since", ""},
		{"until", ""},
		{"ignore-case", "false"},
		{"regex", "false"},
		{"limit", "50"},
		{"scan-limit", "20000"},
		{"json", "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.name)
			if f == nil {
				t.Fatalf("flag --%s not registered", tt.name)
			}
			if f.DefValue != tt.defValue {
				t.Errorf("--%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
			}
		})
	}
}

// ─── matcher tests ──────────────────────────────────────────────────────────

func TestMatcher_Literal(t *testing.T) {
	m, err := buildMatcher("foo", false, false)
	if err != nil {
		t.Fatalf("buildMatcher: %v", err)
	}
	if got := m.count([]byte("foo bar foo baz FOO")); got != 2 {
		t.Errorf("literal count = %d, want 2", got)
	}
}

func TestMatcher_LiteralIgnoreCase(t *testing.T) {
	m, err := buildMatcher("foo", true, false)
	if err != nil {
		t.Fatalf("buildMatcher: %v", err)
	}
	if got := m.count([]byte("foo bar FOO baz Foo")); got != 3 {
		t.Errorf("case-insensitive count = %d, want 3", got)
	}
}

func TestMatcher_Regex(t *testing.T) {
	m, err := buildMatcher(`foo[0-9]+`, false, true)
	if err != nil {
		t.Fatalf("buildMatcher: %v", err)
	}
	if got := m.count([]byte("foo1 foo bar foo42 FOO9")); got != 2 {
		t.Errorf("regex count = %d, want 2", got)
	}
}

func TestMatcher_RegexIgnoreCase(t *testing.T) {
	m, err := buildMatcher(`foo[0-9]+`, true, true)
	if err != nil {
		t.Fatalf("buildMatcher: %v", err)
	}
	if got := m.count([]byte("foo1 FOO9 BAZ Foo7")); got != 3 {
		t.Errorf("regex+ignorecase count = %d, want 3", got)
	}
}

func TestMatcher_RegexInvalid(t *testing.T) {
	if _, err := buildMatcher(`(unterminated`, false, true); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestMatcher_EmptyInput(t *testing.T) {
	m, _ := buildMatcher("foo", false, false)
	if got := m.count(nil); got != 0 {
		t.Errorf("nil count = %d, want 0", got)
	}
	if got := m.count([]byte{}); got != 0 {
		t.Errorf("empty count = %d, want 0", got)
	}
}

// ─── formatDelta ────────────────────────────────────────────────────────────

func TestFormatDelta(t *testing.T) {
	tests := []struct {
		in       int
		contains string
	}{
		{3, "+3"},
		{-2, "-2"},
		{0, "0"},
	}
	for _, tt := range tests {
		if got := formatDelta(tt.in); got == "" {
			t.Errorf("formatDelta(%d) is empty", tt.in)
		} else if !containsStr(got, tt.contains) {
			t.Errorf("formatDelta(%d) = %q, should contain %q", tt.in, got, tt.contains)
		}
	}
}

// ─── pickaxeEvents integration ──────────────────────────────────────────────

func TestPickaxeEvents_DetectsAddAndRemove(t *testing.T) {
	dir := t.TempDir()
	objStore, err := store.NewStore(filepath.Join(dir, "objects"), false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Blob fixtures.
	noPhrase := []byte("line 1\nline 2\nline 3\n")
	onePhrase := []byte("line 1\nEnter to send, Shift+Enter for new line\nline 3\n")
	twoPhrase := []byte("Enter to send, Shift+Enter for new line\nEnter to send, Shift+Enter for new line\n")

	hashEmpty, _, err := objStore.Put(noPhrase)
	if err != nil {
		t.Fatalf("put noPhrase: %v", err)
	}
	hashOne, _, err := objStore.Put(onePhrase)
	if err != nil {
		t.Fatalf("put onePhrase: %v", err)
	}
	hashTwo, _, err := objStore.Put(twoPhrase)
	if err != nil {
		t.Fatalf("put twoPhrase: %v", err)
	}

	base := time.Date(2026, 4, 11, 8, 0, 0, 0, time.UTC)

	// Event A: add one occurrence (0 → 1).  Delta = +1.
	addOne := &schema.Event{
		EventID:       "evt-add",
		TimestampNano: base.UnixNano(),
		FilePath:      "ChatInput.tsx",
		Op:            schema.OpModify,
		ContentHash:   hashOne,
		PreviousHash:  hashEmpty,
		SessionID:     "sess-a",
	}
	// Event B: grow from 1 → 2. Delta = +1.
	grow := &schema.Event{
		EventID:       "evt-grow",
		TimestampNano: base.Add(1 * time.Minute).UnixNano(),
		FilePath:      "ChatInput.tsx",
		Op:            schema.OpModify,
		ContentHash:   hashTwo,
		PreviousHash:  hashOne,
		SessionID:     "sess-a",
	}
	// Event C: shrink from 2 → 0. Delta = -2.
	remove := &schema.Event{
		EventID:       "evt-remove",
		TimestampNano: base.Add(2 * time.Minute).UnixNano(),
		FilePath:      "ChatInput.tsx",
		Op:            schema.OpModify,
		ContentHash:   hashEmpty,
		PreviousHash:  hashTwo,
		SessionID:     "sess-b",
	}
	// Event D: unrelated modify that does NOT touch the phrase (1 → 1). Must NOT match.
	unrelated := &schema.Event{
		EventID:       "evt-nop",
		TimestampNano: base.Add(3 * time.Minute).UnixNano(),
		FilePath:      "Other.tsx",
		Op:            schema.OpModify,
		ContentHash:   hashOne,
		PreviousHash:  hashOne,
		SessionID:     "sess-a",
	}

	m, err := buildMatcher("Enter to send, Shift+Enter for new line", false, false)
	if err != nil {
		t.Fatalf("buildMatcher: %v", err)
	}

	events := []*schema.Event{remove, grow, addOne, unrelated} // newest-first, like the index returns
	matches, scanned, err := pickaxeEvents(events, objStore, m, 0)
	if err != nil {
		t.Fatalf("pickaxeEvents: %v", err)
	}
	if scanned != 4 {
		t.Errorf("scanned = %d, want 4", scanned)
	}
	if len(matches) != 3 {
		t.Fatalf("matches = %d, want 3 (+1, +1, -2). got IDs: %v", len(matches), idsOf(matches))
	}

	wantDelta := map[string]int{
		"evt-remove": -2,
		"evt-grow":   1,
		"evt-add":    1,
	}
	for _, mm := range matches {
		want, ok := wantDelta[mm.Event.EventID]
		if !ok {
			t.Errorf("unexpected match: %s", mm.Event.EventID)
			continue
		}
		if mm.Delta != want {
			t.Errorf("event %s delta = %d, want %d", mm.Event.EventID, mm.Delta, want)
		}
	}
}

func TestPickaxeEvents_RespectsLimit(t *testing.T) {
	dir := t.TempDir()
	objStore, err := store.NewStore(filepath.Join(dir, "objects"), false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	withPhrase := []byte("hello world\n")
	withoutPhrase := []byte("goodnight\n")

	hashHit, _, _ := objStore.Put(withPhrase)
	hashMiss, _, _ := objStore.Put(withoutPhrase)

	var events []*schema.Event
	for i := 0; i < 5; i++ {
		events = append(events, &schema.Event{
			EventID:       "e" + string(rune('0'+i)),
			TimestampNano: time.Now().Add(time.Duration(i) * time.Minute).UnixNano(),
			FilePath:      "f.txt",
			Op:            schema.OpModify,
			ContentHash:   hashHit,
			PreviousHash:  hashMiss,
		})
	}

	m, _ := buildMatcher("hello", false, false)
	matches, scanned, err := pickaxeEvents(events, objStore, m, 2)
	if err != nil {
		t.Fatalf("pickaxeEvents: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("matches = %d, want 2 (limit)", len(matches))
	}
	if scanned != 2 {
		t.Errorf("scanned = %d, want 2 (short-circuit at limit)", scanned)
	}
}

func TestPickaxeEvents_MissingBlobTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	objStore, err := store.NewStore(filepath.Join(dir, "objects"), false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	withPhrase := []byte("TODO: fix this\n")
	hash, _, _ := objStore.Put(withPhrase)

	// Previous hash refers to a blob that doesn't exist on disk — treat as empty.
	evt := &schema.Event{
		EventID:       "evt",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "notes.md",
		Op:            schema.OpModify,
		ContentHash:   hash,
		PreviousHash:  "deadbeef0000000000000000000000000000000000000000000000000000dead",
	}

	m, _ := buildMatcher("TODO", false, false)
	matches, _, err := pickaxeEvents([]*schema.Event{evt}, objStore, m, 0)
	if err != nil {
		t.Fatalf("pickaxeEvents: %v", err)
	}
	if len(matches) != 1 || matches[0].Delta != 1 {
		t.Errorf("expected one match with delta +1, got %+v", matches)
	}
}

func idsOf(ms []grepMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Event.EventID
	}
	return out
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
