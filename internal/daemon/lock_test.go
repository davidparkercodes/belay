package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPIDLock_FirstAcquireWritesPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	lock, err := acquirePIDLock(path)
	if err != nil {
		t.Fatalf("acquirePIDLock: %v", err)
	}
	t.Cleanup(func() { lock.Release() })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("parse pid %q: %v", string(data), err)
	}
	if pid != os.Getpid() {
		t.Fatalf("pidfile pid = %d, want %d", pid, os.Getpid())
	}
}

func TestPIDLock_SecondAcquireFailsWhileFirstHeld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	first, err := acquirePIDLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	t.Cleanup(func() { first.Release() })

	_, err = acquirePIDLock(path)
	if err == nil {
		t.Fatalf("second acquire succeeded; expected alreadyRunningError")
	}
	if !IsAlreadyRunning(err) {
		t.Fatalf("second acquire returned %v; expected alreadyRunningError", err)
	}
	if RunningPID(err) != os.Getpid() {
		t.Fatalf("RunningPID = %d, want %d", RunningPID(err), os.Getpid())
	}
}

func TestPIDLock_AcquireSucceedsAfterRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	first, err := acquirePIDLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	first.Release()

	second, err := acquirePIDLock(path)
	if err != nil {
		t.Fatalf("second acquire after release: %v", err)
	}
	t.Cleanup(func() { second.Release() })
}

func TestPIDLock_AcquireSucceedsOnStalePIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	// Simulate a stale pidfile from a crashed daemon: file exists with an old
	// PID but no process holds the flock.
	if err := os.WriteFile(path, []byte("999999"), 0644); err != nil {
		t.Fatalf("write stale pidfile: %v", err)
	}

	lock, err := acquirePIDLock(path)
	if err != nil {
		t.Fatalf("acquire on stale pidfile: %v", err)
	}
	t.Cleanup(func() { lock.Release() })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("stale pidfile not overwritten: pid = %d, want %d", pid, os.Getpid())
	}
}
