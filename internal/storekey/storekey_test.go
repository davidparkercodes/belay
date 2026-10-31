package storekey

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeKeychain struct {
	available bool
	getErr    error
	setErr    error
	stored    []byte
	gets      int
}

func (f *fakeKeychain) Name() string    { return "fake keychain" }
func (f *fakeKeychain) Available() bool { return f.available }
func (f *fakeKeychain) Get() ([]byte, bool, error) {
	f.gets++
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	if f.stored == nil {
		return nil, false, nil
	}
	return f.stored, true, nil
}
func (f *fakeKeychain) Set(m []byte) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = append([]byte(nil), m...)
	return nil
}

func TestFileProviderCreateAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "belay", "store.key")
	p := &FileProvider{Path: path}

	keys, err := p.Keys()
	if err != nil || len(keys) != 0 {
		t.Fatalf("Keys on empty = %v, %v", keys, err)
	}
	k, err := p.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(k.Master) != KeySize {
		t.Fatalf("key size %d", len(k.Master))
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("key file mode %v, want 0600", info.Mode().Perm())
		}
		dinfo, _ := os.Stat(filepath.Dir(path))
		if dinfo.Mode().Perm() != 0o700 {
			t.Errorf("key dir mode %v, want 0700", dinfo.Mode().Perm())
		}
	}
	keys, err = p.Keys()
	if err != nil || len(keys) != 1 || !bytes.Equal(keys[0].Master, k.Master) {
		t.Fatalf("reload mismatch: %v %v", keys, err)
	}
}

func TestFileProviderInstallRefusesDifferentKeyWithoutForce(t *testing.T) {
	p := &FileProvider{Path: filepath.Join(t.TempDir(), "store.key")}
	first, _ := p.Create()
	other, _ := Generate()
	if _, err := p.Install(other, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected refusal mentioning --force, got %v", err)
	}
	if _, err := p.Install(first.Master, false); err != nil {
		t.Fatalf("reinstalling the same key must succeed: %v", err)
	}
	if _, err := p.Install(other, true); err != nil {
		t.Fatalf("forced install: %v", err)
	}
	keys, _ := p.Keys()
	if !bytes.Equal(keys[0].Master, other) {
		t.Fatal("forced install did not replace the key")
	}
}

func TestFileProviderRejectsMalformedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.key")
	if err := os.WriteFile(path, []byte("not base64!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&FileProvider{Path: path}).Keys(); err == nil {
		t.Fatal("expected error for malformed key file")
	}
}

func TestChainFallsBackSilentlyWhenKeychainMissing(t *testing.T) {
	for name, kc := range map[string]*fakeKeychain{
		"unavailable": {available: false},
		"set-error":   {available: true, setErr: errors.New("user interaction is not allowed")},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.key")
			c := &ChainProvider{Keychain: kc, File: &FileProvider{Path: path}}
			k, err := c.Create()
			if err != nil {
				t.Fatalf("Create must not fail without a keychain: %v", err)
			}
			if !strings.HasPrefix(k.Source, "file ") {
				t.Fatalf("source = %q, want key file", k.Source)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("key file not written: %v", err)
			}
			keys, err := c.Keys()
			if err != nil || len(keys) != 1 || !bytes.Equal(keys[0].Master, k.Master) {
				t.Fatalf("Keys after fallback = %v, %v", keys, err)
			}
		})
	}
}

func TestChainPrefersKeychain(t *testing.T) {
	kc := &fakeKeychain{available: true}
	path := filepath.Join(t.TempDir(), "store.key")
	c := &ChainProvider{Keychain: kc, File: &FileProvider{Path: path}}
	k, err := c.Create()
	if err != nil {
		t.Fatal(err)
	}
	if k.Source != "fake keychain" {
		t.Fatalf("source = %q", k.Source)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("key file must not be written when the keychain works")
	}
}

func TestChainReturnsBothKeysWhenTheyDiffer(t *testing.T) {
	a, _ := Generate()
	b, _ := Generate()
	path := filepath.Join(t.TempDir(), "store.key")
	if _, err := (&FileProvider{Path: path}).Install(b, false); err != nil {
		t.Fatal(err)
	}
	c := &ChainProvider{Keychain: &fakeKeychain{available: true, stored: a}, File: &FileProvider{Path: path}}
	keys, err := c.Keys()
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys = %d, %v", len(keys), err)
	}
	if !bytes.Equal(keys[0].Master, a) || !bytes.Equal(keys[1].Master, b) {
		t.Fatal("keychain key must come first, file key second")
	}
}

func TestDefaultHonorsKeyFileOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "override.key")
	t.Setenv(EnvKeyFile, path)
	p, ok := Default().(*Cached).Inner().(*FileProvider)
	if !ok || p.Path != path {
		t.Fatalf("Default() = %#v, want FileProvider at %s", Default(), path)
	}
}

func TestDefaultNeverUsesRealLocationsUnderTest(t *testing.T) {
	t.Setenv(EnvKeyFile, "")
	p, ok := Default().(*Cached).Inner().(*FileProvider)
	if !ok {
		t.Fatalf("test binaries must not use the keychain, got %T", Default())
	}
	if !strings.HasPrefix(p.Path, os.TempDir()) {
		t.Fatalf("test key path %s is outside the temp dir", p.Path)
	}
}

func TestEncodeDecode(t *testing.T) {
	k, _ := Generate()
	got, err := Decode(Encode(k) + "\n")
	if err != nil || !bytes.Equal(got, k) {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := Decode(Encode(k[:16])); err == nil {
		t.Fatal("short key must be rejected")
	}
}

type countingKeychain struct {
	master []byte
	gets   int
}

func (k *countingKeychain) Available() bool { return true }
func (k *countingKeychain) Name() string    { return "counting keychain" }
func (k *countingKeychain) Get() ([]byte, bool, error) {
	k.gets++
	if k.master == nil {
		return nil, false, nil
	}
	return k.master, true, nil
}
func (k *countingKeychain) Set(master []byte) error {
	k.master = append([]byte(nil), master...)
	return nil
}

func TestCachedLoadsKeychainOncePerProcess(t *testing.T) {
	master, _ := Generate()
	kc := &countingKeychain{master: master}
	c := NewCached(&ChainProvider{Keychain: kc, File: &FileProvider{Path: filepath.Join(t.TempDir(), "store.key")}})
	for i := 0; i < 3; i++ {
		keys, err := c.Keys()
		if err != nil || len(keys) != 1 || !bytes.Equal(keys[0].Master, master) {
			t.Fatalf("Keys() = %v, %v", keys, err)
		}
	}
	if kc.gets != 1 {
		t.Fatalf("keychain read %d times, want 1", kc.gets)
	}
}

func TestCachedSkipsEmptyAndResetsOnInstall(t *testing.T) {
	kc := &countingKeychain{}
	c := NewCached(&ChainProvider{Keychain: kc, File: &FileProvider{Path: filepath.Join(t.TempDir(), "store.key")}})
	if keys, _ := c.Keys(); len(keys) != 0 {
		t.Fatalf("expected no keys, got %d", len(keys))
	}
	k, err := c.Create()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := c.Keys()
	if err != nil || len(keys) != 1 || !bytes.Equal(keys[0].Master, k.Master) {
		t.Fatalf("after Create, Keys() = %v, %v", keys, err)
	}
	gets := kc.gets
	_, _ = c.Keys()
	if kc.gets != gets {
		t.Fatal("cached key was reloaded")
	}
}

func TestDefaultIsSharedWithinProcess(t *testing.T) {
	t.Setenv(EnvKeyFile, filepath.Join(t.TempDir(), "shared.key"))
	if Default() != Default() {
		t.Fatal("Default() must return the same provider for the same location")
	}
}
