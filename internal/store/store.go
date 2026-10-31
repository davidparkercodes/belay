// Package store implements a content-addressable object store. Objects are encrypted
// with XChaCha20-Poly1305 and addressed by keyed HMAC-SHA256 ids; legacy plaintext
// objects addressed by SHA-256 stay readable.
package store

import (
	"bytes"
	"compress/gzip"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/storekey"
)

const (
	FormatLegacy    = 1
	FormatEncrypted = 2

	MigrationPending  = "pending"
	MigrationComplete = "complete"

	flagGzip = 0x01

	hkdfInfoEnc  = "belay store enc v1"
	hkdfInfoID   = "belay store id v1"
	hkdfInfoTomb = "belay store legacy tombstone v1"
	keyIDLabel   = "belay-key-id"
)

var objectMagic = []byte{'B', 'L', 'Y', 0x01}

const headerSize = 4 + 1 + chacha20poly1305.NonceSizeX

// ErrWrongKey is returned when an encrypted store is opened without its key.
var ErrWrongKey = errors.New("wrong store key")

// Manifest is persisted at .belay/store.json.
type Manifest struct {
	Format    int    `json:"format"`
	KeyID     string `json:"key_id,omitempty"`
	SealKey   string `json:"seal_key,omitempty"`
	Migration string `json:"migration,omitempty"`
}

// Options configures Open.
type Options struct {
	Compress     bool
	Encrypt      bool
	Keys         storekey.Provider
	ManifestPath string
}

// Store is a content-addressable object store that persists file content on disk
// with a two-character prefix directory structure.
type Store struct {
	dir      string
	compress bool
	encrypt  bool

	aead      cipher.AEAD
	idKey     []byte
	tombKey   []byte
	keyID     string
	keySource string
	seal      *sealKeys

	manifestPath string
	mu           sync.Mutex
	manifest     Manifest
}

// NewStore creates a legacy (unencrypted, SHA-256 addressed) Store rooted at objectsDir.
func NewStore(objectsDir string, compressionEnabled bool) (*Store, error) {
	return Open(objectsDir, Options{Compress: compressionEnabled})
}

// OpenConfig opens the project store using its config and the default key provider.
func OpenConfig(cfg *config.Config) (*Store, error) {
	return OpenConfigWithKeys(cfg, storekey.Default())
}

// OpenConfigWithKeys opens the project store with an explicit key provider.
func OpenConfigWithKeys(cfg *config.Config, keys storekey.Provider) (*Store, error) {
	return Open(cfg.ObjectsDir(), Options{
		Compress:     cfg.Storage.CompressionEnabled,
		Encrypt:      cfg.Store.Encryption,
		Keys:         keys,
		ManifestPath: cfg.StoreManifestPath(),
	})
}

// Open creates or opens a Store.
func Open(objectsDir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return nil, fmt.Errorf("create objects dir: %w", err)
	}
	s := &Store{
		dir:          objectsDir,
		compress:     opts.Compress,
		encrypt:      opts.Encrypt,
		manifestPath: opts.ManifestPath,
	}

	var manifest Manifest
	haveManifest := false
	if opts.ManifestPath != "" {
		m, ok, err := ReadManifest(opts.ManifestPath)
		if err != nil {
			return nil, err
		}
		manifest, haveManifest = m, ok
	}

	if opts.Encrypt && opts.Keys == nil {
		return nil, errors.New("encrypted store requires a key provider")
	}

	if opts.Keys != nil {
		if err := s.loadKey(opts.Keys, manifest); err != nil {
			return nil, err
		}
	}

	if opts.ManifestPath == "" {
		return s, nil
	}

	updated := manifest
	switch {
	case !haveManifest && opts.Encrypt:
		updated = Manifest{Format: FormatEncrypted, KeyID: s.keyID, Migration: MigrationComplete}
		if s.hasAnyObject() {
			updated.Migration = MigrationPending
		}
	case !haveManifest:
		updated = Manifest{Format: FormatLegacy}
	case manifest.Format < FormatEncrypted && opts.Encrypt:
		updated = Manifest{Format: FormatEncrypted, KeyID: s.keyID, Migration: MigrationPending}
	}
	if updated.Format == FormatEncrypted && s.seal != nil && updated.KeyID == s.keyID {
		updated.SealKey = SealFingerprint(s.seal.public)
	}
	s.manifest = updated
	if !haveManifest || updated != manifest {
		if err := WriteManifest(opts.ManifestPath, updated); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) loadKey(p storekey.Provider, manifest Manifest) error {
	keys, err := p.Keys()

	if manifest.KeyID != "" {
		for _, k := range keys {
			if id := KeyID(k.Master); id == manifest.KeyID {
				return s.useKey(k)
			}
		}
		if err != nil {
			return fmt.Errorf("load store key: %w", err)
		}
		if len(keys) == 0 {
			return fmt.Errorf("%w: this store is encrypted with key %s but no store key was found (looked in %s); install the key with 'belay key import FILE'",
				ErrWrongKey, manifest.KeyID, p.Describe())
		}
		return fmt.Errorf("%w: this store is encrypted with key %s but the available key is %s (%s); install the right key with 'belay key import FILE --force'",
			ErrWrongKey, manifest.KeyID, KeyID(keys[0].Master), keys[0].Source)
	}

	if err != nil {
		if !s.encrypt {
			return nil
		}
		return fmt.Errorf("load store key: %w", err)
	}
	if len(keys) > 0 {
		return s.useKey(keys[0])
	}
	if !s.encrypt {
		return nil
	}
	k, err := p.Create()
	if err != nil {
		return fmt.Errorf("create store key: %w", err)
	}
	return s.useKey(k)
}

func (s *Store) useKey(k storekey.Key) error {
	encKey, idKey, err := deriveKeys(k.Master)
	if err != nil {
		return err
	}
	tombKey, err := hkdf.Key(sha256.New, k.Master, nil, hkdfInfoTomb, 32)
	if err != nil {
		return fmt.Errorf("derive tombstone key: %w", err)
	}
	aead, err := chacha20poly1305.NewX(encKey)
	if err != nil {
		return fmt.Errorf("init cipher: %w", err)
	}
	seal, err := deriveSealKeys(k.Master)
	if err != nil {
		return err
	}
	s.aead = aead
	s.idKey = idKey
	s.tombKey = tombKey
	s.seal = seal
	s.keyID = keyIDFromIDKey(idKey)
	s.keySource = k.Source
	return nil
}

func deriveKeys(master []byte) (encKey, idKey []byte, err error) {
	if len(master) != storekey.KeySize {
		return nil, nil, fmt.Errorf("store key must be %d bytes", storekey.KeySize)
	}
	encKey, err = hkdf.Key(sha256.New, master, nil, hkdfInfoEnc, chacha20poly1305.KeySize)
	if err != nil {
		return nil, nil, fmt.Errorf("derive enc key: %w", err)
	}
	idKey, err = hkdf.Key(sha256.New, master, nil, hkdfInfoID, 32)
	if err != nil {
		return nil, nil, fmt.Errorf("derive id key: %w", err)
	}
	return encKey, idKey, nil
}

func keyIDFromIDKey(idKey []byte) string {
	mac := hmac.New(sha256.New, idKey)
	mac.Write([]byte(keyIDLabel))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// KeyID returns the public identifier of a master key.
func KeyID(master []byte) string {
	_, idKey, err := deriveKeys(master)
	if err != nil {
		return ""
	}
	return keyIDFromIDKey(idKey)
}

// ReadManifest loads a store manifest. ok is false when none exists.
func ReadManifest(path string) (Manifest, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, false, nil
		}
		return Manifest{}, false, fmt.Errorf("read store manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, false, fmt.Errorf("parse store manifest %s: %w", path, err)
	}
	return m, true, nil
}

// WriteManifest atomically writes a store manifest.
func WriteManifest(path string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".store.json-*")
	if err != nil {
		return fmt.Errorf("write store manifest: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write store manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync store manifest: %w", err)
	}
	tmp.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("install store manifest: %w", err)
	}
	return nil
}

// Manifest returns the store manifest as last read or written.
func (s *Store) Manifest() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifest
}

// SetMigration records the migration state in the manifest.
func (s *Store) SetMigration(state string) error {
	if s.manifestPath == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manifest.Migration == state {
		return nil
	}
	m := s.manifest
	m.Migration = state
	if err := WriteManifest(s.manifestPath, m); err != nil {
		return err
	}
	s.manifest = m
	return nil
}

// NeedsMigration reports whether legacy objects should be migrated to the encrypted format.
func (s *Store) NeedsMigration() bool {
	return s.Encrypting() && s.Manifest().Migration == MigrationPending
}

// Encrypting reports whether new objects are written encrypted. A store that has
// ever encrypted keeps encrypting even with [store] encryption = false, because
// the opt-out only applies to stores that never encrypted.
func (s *Store) Encrypting() bool {
	if s.aead == nil {
		return false
	}
	return s.encrypt || s.Manifest().Format == FormatEncrypted
}

// TombstoneID replaces a legacy id whose object no longer exists. It keeps equal ids
// equal without revealing the SHA-256 of old content, and reads like any missing object.
func (s *Store) TombstoneID(legacyID string) string {
	mac := hmac.New(sha256.New, s.tombKey)
	mac.Write([]byte(legacyID))
	return hex.EncodeToString(mac.Sum(nil))
}

// ObjectExists reports whether an object file exists. Unlike Has, a failure to check
// is returned as an error instead of reading as absent.
func (s *Store) ObjectExists(hash string) (bool, error) {
	_, err := os.Stat(s.objectPath(hash))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// FindLegacyObject returns the first object, outside skip, stored in the legacy
// unencrypted format. It reads only object headers.
func (s *Store) FindLegacyObject(skip func(id string) bool) (string, bool, error) {
	hashes, err := s.ListHashes()
	if err != nil {
		return "", false, err
	}
	head := make([]byte, headerSize)
	for _, h := range hashes {
		if !isContentID(h) || (skip != nil && skip(h)) {
			continue
		}
		f, err := os.Open(s.objectPath(h))
		if err != nil {
			continue
		}
		n, _ := io.ReadFull(f, head)
		f.Close()
		if !isEncryptedBlob(head[:n]) {
			return h, true, nil
		}
	}
	return "", false, nil
}

// KeyID returns the id of the loaded key, or "" when no key is loaded.
func (s *Store) KeyID() string { return s.keyID }

// KeySource describes where the loaded key came from.
func (s *Store) KeySource() string { return s.keySource }

// ContentID returns the id this store assigns to data. Callers comparing fresh file
// content with stored ids must use this instead of hashing directly.
func (s *Store) ContentID(data []byte) string {
	if s.Encrypting() {
		return s.keyedID(data)
	}
	return schema.ContentHashForBytes(data)
}

func (s *Store) keyedID(data []byte) string {
	mac := hmac.New(sha256.New, s.idKey)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// Put stores the given data and returns its content id and original size.
func (s *Store) Put(data []byte) (hash string, size int64, err error) {
	size = int64(len(data))
	if s.Encrypting() {
		hash = s.keyedID(data)
		content, err := s.encode(hash, data)
		if err != nil {
			return "", 0, err
		}
		if err := s.writeObject(hash, content); err != nil {
			return "", 0, err
		}
		return hash, size, nil
	}

	hash = schema.ContentHashForBytes(data)
	content, err := s.legacyEncode(data)
	if err != nil {
		return "", 0, err
	}
	if err := s.writeObject(hash, content); err != nil {
		return "", 0, err
	}
	if s.Manifest().Format == FormatEncrypted && s.Manifest().Migration != MigrationPending {
		_ = s.SetMigration(MigrationPending)
	}
	return hash, size, nil
}

func (s *Store) legacyEncode(data []byte) ([]byte, error) {
	if !s.compress {
		return append([]byte(nil), data...), nil
	}
	return gzipBytes(data)
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return nil, fmt.Errorf("gzip compress: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *Store) encode(id string, data []byte) ([]byte, error) {
	payload := data
	var flags byte
	if s.compress {
		gz, err := gzipBytes(data)
		if err != nil {
			return nil, err
		}
		payload = gz
		flags |= flagGzip
	}
	out := make([]byte, headerSize, headerSize+len(payload)+s.aead.Overhead())
	copy(out, objectMagic)
	out[4] = flags
	nonce := out[5:headerSize]
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return s.aead.Seal(out, nonce, payload, []byte(id)), nil
}

func (s *Store) decrypt(id string, raw []byte) ([]byte, error) {
	if len(raw) < headerSize+s.aead.Overhead() {
		return nil, errors.New("encrypted object too short")
	}
	flags := raw[4]
	nonce := raw[5:headerSize]
	payload, err := s.aead.Open(nil, nonce, raw[headerSize:], []byte(id))
	if err != nil {
		return nil, err
	}
	if flags&flagGzip == 0 {
		return payload, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	return data, nil
}

func (s *Store) writeObject(hash string, content []byte) error {
	objPath := s.objectPath(hash)
	prefixDir := filepath.Dir(objPath)
	if err := os.MkdirAll(prefixDir, 0755); err != nil {
		return fmt.Errorf("create prefix dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(prefixDir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, objPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename to final path: %w", err)
	}
	return nil
}

// ObjectSize returns the on-disk size of the object with the given hash.
func (s *Store) ObjectSize(hash string) (int64, error) {
	info, err := os.Stat(s.objectPath(hash))
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ObjectInfo returns the file info of the stored object with the given hash.
func (s *Store) ObjectInfo(hash string) (os.FileInfo, error) {
	return os.Stat(s.objectPath(hash))
}

// Get retrieves the original content for the given id, decrypting and decompressing as needed.
func (s *Store) Get(hash string) ([]byte, error) {
	raw, err := os.ReadFile(s.objectPath(hash))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object not found: %s", hash)
		}
		return nil, fmt.Errorf("read object: %w", err)
	}
	return s.decode(hash, raw)
}

func (s *Store) decode(hash string, raw []byte) ([]byte, error) {
	if !isEncryptedBlob(raw) {
		return legacyDecode(hash, raw)
	}
	if s.aead == nil {
		if data, err := legacyDecode(hash, raw); err == nil {
			return data, nil
		}
		return nil, fmt.Errorf("object %s is encrypted but no store key is loaded", hash)
	}
	data, decErr := s.decrypt(hash, raw)
	if decErr == nil {
		if s.keyedID(data) != hash {
			return nil, fmt.Errorf("object corrupted: %s does not match its keyed id", hash)
		}
		return data, nil
	}
	if data, err := legacyDecode(hash, raw); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("object %s: cannot decrypt (wrong store key, or the object was tampered with or corrupted): %v", hash, decErr)
}

func legacyDecode(hash string, raw []byte) ([]byte, error) {
	if gz, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
		decompressed, readErr := io.ReadAll(gz)
		gz.Close()
		if readErr == nil && schema.ContentHashForBytes(decompressed) == hash {
			return decompressed, nil
		}
	}
	actual := schema.ContentHashForBytes(raw)
	if actual == hash {
		return raw, nil
	}
	return nil, fmt.Errorf("object corrupted: expected hash %s, got %s", hash, actual)
}

func isEncryptedBlob(raw []byte) bool {
	return len(raw) >= headerSize && bytes.Equal(raw[:4], objectMagic)
}

// IsEncrypted reports whether the object stored under hash uses the encrypted format.
func (s *Store) IsEncrypted(hash string) (bool, error) {
	f, err := os.Open(s.objectPath(hash))
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, headerSize)
	n, _ := io.ReadFull(f, head)
	if !isEncryptedBlob(head[:n]) {
		return false, nil
	}
	if s.aead == nil {
		return true, nil
	}
	raw, err := os.ReadFile(s.objectPath(hash))
	if err != nil {
		return false, err
	}
	if _, err := s.decrypt(hash, raw); err == nil {
		return true, nil
	}
	_, legacyErr := legacyDecode(hash, raw)
	return legacyErr != nil, nil
}

// Has reports whether an object with the given hash exists in the store.
func (s *Store) Has(hash string) bool {
	_, err := os.Stat(s.objectPath(hash))
	return err == nil
}

// Delete removes the object with the given hash from the store.
func (s *Store) Delete(hash string) error {
	objPath := s.objectPath(hash)
	if err := os.Remove(objPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("delete object: %w", err)
	}

	prefixDir := filepath.Dir(objPath)
	entries, _ := os.ReadDir(prefixDir)
	if len(entries) == 0 {
		os.Remove(prefixDir)
	}

	return nil
}

// Size returns the total on-disk bytes and object count across the store.
func (s *Store) Size() (totalBytes int64, objectCount int, err error) {
	err = filepath.Walk(s.dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && !strings.HasPrefix(info.Name(), ".tmp-") {
			totalBytes += info.Size()
			objectCount++
		}
		return nil
	})
	return
}

func (s *Store) hasAnyObject() bool {
	prefixDirs, err := os.ReadDir(s.dir)
	if err != nil {
		return false
	}
	for _, p := range prefixDirs {
		if !p.IsDir() || len(p.Name()) != 2 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.dir, p.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".tmp-") {
				return true
			}
		}
	}
	return false
}

// ListHashes returns all object hashes currently stored.
func (s *Store) ListHashes() ([]string, error) {
	var hashes []string

	prefixDirs, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read objects dir: %w", err)
	}

	for _, prefixDir := range prefixDirs {
		if !prefixDir.IsDir() || len(prefixDir.Name()) != 2 {
			continue
		}

		entries, err := os.ReadDir(filepath.Join(s.dir, prefixDir.Name()))
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".tmp-") {
				hashes = append(hashes, prefixDir.Name()+entry.Name())
			}
		}
	}

	return hashes, nil
}

// Verify checks that the object's content matches its expected id.
func (s *Store) Verify(hash string) error {
	_, err := s.Get(hash)
	return err
}

// PutReader reads all data from r and stores it, returning the content id and size.
func (s *Store) PutReader(r io.Reader) (hash string, size int64, err error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return "", 0, fmt.Errorf("read content: %w", err)
	}
	return s.Put(buf.Bytes())
}

// Close releases any resources held by the Store.
func (s *Store) Close() {}

func (s *Store) objectPath(hash string) string {
	if len(hash) < 4 {
		return filepath.Join(s.dir, hash)
	}
	return filepath.Join(s.dir, hash[:2], hash[2:])
}
