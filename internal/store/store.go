// Package store implements a content-addressable object store using SHA-256 hashing
// with optional gzip compression.
package store

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidparkercodes/belay/internal/schema"
)

// Store is a content-addressable object store that persists file content on disk,
// keyed by SHA-256 hash with a two-character prefix directory structure.
type Store struct {
	dir      string
	compress bool
}

// NewStore creates a new Store rooted at objectsDir with optional gzip compression.
func NewStore(objectsDir string, compressionEnabled bool) (*Store, error) {
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		return nil, fmt.Errorf("create objects dir: %w", err)
	}

	return &Store{
		dir:      objectsDir,
		compress: compressionEnabled,
	}, nil
}

// Put stores the given data and returns its content hash and original size.
func (s *Store) Put(data []byte) (hash string, size int64, err error) {
	hash = schema.ContentHashForBytes(data)
	size = int64(len(data))

	objPath := s.objectPath(hash)

	prefixDir := filepath.Dir(objPath)
	if err := os.MkdirAll(prefixDir, 0755); err != nil {
		return "", 0, fmt.Errorf("create prefix dir: %w", err)
	}

	var content []byte
	if s.compress {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(data); err != nil {
			return "", 0, fmt.Errorf("gzip compress: %w", err)
		}
		if err := gz.Close(); err != nil {
			return "", 0, fmt.Errorf("gzip close: %w", err)
		}
		content = buf.Bytes()
	} else {
		content = make([]byte, len(data))
		copy(content, data)
	}

	tmpFile, err := os.CreateTemp(prefixDir, ".tmp-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, objPath); err != nil {
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("rename to final path: %w", err)
	}

	return hash, size, nil
}

// ObjectSize returns the on-disk size of the object with the given hash.
func (s *Store) ObjectSize(hash string) (int64, error) {
	path := s.objectPath(hash)
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Get retrieves the original content for the given hash, decompressing if needed.
func (s *Store) Get(hash string) ([]byte, error) {
	objPath := s.objectPath(hash)

	raw, err := os.ReadFile(objPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object not found: %s", hash)
		}
		return nil, fmt.Errorf("read object: %w", err)
	}

	var data []byte
	if s.compress {
		gz, gzErr := gzip.NewReader(bytes.NewReader(raw))
		if gzErr != nil {
			// Not gzip-compressed; treat as raw
			data = raw
		} else {
			decompressed, readErr := io.ReadAll(gz)
			gz.Close()
			if readErr != nil {
				// Decompression failed; treat as raw
				data = raw
			} else {
				data = decompressed
			}
		}
	} else {
		data = raw
	}

	actualHash := schema.ContentHashForBytes(data)
	if actualHash != hash {
		if s.compress {
			rawHash := schema.ContentHashForBytes(raw)
			if rawHash == hash {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("object corrupted: expected hash %s, got %s", hash, actualHash)
	}

	return data, nil
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

// Verify checks that the object's content matches its expected hash.
func (s *Store) Verify(hash string) error {
	data, err := s.Get(hash)
	if err != nil {
		return err
	}

	actualHash := schema.ContentHashForBytes(data)
	if actualHash != hash {
		return fmt.Errorf("verification failed: expected %s, got %s", hash, actualHash)
	}
	return nil
}

// PutReader reads all data from r and stores it, returning the content hash and size.
func (s *Store) PutReader(r io.Reader) (hash string, size int64, err error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return "", 0, fmt.Errorf("read content: %w", err)
	}
	return s.Put(buf.Bytes())
}

// Close releases any resources held by the Store.
func (s *Store) Close() {
	// No persistent resources to clean up with gzip (created per-operation)
}

func (s *Store) objectPath(hash string) string {
	if len(hash) < 4 {
		return filepath.Join(s.dir, hash)
	}
	return filepath.Join(s.dir, hash[:2], hash[2:])
}
