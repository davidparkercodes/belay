// Package storekey manages the per-user master key that encrypts the Belay content store.
package storekey

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	KeySize = 32

	EnvKeyFile = "BELAY_KEY_FILE"

	KeychainService = "belay"
	KeychainAccount = "store-key"
)

var ErrNotFound = errors.New("no belay store key found")

type Key struct {
	Master []byte
	Source string
}

type Provider interface {
	Keys() ([]Key, error)
	Create() (Key, error)
	Install(master []byte, force bool) (Key, error)
	Describe() string
}

func Generate() ([]byte, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return k, nil
}

func Encode(master []byte) string {
	return base64.StdEncoding.EncodeToString(master)
}

func Decode(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(b) != KeySize {
		return nil, fmt.Errorf("decode key: want %d bytes, got %d", KeySize, len(b))
	}
	return b, nil
}

func DefaultKeyFile() (string, error) {
	if p := os.Getenv(EnvKeyFile); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "belay", "store.key"), nil
}

var (
	defaultMu    sync.Mutex
	defaultCache = map[string]*Cached{}
)

// Default returns the key provider. BELAY_KEY_FILE disables the keychain, and test
// binaries never reach the real keychain or key file. Providers are shared per
// process so repeated store opens load the key once.
func Default() Provider {
	var p Provider
	var cacheKey string
	if testing.Testing() && os.Getenv(EnvKeyFile) == "" {
		path := filepath.Join(os.TempDir(), fmt.Sprintf("belay-test-%d", os.Getpid()), "store.key")
		p, cacheKey = &FileProvider{Path: path}, "file:"+path
	} else {
		path, err := DefaultKeyFile()
		if err != nil {
			path = ""
		}
		file := &FileProvider{Path: path}
		if os.Getenv(EnvKeyFile) != "" {
			p, cacheKey = file, "file:"+path
		} else {
			p, cacheKey = &ChainProvider{Keychain: systemKeychain(), File: file}, "chain:"+path
		}
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if c, ok := defaultCache[cacheKey]; ok {
		return c
	}
	c := NewCached(p)
	defaultCache[cacheKey] = c
	return c
}

// Cached remembers the keys a provider returned so a process asks the keychain once.
// Failures and empty results are not cached; Create and Install reset the cache.
type Cached struct {
	inner  Provider
	mu     sync.Mutex
	keys   []Key
	loaded bool
}

func NewCached(p Provider) *Cached { return &Cached{inner: p} }

func (c *Cached) Describe() string { return c.inner.Describe() }

func (c *Cached) Inner() Provider { return c.inner }

func (c *Cached) Keys() ([]Key, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return append([]Key(nil), c.keys...), nil
	}
	keys, err := c.inner.Keys()
	if err == nil && len(keys) > 0 {
		c.keys = append([]Key(nil), keys...)
		c.loaded = true
	}
	return keys, err
}

func (c *Cached) Create() (Key, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded, c.keys = false, nil
	return c.inner.Create()
}

func (c *Cached) Install(master []byte, force bool) (Key, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded, c.keys = false, nil
	return c.inner.Install(master, force)
}

type FileProvider struct {
	Path string
}

func (f *FileProvider) Describe() string { return "file " + f.Path }

// LockPath is the lock every key creation or install takes, file or keychain.
func (f *FileProvider) LockPath() string { return f.Path + ".lock" }

func (f *FileProvider) Keys() ([]Key, error) {
	if f.Path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read key file %s: %w", f.Path, err)
	}
	master, err := Decode(string(data))
	if err != nil {
		return nil, fmt.Errorf("key file %s: %w", f.Path, err)
	}
	return []Key{{Master: master, Source: "file " + f.Path}}, nil
}

// Create returns the existing key when one is already there, so concurrent first
// uses all end with the same key.
func (f *FileProvider) Create() (Key, error) {
	if f.Path == "" {
		return Key{}, errors.New("no key file location available")
	}
	unlock, err := f.lock()
	if err != nil {
		return Key{}, err
	}
	defer unlock()
	if existing, err := f.Keys(); err != nil {
		return Key{}, err
	} else if len(existing) > 0 {
		return existing[0], nil
	}
	master, err := Generate()
	if err != nil {
		return Key{}, err
	}
	return f.writeNew(master, true)
}

func (f *FileProvider) Install(master []byte, force bool) (Key, error) {
	if f.Path == "" {
		return Key{}, errors.New("no key file location available")
	}
	unlock, err := f.lock()
	if err != nil {
		return Key{}, err
	}
	defer unlock()
	return f.installLocked(master, force)
}

func (f *FileProvider) installLocked(master []byte, force bool) (Key, error) {
	if len(master) != KeySize {
		return Key{}, fmt.Errorf("key must be %d bytes", KeySize)
	}
	existing, err := f.Keys()
	if err != nil && !force {
		return Key{}, err
	}
	if len(existing) > 0 {
		if string(existing[0].Master) == string(master) {
			return existing[0], nil
		}
		if !force {
			return Key{}, fmt.Errorf("a different key already exists at %s (use --force to replace it)", f.Path)
		}
		return f.replace(master)
	}
	if err != nil {
		return f.replace(master)
	}
	return f.writeNew(master, false)
}

func (f *FileProvider) lock() (func(), error) {
	if _, err := mkdirPrivate(filepath.Dir(f.Path)); err != nil {
		return nil, err
	}
	return lockFile(f.LockPath())
}

// mkdirPrivate creates dir with mode 0700 and reports whether this call created it.
// Only a directory this call created is chmodded; an existing one is left as it is.
func mkdirPrivate(dir string) (bool, error) {
	if _, err := os.Stat(dir); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create key dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	return true, nil
}

func (f *FileProvider) tempKeyFile(master []byte) (string, error) {
	dir := filepath.Dir(f.Path)
	if _, err := mkdirPrivate(dir); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".store.key-*")
	if err != nil {
		return "", fmt.Errorf("create key file: %w", err)
	}
	tmpPath := tmp.Name()
	fail := func(what string, err error) (string, error) {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("%s key file: %w", what, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail("chmod", err)
	}
	if _, err := tmp.WriteString(Encode(master) + "\n"); err != nil {
		return fail("write", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("sync", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close key file: %w", err)
	}
	return tmpPath, nil
}

// writeNew links the key into place and never replaces a key file that appeared
// meanwhile. With adopt, a key that won the race is returned instead of an error.
func (f *FileProvider) writeNew(master []byte, adopt bool) (Key, error) {
	tmpPath, err := f.tempKeyFile(master)
	if err != nil {
		return Key{}, err
	}
	defer os.Remove(tmpPath)
	linkErr := os.Link(tmpPath, f.Path)
	if linkErr == nil {
		return Key{Master: master, Source: "file " + f.Path}, nil
	}
	if !os.IsExist(linkErr) {
		return Key{}, fmt.Errorf("install key file: %w", linkErr)
	}
	existing, err := f.Keys()
	if err != nil {
		return Key{}, err
	}
	if len(existing) == 0 {
		return Key{}, fmt.Errorf("install key file: %w", linkErr)
	}
	if string(existing[0].Master) == string(master) || adopt {
		return existing[0], nil
	}
	return Key{}, fmt.Errorf("a different key already exists at %s (use --force to replace it)", f.Path)
}

func (f *FileProvider) replace(master []byte) (Key, error) {
	tmpPath, err := f.tempKeyFile(master)
	if err != nil {
		return Key{}, err
	}
	if err := os.Rename(tmpPath, f.Path); err != nil {
		os.Remove(tmpPath)
		return Key{}, fmt.Errorf("install key file: %w", err)
	}
	return Key{Master: master, Source: "file " + f.Path}, nil
}

// Keychain.Get reports found=false with a nil error only when the keychain answered
// that no key exists. Any other failure is an error.
type Keychain interface {
	Available() bool
	Get() ([]byte, bool, error)
	Set(master []byte) error
	Name() string
}

// ErrKeyUnavailable means the key could not be looked up this time. Callers must not
// create a new key in its place.
var ErrKeyUnavailable = errors.New("store key unavailable")

// ChainProvider prefers the OS keychain and silently falls back to the key file.
type ChainProvider struct {
	Keychain Keychain
	File     *FileProvider
}

func (c *ChainProvider) Describe() string {
	if c.keychainOn() {
		return c.Keychain.Name() + ", fallback " + c.File.Describe()
	}
	return c.File.Describe()
}

func (c *ChainProvider) keychainOn() bool {
	return c.Keychain != nil && c.Keychain.Available()
}

// Keys returns every key it could read. When the keychain lookup failed the error
// wraps ErrKeyUnavailable and any key-file key is still returned.
func (c *ChainProvider) Keys() ([]Key, error) {
	var keys []Key
	var kcErr error
	if c.keychainOn() {
		master, ok, err := c.Keychain.Get()
		switch {
		case err != nil:
			kcErr = fmt.Errorf("%w: %s: %v", ErrKeyUnavailable, c.Keychain.Name(), err)
		case ok:
			keys = append(keys, Key{Master: master, Source: c.Keychain.Name()})
		}
	}
	fileKeys, err := c.File.Keys()
	if err != nil && len(keys) == 0 {
		if kcErr != nil {
			return nil, kcErr
		}
		return nil, err
	}
	for _, fk := range fileKeys {
		if len(keys) > 0 && string(keys[0].Master) == string(fk.Master) {
			continue
		}
		keys = append(keys, fk)
	}
	return keys, kcErr
}

func (c *ChainProvider) Create() (Key, error) {
	return c.install(nil, false)
}

func (c *ChainProvider) Install(master []byte, force bool) (Key, error) {
	if len(master) != KeySize {
		return Key{}, fmt.Errorf("key must be %d bytes", KeySize)
	}
	return c.install(master, force)
}

// install creates a key when master is nil. Everything runs under the key-file lock
// so two processes never each create a different key.
func (c *ChainProvider) install(master []byte, force bool) (Key, error) {
	if c.File.Path == "" && !c.keychainOn() {
		return Key{}, errors.New("no key file location available")
	}
	if c.File.Path != "" {
		unlock, err := c.File.lock()
		if err != nil {
			return Key{}, err
		}
		defer unlock()
	}
	existing, err := c.Keys()
	if err != nil {
		return Key{}, fmt.Errorf("refusing to create or replace the store key: %w", err)
	}
	create := master == nil
	if len(existing) > 0 {
		if create || string(existing[0].Master) == string(master) {
			return existing[0], nil
		}
		if !force {
			return Key{}, fmt.Errorf("a different key already exists in %s (use --force to replace it)", existing[0].Source)
		}
	}
	if create {
		if master, err = Generate(); err != nil {
			return Key{}, err
		}
	}
	if c.keychainOn() {
		if err := c.Keychain.Set(master); err == nil {
			got, ok, gerr := c.Keychain.Get()
			if gerr == nil && ok {
				if string(got) == string(master) || create {
					return Key{Master: got, Source: c.Keychain.Name()}, nil
				}
				return Key{}, fmt.Errorf("a different key appeared in %s while installing", c.Keychain.Name())
			}
		}
	}
	if c.File.Path == "" {
		return Key{}, errors.New("no key file location available")
	}
	if create {
		return c.File.writeNew(master, true)
	}
	return c.File.installLocked(master, force)
}

type MemoryProvider struct {
	mu   sync.Mutex
	keys []Key
}

func NewMemoryProvider(masters ...[]byte) *MemoryProvider {
	m := &MemoryProvider{}
	for _, k := range masters {
		m.keys = append(m.keys, Key{Master: k, Source: "memory"})
	}
	return m
}

func (m *MemoryProvider) Describe() string { return "memory" }

func (m *MemoryProvider) Keys() ([]Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Key(nil), m.keys...), nil
}

func (m *MemoryProvider) Create() (Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.keys) > 0 {
		return m.keys[0], nil
	}
	master, err := Generate()
	if err != nil {
		return Key{}, err
	}
	k := Key{Master: master, Source: "memory"}
	m.keys = []Key{k}
	return k, nil
}

func (m *MemoryProvider) Install(master []byte, force bool) (Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.keys) > 0 && string(m.keys[0].Master) != string(master) && !force {
		return Key{}, errors.New("a different key already exists (use --force to replace it)")
	}
	k := Key{Master: append([]byte(nil), master...), Source: "memory"}
	m.keys = []Key{k}
	return k, nil
}
