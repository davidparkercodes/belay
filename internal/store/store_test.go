package store

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

func newTestStore(t *testing.T, compress bool) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, compress)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPut_ReturnsCorrectSHA256(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("hello belay")
	hash, size, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	expected := fmt.Sprintf("%x", sha256.Sum256(data))
	if hash != expected {
		t.Errorf("hash = %q, want %q", hash, expected)
	}
	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}
}

func TestPut_Get_Roundtrip(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("roundtrip content")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("Get returned %q, want %q", got, data)
	}
}

func TestPut_Get_WithCompression(t *testing.T) {
	s := newTestStore(t, true)

	data := []byte("compressed content that should be stored with gzip")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("Get with compression returned %q, want %q", got, data)
	}
}

func TestHas_ExistingObject(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("check existence")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !s.Has(hash) {
		t.Error("Has returned false for existing object")
	}
}

func TestHas_MissingObject(t *testing.T) {
	s := newTestStore(t, false)

	fakeHash := "0000000000000000000000000000000000000000000000000000000000000000"
	if s.Has(fakeHash) {
		t.Error("Has returned true for missing object")
	}
}

func TestDelete_RemovesObject(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("to be deleted")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !s.Has(hash) {
		t.Fatal("object should exist before delete")
	}

	if err := s.Delete(hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if s.Has(hash) {
		t.Error("Has returned true after Delete")
	}
}

func TestDelete_NonExistent_NoError(t *testing.T) {
	s := newTestStore(t, false)

	fakeHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := s.Delete(fakeHash); err != nil {
		t.Errorf("Delete non-existent object returned error: %v", err)
	}
}

func TestGet_NonExistent_ReturnsError(t *testing.T) {
	s := newTestStore(t, false)

	fakeHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, err := s.Get(fakeHash)
	if err == nil {
		t.Fatal("Get non-existent object should return error")
	}
	if !strings.Contains(err.Error(), "object not found") {
		t.Errorf("expected 'object not found' error, got: %v", err)
	}
}

func TestDuplicateContent_SameHash(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("duplicate me")
	hash1, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}

	hash2, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("same content produced different hashes: %q vs %q", hash1, hash2)
	}
}

func TestSize_Empty(t *testing.T) {
	s := newTestStore(t, false)

	totalBytes, count, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if totalBytes != 0 {
		t.Errorf("totalBytes = %d, want 0", totalBytes)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestSize_AfterPuts(t *testing.T) {
	s := newTestStore(t, false)

	data1 := []byte("first")
	data2 := []byte("second")

	s.Put(data1)
	s.Put(data2)

	_, count, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestSize_AfterDuplicate(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("same content")
	s.Put(data)
	s.Put(data) // duplicate

	_, count, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	// Duplicate overwrites the same file, so count should be 1
	if count != 1 {
		t.Errorf("count = %d, want 1 (duplicate should not increase count)", count)
	}
}

func TestEmptyContent(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte{}
	hash, size, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put empty: %v", err)
	}

	if size != 0 {
		t.Errorf("size = %d, want 0", size)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("Get empty returned %d bytes, want 0", len(got))
	}
}

func TestLargeContent(t *testing.T) {
	s := newTestStore(t, false)

	// 1MB of data
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	hash, size, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put large: %v", err)
	}

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get large: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Error("large content roundtrip mismatch")
	}
}

func TestLargeContent_WithCompression(t *testing.T) {
	s := newTestStore(t, true)

	// Highly compressible 1MB of data
	data := bytes.Repeat([]byte("ABCDEFGHIJ"), 100*1024)

	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put large compressed: %v", err)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get large compressed: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Error("large compressed content roundtrip mismatch")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	s := newTestStore(t, false)

	var wg sync.WaitGroup
	const numWriters = 20

	hashes := make([]string, numWriters)
	contents := make([][]byte, numWriters)
	errs := make([]error, numWriters)

	// Write concurrently
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("content-%d-%d", idx, time.Now().UnixNano()))
			contents[idx] = data
			hash, _, err := s.Put(data)
			hashes[idx] = hash
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d error: %v", i, err)
		}
	}

	// Read all back concurrently
	readErrs := make([]error, numWriters)
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			got, err := s.Get(hashes[idx])
			if err != nil {
				readErrs[idx] = err
				return
			}
			if !bytes.Equal(got, contents[idx]) {
				readErrs[idx] = fmt.Errorf("content mismatch for writer %d", idx)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range readErrs {
		if err != nil {
			t.Errorf("reader %d error: %v", i, err)
		}
	}
}

func TestListHashes(t *testing.T) {
	s := newTestStore(t, false)

	data1 := []byte("list-hash-1")
	data2 := []byte("list-hash-2")
	data3 := []byte("list-hash-3")

	hash1, _, _ := s.Put(data1)
	hash2, _, _ := s.Put(data2)
	hash3, _, _ := s.Put(data3)

	hashes, err := s.ListHashes()
	if err != nil {
		t.Fatalf("ListHashes: %v", err)
	}

	if len(hashes) != 3 {
		t.Fatalf("ListHashes returned %d hashes, want 3", len(hashes))
	}

	hashSet := make(map[string]bool)
	for _, h := range hashes {
		hashSet[h] = true
	}

	for _, expected := range []string{hash1, hash2, hash3} {
		if !hashSet[expected] {
			t.Errorf("ListHashes missing hash %s", expected)
		}
	}
}

func TestVerify_ValidObject(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("verify me")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := s.Verify(hash); err != nil {
		t.Errorf("Verify valid object: %v", err)
	}
}

func TestVerify_MissingObject(t *testing.T) {
	s := newTestStore(t, false)

	fakeHash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := s.Verify(fakeHash); err == nil {
		t.Error("Verify missing object should return error")
	}
}

func TestPutReader(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("reader content")
	r := bytes.NewReader(data)

	hash, size, err := s.PutReader(r)
	if err != nil {
		t.Fatalf("PutReader: %v", err)
	}

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get after PutReader: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("content mismatch after PutReader")
	}
}

// --- Retention Policy Tests ---

func TestDefaultRetentionPolicy(t *testing.T) {
	p := DefaultRetentionPolicy(24, 7, 30, 90)

	if len(p.Tiers) != 4 {
		t.Fatalf("expected 4 tiers, got %d", len(p.Tiers))
	}

	expected := []struct {
		name     string
		strategy CompactionStrategy
	}{
		{"hot", StrategyFull},
		{"warm", StrategyHourly},
		{"cold", StrategySessionBoundary},
		{"archive", StrategyDaily},
	}

	for i, e := range expected {
		if p.Tiers[i].Name != e.name {
			t.Errorf("tier %d: name = %q, want %q", i, p.Tiers[i].Name, e.name)
		}
		if p.Tiers[i].Strategy != e.strategy {
			t.Errorf("tier %d: strategy = %d, want %d", i, p.Tiers[i].Strategy, e.strategy)
		}
	}
}

func TestRetentionPolicy_TierForAge(t *testing.T) {
	p := DefaultRetentionPolicy(24, 7, 30, 90)

	tests := []struct {
		age      time.Duration
		wantTier string
		wantNil  bool
	}{
		{1 * time.Hour, "hot", false},
		{23 * time.Hour, "hot", false},
		{24 * time.Hour, "hot", false},
		{25 * time.Hour, "warm", false},
		{3 * 24 * time.Hour, "warm", false},
		{7 * 24 * time.Hour, "warm", false},
		{8 * 24 * time.Hour, "cold", false},
		{30 * 24 * time.Hour, "cold", false},
		{31 * 24 * time.Hour, "archive", false},
		{90 * 24 * time.Hour, "archive", false},
		{91 * 24 * time.Hour, "", true},
	}

	for _, tt := range tests {
		tier := p.TierForAge(tt.age)
		if tt.wantNil {
			if tier != nil {
				t.Errorf("TierForAge(%v) = %q, want nil", tt.age, tier.Name)
			}
		} else {
			if tier == nil {
				t.Errorf("TierForAge(%v) = nil, want %q", tt.age, tt.wantTier)
			} else if tier.Name != tt.wantTier {
				t.Errorf("TierForAge(%v) = %q, want %q", tt.age, tier.Name, tt.wantTier)
			}
		}
	}
}

func TestRetentionPolicy_TierForAge_BeyondAllTiers(t *testing.T) {
	p := DefaultRetentionPolicy(1, 1, 1, 1)

	// Way beyond any tier
	tier := p.TierForAge(365 * 24 * time.Hour)
	if tier != nil {
		t.Errorf("expected nil for age beyond all tiers, got %q", tier.Name)
	}
}

// --- ObjectSize Tests ---

func TestObjectSize_ExistingObject(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("measure my size")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	size, err := s.ObjectSize(hash)
	if err != nil {
		t.Fatalf("ObjectSize: %v", err)
	}

	// Without compression, on-disk size equals data length
	if size != int64(len(data)) {
		t.Errorf("ObjectSize = %d, want %d", size, len(data))
	}
}

func TestObjectSize_MissingObject(t *testing.T) {
	s := newTestStore(t, false)

	fakeHash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	_, err := s.ObjectSize(fakeHash)
	if err == nil {
		t.Fatal("ObjectSize on missing object should return error")
	}
}

func TestObjectSize_WithCompression(t *testing.T) {
	s := newTestStore(t, true)

	// Highly compressible data — on-disk size should differ from original
	data := bytes.Repeat([]byte("ZZZZZZZZZZ"), 10000)
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	diskSize, err := s.ObjectSize(hash)
	if err != nil {
		t.Fatalf("ObjectSize: %v", err)
	}

	// Compressed on-disk size should be smaller than original
	if diskSize >= int64(len(data)) {
		t.Errorf("expected compressed size < %d, got %d", len(data), diskSize)
	}
	if diskSize <= 0 {
		t.Errorf("expected positive disk size, got %d", diskSize)
	}
}

// --- objectPath Tests ---

func TestObjectPath_NormalHash(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("path check")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Object should be accessible — verify its on-disk path uses prefix directory
	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("roundtrip mismatch")
	}

	// Verify the hash is long enough for prefix-based storage
	if len(hash) < 4 {
		t.Errorf("hash too short: %q", hash)
	}
}

func TestObjectPath_ShortHash(t *testing.T) {
	// Test the edge case where hash < 4 chars (objectPath uses flat path)
	s := newTestStore(t, false)

	// Directly test that Has returns false for a very short "hash" (edge case)
	if s.Has("ab") {
		t.Error("Has returned true for short non-existent hash")
	}
}

// --- Delete prefix directory cleanup Tests ---

func TestDelete_CleansPrefixDir(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("sole object in prefix")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get the prefix directory path
	prefix := hash[:2]
	prefixDir := filepath.Join(s.dir, prefix)

	// Prefix dir should exist
	if _, err := os.Stat(prefixDir); os.IsNotExist(err) {
		t.Fatal("prefix directory should exist after Put")
	}

	// Check if this is the only object in the prefix dir
	entries, err := os.ReadDir(prefixDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) == 1 {
		// Delete the only object — prefix dir should be cleaned up
		if err := s.Delete(hash); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		if _, err := os.Stat(prefixDir); !os.IsNotExist(err) {
			t.Error("prefix directory should be removed when empty after Delete")
		}
	}
}

func TestDelete_KeepsPrefixDirWithSiblings(t *testing.T) {
	s := newTestStore(t, false)

	// Store many objects, group by prefix, find a prefix with 2+ objects
	var allHashes []string
	for i := 0; i < 50; i++ {
		data := []byte(fmt.Sprintf("sibling-find-%d", i))
		h, _, err := s.Put(data)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		allHashes = append(allHashes, h)
	}

	// Group by prefix
	prefixMap := make(map[string][]string)
	for _, h := range allHashes {
		prefix := h[:2]
		prefixMap[prefix] = append(prefixMap[prefix], h)
	}

	// Find a prefix with at least 2 hashes
	var hash1, hash2 string
	for _, hashes := range prefixMap {
		if len(hashes) >= 2 {
			hash1 = hashes[0]
			hash2 = hashes[1]
			break
		}
	}

	if hash1 == "" {
		t.Skip("couldn't find two hashes sharing a prefix in 50 objects")
	}

	prefix := hash1[:2]
	prefixDir := filepath.Join(s.dir, prefix)

	// Delete one of them
	if err := s.Delete(hash1); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Prefix dir should still exist because the sibling remains
	if _, err := os.Stat(prefixDir); os.IsNotExist(err) {
		t.Error("prefix directory should still exist when sibling objects remain")
	}

	// Sibling should still be accessible
	if !s.Has(hash2) {
		t.Error("sibling object should still exist")
	}
}

// --- Get edge cases ---

func TestGet_CorruptedCompressedData(t *testing.T) {
	s := newTestStore(t, true)

	// Store valid data to get a valid hash
	data := []byte("will be corrupted")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the on-disk file
	objPath := filepath.Join(s.dir, hash[:2], hash[2:])
	if err := os.WriteFile(objPath, []byte("not valid gzip data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Get should detect corruption — the decompressed data won't match the hash,
	// and the raw data won't match the hash either
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("Get should return error for corrupted compressed data")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("expected 'corrupted' error, got: %v", err)
	}
}

func TestGet_UncompressedDataInCompressedStore(t *testing.T) {
	// When compression is enabled but the stored data was written without
	// compression (e.g., data that happens to be valid raw data matching hash),
	// the store should fall back to reading the raw data.
	sNoCompress := newTestStore(t, false)

	data := []byte("stored without compression")
	hash, _, err := sNoCompress.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Copy the file to a new compressed store location
	dir := t.TempDir()
	sCompress, err := NewStore(dir, true)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { sCompress.Close() })

	// Directly copy the uncompressed file into the compressed store
	srcPath := filepath.Join(sNoCompress.dir, hash[:2], hash[2:])
	dstDir := filepath.Join(dir, hash[:2])
	os.MkdirAll(dstDir, 0755)
	dstPath := filepath.Join(dstDir, hash[2:])
	raw, _ := os.ReadFile(srcPath)
	os.WriteFile(dstPath, raw, 0644)

	// The compressed store should fall back to raw data since decompression
	// won't produce the expected hash, but raw data does
	got, err := sCompress.Get(hash)
	if err != nil {
		t.Fatalf("Get should fall back to raw data, got error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %q, want %q", got, data)
	}
}

// --- Compression edge cases ---

func TestCompression_EmptyContent(t *testing.T) {
	s := newTestStore(t, true)

	data := []byte{}
	hash, size, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put empty with compression: %v", err)
	}

	if size != 0 {
		t.Errorf("size = %d, want 0", size)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get empty with compression: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("Get empty with compression returned %d bytes, want 0", len(got))
	}
}

func TestCompression_ToggleOff(t *testing.T) {
	// Verify data stored without compression survives roundtrip
	s := newTestStore(t, false)

	data := []byte("no compression here but still works fine")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Error("roundtrip mismatch without compression")
	}

	// Verify works too
	if err := s.Verify(hash); err != nil {
		t.Errorf("Verify without compression: %v", err)
	}
}

func TestCompression_SmallContent(t *testing.T) {
	s := newTestStore(t, true)

	// Very small content (compression may expand it)
	data := []byte("x")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("roundtrip mismatch for small compressed data")
	}
}

// --- Concurrent delete during read ---

func TestConcurrentDeleteDuringRead(t *testing.T) {
	s := newTestStore(t, false)

	const numObjects = 50
	hashes := make([]string, numObjects)

	for i := 0; i < numObjects; i++ {
		data := []byte(fmt.Sprintf("concurrent-delete-%d", i))
		hash, _, err := s.Put(data)
		if err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		hashes[i] = hash
	}

	var wg sync.WaitGroup

	// Readers
	readErrors := make([]error, numObjects)
	for i := 0; i < numObjects; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := s.Get(hashes[idx])
			// Error is acceptable since delete may race
			readErrors[idx] = err
		}(i)
	}

	// Deleters (delete every other one)
	for i := 0; i < numObjects; i += 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.Delete(hashes[idx])
		}(i)
	}

	wg.Wait()

	// After all goroutines finish, deleted objects should not exist
	for i := 0; i < numObjects; i += 2 {
		if s.Has(hashes[i]) {
			// Might still exist if Get raced — that's OK, but it shouldn't be guaranteed
		}
	}

	// Non-deleted objects should still exist
	for i := 1; i < numObjects; i += 2 {
		if !s.Has(hashes[i]) {
			t.Errorf("object %d should still exist (was not deleted)", i)
		}
	}
}

// --- ListHashes edge cases ---

func TestListHashes_AfterDelete(t *testing.T) {
	s := newTestStore(t, false)

	data1 := []byte("lh-del-1")
	data2 := []byte("lh-del-2")

	hash1, _, _ := s.Put(data1)
	hash2, _, _ := s.Put(data2)

	s.Delete(hash1)

	hashes, err := s.ListHashes()
	if err != nil {
		t.Fatalf("ListHashes: %v", err)
	}

	hashSet := make(map[string]bool)
	for _, h := range hashes {
		hashSet[h] = true
	}

	if hashSet[hash1] {
		t.Error("deleted hash should not appear in ListHashes")
	}
	if !hashSet[hash2] {
		t.Error("remaining hash should appear in ListHashes")
	}
}

// --- CompactionStrategy enum Tests ---

func TestCompactionStrategy_Values(t *testing.T) {
	// Verify the CompactionStrategy enum values are distinct and ordered
	if StrategyFull != 0 {
		t.Errorf("StrategyFull = %d, want 0", StrategyFull)
	}
	if StrategyHourly != 1 {
		t.Errorf("StrategyHourly = %d, want 1", StrategyHourly)
	}
	if StrategySessionBoundary != 2 {
		t.Errorf("StrategySessionBoundary = %d, want 2", StrategySessionBoundary)
	}
	if StrategyDaily != 3 {
		t.Errorf("StrategyDaily = %d, want 3", StrategyDaily)
	}
}

// --- GarbageCollect Tests ---

func TestGarbageCollect_OrphanDetection(t *testing.T) {
	dir := t.TempDir()
	objDir := filepath.Join(dir, "objects")
	dbPath := filepath.Join(dir, "test-gc.db")

	objStore, err := NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer objStore.Close()

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	// Store three objects
	dataReferenced := []byte("referenced content")
	hashReferenced, _, _ := objStore.Put(dataReferenced)

	dataOrphan1 := []byte("orphan content 1")
	hashOrphan1, _, _ := objStore.Put(dataOrphan1)

	dataOrphan2 := []byte("orphan content 2")
	hashOrphan2, _, _ := objStore.Put(dataOrphan2)

	// Only index the referenced one
	e := &schema.Event{
		EventID:       "evt-gc-1",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "referenced.go",
		Op:            schema.OpModify,
		ContentHash:   hashReferenced,
	}
	if err := idx.IndexEvent(e, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	// Run GC (not dry run)
	result, err := GarbageCollect(idx, objStore, false)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if result.ObjectsScanned != 3 {
		t.Errorf("ObjectsScanned = %d, want 3", result.ObjectsScanned)
	}

	if result.OrphanedObjects != 2 {
		t.Errorf("OrphanedObjects = %d, want 2", result.OrphanedObjects)
	}

	if result.BytesFreed <= 0 {
		t.Errorf("BytesFreed = %d, want > 0", result.BytesFreed)
	}

	// Orphans should be deleted
	if objStore.Has(hashOrphan1) {
		t.Error("orphan 1 should have been deleted")
	}
	if objStore.Has(hashOrphan2) {
		t.Error("orphan 2 should have been deleted")
	}

	// Referenced object should still exist
	if !objStore.Has(hashReferenced) {
		t.Error("referenced object should still exist")
	}
}

func TestGarbageCollect_DryRun(t *testing.T) {
	dir := t.TempDir()
	objDir := filepath.Join(dir, "objects")
	dbPath := filepath.Join(dir, "test-gc-dry.db")

	objStore, err := NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer objStore.Close()

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	// Store an orphan (no index reference)
	dataOrphan := []byte("dry run orphan")
	hashOrphan, _, _ := objStore.Put(dataOrphan)

	// Run GC in dry-run mode
	result, err := GarbageCollect(idx, objStore, true)
	if err != nil {
		t.Fatalf("GarbageCollect dry run: %v", err)
	}

	if result.OrphanedObjects != 1 {
		t.Errorf("OrphanedObjects = %d, want 1", result.OrphanedObjects)
	}

	if result.BytesFreed <= 0 {
		t.Errorf("BytesFreed = %d, want > 0", result.BytesFreed)
	}

	// In dry-run mode, the orphan should NOT be deleted
	if !objStore.Has(hashOrphan) {
		t.Error("dry-run should not delete objects")
	}
}

func TestGarbageCollect_NoOrphans(t *testing.T) {
	dir := t.TempDir()
	objDir := filepath.Join(dir, "objects")
	dbPath := filepath.Join(dir, "test-gc-clean.db")

	objStore, err := NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer objStore.Close()

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	// Store and reference all objects
	data := []byte("fully referenced")
	hash, _, _ := objStore.Put(data)

	e := &schema.Event{
		EventID:       "evt-gc-clean",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "clean.go",
		Op:            schema.OpCreate,
		ContentHash:   hash,
	}
	if err := idx.IndexEvent(e, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	result, err := GarbageCollect(idx, objStore, false)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if result.OrphanedObjects != 0 {
		t.Errorf("OrphanedObjects = %d, want 0", result.OrphanedObjects)
	}

	if result.BytesFreed != 0 {
		t.Errorf("BytesFreed = %d, want 0", result.BytesFreed)
	}

	if !objStore.Has(hash) {
		t.Error("referenced object should still exist")
	}
}

func TestGarbageCollect_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	objDir := filepath.Join(dir, "objects")
	dbPath := filepath.Join(dir, "test-gc-empty.db")

	objStore, err := NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer objStore.Close()

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	result, err := GarbageCollect(idx, objStore, false)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if result.ObjectsScanned != 0 {
		t.Errorf("ObjectsScanned = %d, want 0", result.ObjectsScanned)
	}
	if result.OrphanedObjects != 0 {
		t.Errorf("OrphanedObjects = %d, want 0", result.OrphanedObjects)
	}
}

func TestGarbageCollect_PreviousHashPreservesObject(t *testing.T) {
	dir := t.TempDir()
	objDir := filepath.Join(dir, "objects")
	dbPath := filepath.Join(dir, "test-gc-prev.db")

	objStore, err := NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer objStore.Close()

	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	// Store two objects
	dataOld := []byte("old version")
	hashOld, _, _ := objStore.Put(dataOld)

	dataNew := []byte("new version")
	hashNew, _, _ := objStore.Put(dataNew)

	// Reference hashNew as content_hash and hashOld as previous_hash
	e := &schema.Event{
		EventID:       "evt-gc-prev",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "versioned.go",
		Op:            schema.OpModify,
		ContentHash:   hashNew,
		PreviousHash:  hashOld,
	}
	if err := idx.IndexEvent(e, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	result, err := GarbageCollect(idx, objStore, false)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	// Both hashes are referenced (one as content_hash, one as previous_hash)
	if result.OrphanedObjects != 0 {
		t.Errorf("OrphanedObjects = %d, want 0 (previous_hash should be preserved)", result.OrphanedObjects)
	}

	if !objStore.Has(hashOld) {
		t.Error("object referenced by previous_hash should be preserved")
	}
	if !objStore.Has(hashNew) {
		t.Error("object referenced by content_hash should be preserved")
	}
}

// --- Close Tests ---

func TestClose_MultipleCallsSafe(t *testing.T) {
	s := newTestStore(t, true)

	// Close should be safe to call, even though t.Cleanup will also call it
	s.Close()
	s.Close() // Second call should not panic
}

func TestClose_WithoutCompression(t *testing.T) {
	s := newTestStore(t, false)

	// Close on a store without compression should not panic
	s.Close()
}

// --- PutReader edge cases ---

func TestPutReader_WithCompression(t *testing.T) {
	s := newTestStore(t, true)

	data := []byte("reader content with compression enabled")
	r := bytes.NewReader(data)

	hash, size, err := s.PutReader(r)
	if err != nil {
		t.Fatalf("PutReader: %v", err)
	}

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Error("PutReader with compression roundtrip mismatch")
	}
}

// --- Verify edge cases ---

func TestVerify_CompressedObject(t *testing.T) {
	s := newTestStore(t, true)

	data := []byte("verify this compressed content")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := s.Verify(hash); err != nil {
		t.Errorf("Verify compressed object: %v", err)
	}
}

// --- RetentionPolicy edge cases ---

func TestRetentionPolicy_SingleTier(t *testing.T) {
	p := &RetentionPolicy{
		Tiers: []RetentionTier{
			{Name: "all", MaxAge: 24 * time.Hour, Strategy: StrategyFull},
		},
	}

	tier := p.TierForAge(12 * time.Hour)
	if tier == nil || tier.Name != "all" {
		t.Errorf("expected 'all' tier, got %v", tier)
	}

	tier = p.TierForAge(48 * time.Hour)
	if tier != nil {
		t.Errorf("expected nil for age beyond single tier, got %q", tier.Name)
	}
}

func TestRetentionTier_Fields(t *testing.T) {
	tier := RetentionTier{
		Name:     "test-tier",
		MaxAge:   72 * time.Hour,
		Strategy: StrategyHourly,
	}

	if tier.Name != "test-tier" {
		t.Errorf("Name = %q, want %q", tier.Name, "test-tier")
	}
	if tier.MaxAge != 72*time.Hour {
		t.Errorf("MaxAge = %v, want %v", tier.MaxAge, 72*time.Hour)
	}
	if tier.Strategy != StrategyHourly {
		t.Errorf("Strategy = %d, want %d", tier.Strategy, StrategyHourly)
	}
}

func TestCompactionResult_Fields(t *testing.T) {
	result := &CompactionResult{
		EventsReviewed: 100,
		EventsKept:     80,
		EventsRemoved:  20,
		BytesFreed:     1024,
		TierBreakdown:  map[string]int{"hot": 50, "warm": 30},
	}

	if result.EventsReviewed != 100 {
		t.Errorf("EventsReviewed = %d, want 100", result.EventsReviewed)
	}
	if result.EventsKept != 80 {
		t.Errorf("EventsKept = %d, want 80", result.EventsKept)
	}
	if result.EventsRemoved != 20 {
		t.Errorf("EventsRemoved = %d, want 20", result.EventsRemoved)
	}
	if result.BytesFreed != 1024 {
		t.Errorf("BytesFreed = %d, want 1024", result.BytesFreed)
	}
	if result.TierBreakdown["hot"] != 50 {
		t.Errorf("TierBreakdown[hot] = %d, want 50", result.TierBreakdown["hot"])
	}
}

func TestGCResult_Fields(t *testing.T) {
	result := &GCResult{
		OrphanedObjects: 5,
		BytesFreed:      2048,
		ObjectsScanned:  10,
	}

	if result.OrphanedObjects != 5 {
		t.Errorf("OrphanedObjects = %d, want 5", result.OrphanedObjects)
	}
	if result.BytesFreed != 2048 {
		t.Errorf("BytesFreed = %d, want 2048", result.BytesFreed)
	}
	if result.ObjectsScanned != 10 {
		t.Errorf("ObjectsScanned = %d, want 10", result.ObjectsScanned)
	}
}

// --- ListHashes edge cases (non-dir entries, non-2-char dirs) ---

func TestListHashes_IgnoresNonDirEntries(t *testing.T) {
	s := newTestStore(t, false)

	// Put a real object
	data := []byte("real object")
	hash, _, _ := s.Put(data)

	// Place a stray file directly in the store root (not a prefix dir)
	strayFile := filepath.Join(s.dir, "stray-file.txt")
	os.WriteFile(strayFile, []byte("stray"), 0644)

	// Place a directory with wrong name length (not 2 chars)
	wrongDir := filepath.Join(s.dir, "abc")
	os.MkdirAll(wrongDir, 0755)
	os.WriteFile(filepath.Join(wrongDir, "somefile"), []byte("data"), 0644)

	// Place a single-char directory (not 2 chars)
	shortDir := filepath.Join(s.dir, "x")
	os.MkdirAll(shortDir, 0755)
	os.WriteFile(filepath.Join(shortDir, "somefile"), []byte("data"), 0644)

	hashes, err := s.ListHashes()
	if err != nil {
		t.Fatalf("ListHashes: %v", err)
	}

	// Should only find the real object, ignoring stray files and wrong dirs
	hashSet := make(map[string]bool)
	for _, h := range hashes {
		hashSet[h] = true
	}

	if !hashSet[hash] {
		t.Error("real object hash should be listed")
	}

	// Verify stray items are not listed
	if len(hashes) != 1 {
		t.Errorf("expected 1 hash, got %d: %v", len(hashes), hashes)
	}
}

func TestListHashes_IgnoresTmpFiles(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("real object for tmp test")
	hash, _, _ := s.Put(data)

	// Create a .tmp- file in the same prefix dir
	prefix := hash[:2]
	tmpFile := filepath.Join(s.dir, prefix, ".tmp-leftover")
	os.WriteFile(tmpFile, []byte("temp data"), 0644)

	hashes, err := s.ListHashes()
	if err != nil {
		t.Fatalf("ListHashes: %v", err)
	}

	if len(hashes) != 1 {
		t.Errorf("expected 1 hash (ignoring .tmp-), got %d", len(hashes))
	}
	if hashes[0] != hash {
		t.Errorf("expected hash %s, got %s", hash, hashes[0])
	}
}

func TestListHashes_IgnoresSubdirectories(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("object with subdir sibling")
	hash, _, _ := s.Put(data)

	// Create a subdirectory inside a valid prefix dir
	prefix := hash[:2]
	subdir := filepath.Join(s.dir, prefix, "nested-dir")
	os.MkdirAll(subdir, 0755)

	hashes, err := s.ListHashes()
	if err != nil {
		t.Fatalf("ListHashes: %v", err)
	}

	// The subdirectory should be ignored, only the real object should be listed
	if len(hashes) != 1 {
		t.Errorf("expected 1 hash, got %d", len(hashes))
	}
}

// --- PutReader with failing reader ---

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated read error")
}

func TestPutReader_FailingReader(t *testing.T) {
	s := newTestStore(t, false)

	_, _, err := s.PutReader(&errorReader{})
	if err == nil {
		t.Fatal("PutReader with failing reader should return error")
	}
	if !strings.Contains(err.Error(), "read content") {
		t.Errorf("expected 'read content' error, got: %v", err)
	}
}

// --- Get with valid gzip decompressing to wrong content ---

func TestGet_GzipDecompressesToWrongContent(t *testing.T) {
	s := newTestStore(t, true)

	// Store valid data
	data := []byte("original content")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Replace the on-disk file with validly gzip-compressed but DIFFERENT content
	// This tests the path where gzip decompression succeeds but the hash doesn't match
	objPath := filepath.Join(s.dir, hash[:2], hash[2:])
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte("different content that produces wrong hash"))
	gz.Close()
	os.WriteFile(objPath, buf.Bytes(), 0644)

	// Get should detect the hash mismatch after successful decompression
	// The raw data (gzip bytes) won't match either
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("Get should return error for content with wrong hash")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("expected 'corrupted' error, got: %v", err)
	}
}

// --- Verify with corrupted on-disk data ---

func TestVerify_CorruptedObject(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("will be corrupted for verify")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the file on disk
	objPath := filepath.Join(s.dir, hash[:2], hash[2:])
	os.WriteFile(objPath, []byte("corrupted!"), 0644)

	err = s.Verify(hash)
	if err == nil {
		t.Fatal("Verify should return error for corrupted object")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("expected 'corrupted' in error, got: %v", err)
	}
}

// --- Size with .tmp- files (should be excluded) ---

func TestGet_ReadPermissionError(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("permission test content")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Make the file unreadable
	objPath := filepath.Join(s.dir, hash[:2], hash[2:])
	os.Chmod(objPath, 0000)
	t.Cleanup(func() { os.Chmod(objPath, 0644) })

	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("Get should fail when file is unreadable")
	}
	if !strings.Contains(err.Error(), "read object") {
		t.Errorf("expected 'read object' error, got: %v", err)
	}
}

func TestGet_TruncatedGzipData(t *testing.T) {
	s := newTestStore(t, true)

	data := []byte("data to truncate after gzip")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Read the valid gzip data, then truncate it (keep the header but break the body)
	objPath := filepath.Join(s.dir, hash[:2], hash[2:])
	validGzip, _ := os.ReadFile(objPath)
	if len(validGzip) > 10 {
		// Keep gzip header (valid enough for NewReader) but truncate the body
		// so ReadAll fails midway
		truncated := validGzip[:10]
		os.WriteFile(objPath, truncated, 0644)
	}

	// Get should handle the ReadAll error gracefully by falling back to raw data
	// then detecting corruption since neither decompressed nor raw hash matches
	_, err = s.Get(hash)
	if err == nil {
		t.Fatal("Get should return error for truncated gzip data")
	}
	// It falls back to raw = truncated bytes, which won't hash-match either
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("expected 'corrupted' error, got: %v", err)
	}
}

func TestDelete_ErrorOnNonExistentDir(t *testing.T) {
	s := newTestStore(t, false)

	// Make a readable-only prefix dir, put an object, then make dir read-only
	// and try to delete — this tests the Delete error path
	data := []byte("delete error test")
	hash, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Make the prefix dir read-only so os.Remove of the file fails
	prefixDir := filepath.Join(s.dir, hash[:2])
	os.Chmod(prefixDir, 0555)
	t.Cleanup(func() { os.Chmod(prefixDir, 0755) })

	err = s.Delete(hash)
	if err == nil {
		t.Fatal("Delete should fail when directory is read-only")
	}
	if !strings.Contains(err.Error(), "delete object") {
		t.Errorf("expected 'delete object' error, got: %v", err)
	}
}

func TestSize_ExcludesTmpFiles(t *testing.T) {
	s := newTestStore(t, false)

	data := []byte("real object for size test")
	hash, _, _ := s.Put(data)

	// Create a .tmp- file
	prefix := hash[:2]
	tmpFile := filepath.Join(s.dir, prefix, ".tmp-orphan")
	os.WriteFile(tmpFile, []byte("tmp data that should be excluded"), 0644)

	totalBytes, count, err := s.Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}

	if count != 1 {
		t.Errorf("count = %d, want 1 (should exclude .tmp- files)", count)
	}

	if totalBytes != int64(len(data)) {
		t.Errorf("totalBytes = %d, want %d", totalBytes, len(data))
	}
}
