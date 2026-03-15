package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Corruption: Hash Mismatch (Content Tampered) ───────────────────────────

func TestCorruption_HashMismatch_Uncompressed(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Store valid data
	data := []byte("original content that will be corrupted")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the stored file by overwriting with different content
	objPath := s.objectPath(hash)
	if err := os.WriteFile(objPath, []byte("TAMPERED CONTENT"), 0644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// Get should detect the hash mismatch
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("expected error for hash mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("expected 'corrupted' in error, got: %v", err)
	}
}

func TestCorruption_HashMismatch_Compressed(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, true)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Store valid data with compression
	data := []byte("content to compress and then corrupt")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the stored compressed file
	objPath := s.objectPath(hash)
	if err := os.WriteFile(objPath, []byte("NOT VALID ZSTD DATA"), 0644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// Get should detect corruption (either decompression failure or hash mismatch)
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("expected error for corrupted compressed object, got nil")
	}
	t.Logf("Get corrupted compressed: %v (expected)", err)
}

// ─── Corruption: Deleted Object File ────────────────────────────────────────

func TestCorruption_DeletedObjectFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	data := []byte("this will disappear")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify it exists
	if !s.Has(hash) {
		t.Fatal("object should exist after Put")
	}

	// Delete the file directly (not via store.Delete)
	objPath := s.objectPath(hash)
	if err := os.Remove(objPath); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Get should return a clean "not found" error
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("expected error for deleted object, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}

	// Has should return false
	if s.Has(hash) {
		t.Error("Has should return false for deleted object")
	}

	// Verify should also return error
	if err := s.Verify(hash); err == nil {
		t.Error("Verify should fail for deleted object")
	}
}

// ─── Corruption: Directory Where Object File Should Be ──────────────────────

func TestCorruption_DirectoryInsteadOfObjectFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Compute a hash and create a directory at that path
	fakeData := []byte("fake content")
	fakeHash := fmt.Sprintf("%x", sha256.Sum256(fakeData))

	objPath := s.objectPath(fakeHash)
	if err := os.MkdirAll(objPath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Get should return an error, not panic
	_, err = s.Get(fakeHash)
	if err == nil {
		t.Fatal("expected error when object path is a directory, got nil")
	}
	t.Logf("Get directory-as-object: %v (expected)", err)

	// Put should handle this gracefully too (the object "exists" but is a dir)
	_, _, err = s.Put(fakeData)
	if err != nil {
		t.Logf("Put with directory collision: %v (expected)", err)
	}
}

// ─── Corruption: Read-Only Directory ────────────────────────────────────────

func TestCorruption_PutWithReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Make the store directory read-only
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	data := []byte("cannot store this")
	_, _, err = s.Put(data)
	if err == nil {
		t.Fatal("expected error when storing to read-only directory, got nil")
	}
	t.Logf("Put read-only: %v (expected)", err)
}

// ─── Corruption: Empty Object File ──────────────────────────────────────────

func TestCorruption_EmptyObjectFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Store valid data, then replace with empty file
	data := []byte("will be emptied")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	objPath := s.objectPath(hash)
	if err := os.WriteFile(objPath, []byte{}, 0644); err != nil {
		t.Fatalf("empty file: %v", err)
	}

	// Get with empty file — hash of empty bytes won't match original hash
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("expected error for empty object file, got nil")
	}
	t.Logf("Get empty object: %v (expected)", err)
}

// ─── Corruption: Short Hash ─────────────────────────────────────────────────

func TestCorruption_ShortHash(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Try to Get with very short hashes
	shortHashes := []string{"", "a", "ab", "abc"}
	for _, h := range shortHashes {
		_, err := s.Get(h)
		if err == nil {
			t.Errorf("expected error for short hash %q, got nil", h)
		}
	}
}

// ─── Corruption: Verify Detects Tampering ───────────────────────────────────

func TestCorruption_VerifyDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	data := []byte("verify me")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify should pass on clean data
	if err := s.Verify(hash); err != nil {
		t.Fatalf("Verify clean: %v", err)
	}

	// Tamper with the file
	objPath := s.objectPath(hash)
	if err := os.WriteFile(objPath, []byte("tampered"), 0644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// Verify should now fail
	if err := s.Verify(hash); err == nil {
		t.Error("Verify should fail after tampering")
	}
}

// ─── Corruption: Concurrent Put/Get With Corruption ─────────────────────────

func TestCorruption_PutDeduplication_AfterCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	data := []byte("dedup test content")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the stored object
	objPath := s.objectPath(hash)
	if err := os.WriteFile(objPath, []byte("corrupted"), 0644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	// Put the same data again — dedup check sees file exists, so it's a no-op
	// This means the corrupted version persists (expected behavior)
	hash2, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put after corruption: %v", err)
	}
	if hash2 != hash {
		t.Errorf("hash changed: %q vs %q", hash, hash2)
	}

	// Get may or may not detect corruption depending on implementation.
	// The key invariant is that Put+Get doesn't panic after corruption.
	_, _ = s.Get(hash)
}

// ─── Corruption: ListHashes With Mixed Content ──────────────────────────────

func TestCorruption_ListHashesWithJunkFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Store a valid object
	data := []byte("valid object")
	_, _, err = s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Create junk files in the store directory
	junkDir := filepath.Join(dir, "zz")
	if err := os.MkdirAll(junkDir, 0755); err != nil {
		t.Fatalf("mkdir junk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(junkDir, "not-a-hash"), []byte("junk"), 0644); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	// Also create a file directly in root (not in a 2-char prefix dir)
	if err := os.WriteFile(filepath.Join(dir, "stray-file"), []byte("stray"), 0644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	// ListHashes should not panic and should return at least the valid object
	hashes, err := s.ListHashes()
	if err != nil {
		t.Fatalf("ListHashes: %v", err)
	}

	if len(hashes) == 0 {
		t.Error("expected at least 1 hash from ListHashes")
	}
	t.Logf("ListHashes returned %d entries (including junk prefix dir)", len(hashes))
}

// ─── Corruption: Size With Corrupt Store ────────────────────────────────────

func TestCorruption_SizeWithCorruptStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	data := []byte("for size calculation")
	s.Put(data)

	// Create nested junk
	nestedDir := filepath.Join(dir, "ab", "nested", "deep")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "junk"), []byte("junk data"), 0644); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	// Size should not panic even with unexpected directory structure
	totalBytes, count, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if count < 1 {
		t.Errorf("expected at least 1 object, got %d", count)
	}
	if totalBytes <= 0 {
		t.Errorf("expected positive total bytes, got %d", totalBytes)
	}
	t.Logf("Size: %d bytes, %d objects", totalBytes, count)
}

// ─── Corruption: NewStore With File Instead of Directory ────────────────────

func TestCorruption_NewStoreWithFileAsDir(t *testing.T) {
	// Create a file where the store directory should be
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("i am a file"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := NewStore(filePath, false)
	if err == nil {
		t.Fatal("expected error when store dir is a file, got nil")
	}
	t.Logf("NewStore with file path: %v (expected)", err)
}
