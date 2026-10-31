package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/storekey"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := storekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func openEncrypted(t *testing.T, dir string, compress bool, keys storekey.Provider) *Store {
	t.Helper()
	s, err := Open(filepath.Join(dir, "objects"), Options{
		Compress:     compress,
		Encrypt:      true,
		Keys:         keys,
		ManifestPath: filepath.Join(dir, "store.json"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestEncryptedRoundTrip(t *testing.T) {
	for _, compress := range []bool{true, false} {
		dir := t.TempDir()
		s := openEncrypted(t, dir, compress, storekey.NewMemoryProvider(testKey(t)))
		data := []byte("API_KEY=sk-example-value\nline two\n")

		id, size, err := s.Put(data)
		if err != nil {
			t.Fatal(err)
		}
		if size != int64(len(data)) || len(id) != 64 {
			t.Fatalf("size %d id %q", size, id)
		}
		if id == schema.ContentHashForBytes(data) {
			t.Fatal("encrypted store must not use plain sha256 ids")
		}
		if id != s.ContentID(data) {
			t.Fatal("ContentID must match the id Put returns")
		}
		got, err := s.Get(id)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("Get = %q, %v", got, err)
		}
		raw, _ := os.ReadFile(s.objectPath(id))
		if !bytes.HasPrefix(raw, objectMagic) {
			t.Fatal("object missing magic header")
		}
		if bytes.Contains(raw, []byte("sk-example-value")) {
			t.Fatal("plaintext visible on disk")
		}
		if (raw[4]&flagGzip != 0) != compress {
			t.Fatalf("gzip flag = %v, want %v", raw[4]&flagGzip != 0, compress)
		}
		if err := s.Verify(id); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
}

func TestEncryptedEmptyContent(t *testing.T) {
	s := openEncrypted(t, t.TempDir(), true, storekey.NewMemoryProvider(testKey(t)))
	id, _, err := s.Put(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(id)
	if err != nil || len(got) != 0 {
		t.Fatalf("Get empty = %q, %v", got, err)
	}
}

func TestEncryptedDedupe(t *testing.T) {
	s := openEncrypted(t, t.TempDir(), true, storekey.NewMemoryProvider(testKey(t)))
	a, _, _ := s.Put([]byte("same"))
	b, _, _ := s.Put([]byte("same"))
	if a != b {
		t.Fatal("same content must give the same id")
	}
	hashes, _ := s.ListHashes()
	if len(hashes) != 1 {
		t.Fatalf("objects = %d, want 1", len(hashes))
	}
}

func TestKeyedIDsDependOnKey(t *testing.T) {
	s1 := openEncrypted(t, t.TempDir(), true, storekey.NewMemoryProvider(testKey(t)))
	s2 := openEncrypted(t, t.TempDir(), true, storekey.NewMemoryProvider(testKey(t)))
	if s1.ContentID([]byte("x")) == s2.ContentID([]byte("x")) {
		t.Fatal("different keys must give different ids")
	}
}

func TestOpenWithWrongKeyFailsClearly(t *testing.T) {
	dir := t.TempDir()
	s := openEncrypted(t, dir, true, storekey.NewMemoryProvider(testKey(t)))
	if _, _, err := s.Put([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	_, err := Open(filepath.Join(dir, "objects"), Options{Encrypt: true, Keys: storekey.NewMemoryProvider(testKey(t)), ManifestPath: filepath.Join(dir, "store.json")})
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("want ErrWrongKey, got %v", err)
	}
	if !strings.Contains(err.Error(), s.KeyID()) || !strings.Contains(err.Error(), "belay key import") {
		t.Fatalf("error should name the needed key id and the fix: %v", err)
	}
	if strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("wrong key must not be reported as corruption: %v", err)
	}

	_, err = Open(filepath.Join(dir, "objects"), Options{Encrypt: true, Keys: storekey.NewMemoryProvider(), ManifestPath: filepath.Join(dir, "store.json")})
	if !errors.Is(err, ErrWrongKey) || !strings.Contains(err.Error(), "no store key was found") {
		t.Fatalf("missing key error = %v", err)
	}
}

func TestOpenPicksTheKeyMatchingTheManifest(t *testing.T) {
	dir := t.TempDir()
	right := testKey(t)
	s := openEncrypted(t, dir, true, storekey.NewMemoryProvider(right))
	id, _, _ := s.Put([]byte("data"))

	s2 := openEncrypted(t, dir, true, storekey.NewMemoryProvider(testKey(t), right))
	if s2.KeyID() != s.KeyID() {
		t.Fatal("store must pick the candidate key whose id matches the manifest")
	}
	if got, err := s2.Get(id); err != nil || string(got) != "data" {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

func TestGetWithWrongKeyReportsDecryptFailure(t *testing.T) {
	dir := t.TempDir()
	s := openEncrypted(t, dir, true, storekey.NewMemoryProvider(testKey(t)))
	id, _, _ := s.Put([]byte("secret"))
	other, _ := Open(filepath.Join(dir, "objects"), Options{Encrypt: true, Keys: storekey.NewMemoryProvider(testKey(t))})
	_, err := other.Get(id)
	if err == nil || !strings.Contains(err.Error(), "wrong store key") {
		t.Fatalf("expected wrong-key decrypt error, got %v", err)
	}
}

func TestTamperDetection(t *testing.T) {
	s := openEncrypted(t, t.TempDir(), true, storekey.NewMemoryProvider(testKey(t)))
	a, _, _ := s.Put([]byte("content A"))
	b, _, _ := s.Put([]byte("content B"))

	rawA, _ := os.ReadFile(s.objectPath(a))
	flipped := append([]byte(nil), rawA...)
	flipped[len(flipped)-1] ^= 0xff
	if err := os.WriteFile(s.objectPath(a), flipped, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(a); err == nil {
		t.Fatal("bit flip must be detected")
	}

	rawB, _ := os.ReadFile(s.objectPath(b))
	if err := os.WriteFile(s.objectPath(a), rawB, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(a); err == nil {
		t.Fatal("a blob moved under another id must fail (AAD binds the id)")
	}

	flags := append([]byte(nil), rawB...)
	flags[4] ^= flagGzip
	if err := os.WriteFile(s.objectPath(b), flags, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(b); err == nil {
		t.Fatal("flipping the gzip flag must not yield silently wrong content")
	}
}

func TestLegacyObjectsStayReadable(t *testing.T) {
	dir := t.TempDir()
	legacy, _ := NewStore(filepath.Join(dir, "objects"), true)
	oldID, _, _ := legacy.Put([]byte("legacy gz"))
	legacyRaw, _ := NewStore(filepath.Join(dir, "objects"), false)
	rawID, _, _ := legacyRaw.Put([]byte("legacy raw"))

	s := openEncrypted(t, dir, true, storekey.NewMemoryProvider(testKey(t)))
	if !s.NeedsMigration() {
		t.Fatal("an existing legacy store must be marked for migration")
	}
	for id, want := range map[string]string{oldID: "legacy gz", rawID: "legacy raw"} {
		got, err := s.Get(id)
		if err != nil || string(got) != want {
			t.Fatalf("legacy Get(%s) = %q, %v", id[:8], got, err)
		}
		enc, _ := s.IsEncrypted(id)
		if enc {
			t.Fatal("legacy object reported as encrypted")
		}
	}
}

func TestLegacyPlaintextThatLooksLikeMagicIsStillLegacy(t *testing.T) {
	dir := t.TempDir()
	content := append(append([]byte(nil), objectMagic...), bytes.Repeat([]byte{7}, 64)...)
	legacy, _ := NewStore(filepath.Join(dir, "objects"), false)
	id, _, _ := legacy.Put(content)
	s := openEncrypted(t, dir, true, storekey.NewMemoryProvider(testKey(t)))
	got, err := s.Get(id)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("Get = %v", err)
	}
	if enc, _ := s.IsEncrypted(id); enc {
		t.Fatal("must be treated as legacy")
	}
}

func TestNewStoreManifest(t *testing.T) {
	dir := t.TempDir()
	s := openEncrypted(t, dir, true, storekey.NewMemoryProvider(testKey(t)))
	m, ok, err := ReadManifest(filepath.Join(dir, "store.json"))
	if err != nil || !ok {
		t.Fatalf("manifest missing: %v", err)
	}
	if m.Format != FormatEncrypted || m.KeyID != s.KeyID() || len(m.KeyID) != 16 || m.Migration != MigrationComplete {
		t.Fatalf("manifest = %+v", m)
	}
	if s.NeedsMigration() {
		t.Fatal("a fresh store has nothing to migrate")
	}
}

func TestEncryptionOptOut(t *testing.T) {
	dir := t.TempDir()
	keys := storekey.NewMemoryProvider()
	s, err := Open(filepath.Join(dir, "objects"), Options{Compress: true, Keys: keys, ManifestPath: filepath.Join(dir, "store.json")})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("plain")
	id, _, _ := s.Put(data)
	if id != schema.ContentHashForBytes(data) {
		t.Fatal("opted-out store keeps sha256 ids")
	}
	if ks, _ := keys.Keys(); len(ks) != 0 {
		t.Fatal("opting out must not create a key")
	}
	m, _, _ := ReadManifest(filepath.Join(dir, "store.json"))
	if m.Format != FormatLegacy {
		t.Fatalf("format = %d", m.Format)
	}
}

func TestTurningEncryptionOffKeepsEncryptingFormat2Store(t *testing.T) {
	dir := t.TempDir()
	keys := storekey.NewMemoryProvider(testKey(t))
	enc := openEncrypted(t, dir, true, keys)
	encID, _, _ := enc.Put([]byte("encrypted before"))

	off, err := Open(filepath.Join(dir, "objects"), Options{Compress: true, Keys: keys, ManifestPath: filepath.Join(dir, "store.json")})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := off.Get(encID); err != nil || string(got) != "encrypted before" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	data := []byte("written while off")
	id, _, err := off.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if id == schema.ContentHashForBytes(data) || id != enc.ContentID(data) {
		t.Fatal("a store that has encrypted must keep keyed ids with encryption = false")
	}
	if isEnc, err := off.IsEncrypted(id); err != nil || !isEnc {
		t.Fatalf("object written with encryption = false on a format-2 store is not encrypted (%v)", err)
	}
	if m := off.Manifest(); m.Format != FormatEncrypted || m.Migration == MigrationPending {
		t.Fatalf("no legacy write should happen, manifest %+v", m)
	}
}
