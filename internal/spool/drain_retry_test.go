package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDrainEntries_TransientErrorKeepsEntryInPlace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	first := newEvent("a.txt", "s1")
	time.Sleep(time.Millisecond)
	second := newEvent("b.txt", "s1")
	for _, e := range []*Entry{{Event: first}, {Event: second}} {
		if err := WriteEntry(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	n, err := DrainEntries(dir, func(e *Entry) error {
		calls++
		return errors.New("disk full")
	})
	if err == nil || n != 0 || calls != 1 {
		t.Fatalf("drain = %d, %v after %d calls; want a stop at the first transient error", n, err, calls)
	}
	if bad, _ := Bad(dir); len(bad) != 0 {
		t.Fatalf("transient failure was set aside: %v", bad)
	}
	var order []string
	n, err = DrainEntries(dir, func(e *Entry) error { order = append(order, e.Event.FilePath); return nil })
	if err != nil || n != 2 || order[0] != "a.txt" || order[1] != "b.txt" {
		t.Fatalf("retry drained %d (%v), order %v", n, err, order)
	}
}

func TestDrainEntries_QuarantineSetsAside(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	ev := newEvent("a.txt", "s1")
	if err := WriteEntry(dir, &Entry{Event: ev, Sealed: []byte("sealed")}); err != nil {
		t.Fatal(err)
	}
	n, err := DrainEntries(dir, func(*Entry) error { return &Quarantine{Err: errors.New("cannot open")} })
	if err != nil || n != 0 {
		t.Fatalf("drain = %d, %v", n, err)
	}
	bad, _ := Bad(dir)
	if len(bad) != 1 {
		t.Fatalf("bad = %v", bad)
	}

	if n, _ := Requeue(dir, func([]byte) bool { return false }); n != 0 {
		t.Fatal("requeued an entry that still cannot be opened")
	}
	if n, err := Requeue(dir, func(s []byte) bool { return string(s) == "sealed" }); err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	if pending, _ := Pending(dir); len(pending) != 1 {
		t.Fatalf("pending after requeue = %v", pending)
	}
	if _, err := os.Stat(bad[0]); !os.IsNotExist(err) {
		t.Fatal(".bad file still present after requeue")
	}
}
