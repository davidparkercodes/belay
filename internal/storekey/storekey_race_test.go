package storekey

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type syncKeychain struct {
	mu     sync.Mutex
	stored []byte
	getErr error
	sets   int
}

func (k *syncKeychain) Name() string    { return "sync keychain" }
func (k *syncKeychain) Available() bool { return true }
func (k *syncKeychain) Get() ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.getErr != nil {
		return nil, false, k.getErr
	}
	if k.stored == nil {
		return nil, false, nil
	}
	return append([]byte(nil), k.stored...), true, nil
}
func (k *syncKeychain) Set(m []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sets++
	k.stored = append([]byte(nil), m...)
	return nil
}

func createConcurrently(t *testing.T, n int, newProvider func() Provider) [][]byte {
	t.Helper()
	keys := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := newProvider()
			<-start
			k, err := p.Create()
			keys[i], errs[i] = k.Master, err
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	return keys
}

func assertAllEqual(t *testing.T, keys [][]byte) {
	t.Helper()
	for i := range keys {
		if !bytes.Equal(keys[i], keys[0]) {
			t.Fatalf("concurrent Create returned different keys (index %d)", i)
		}
	}
}

func TestFileProviderConcurrentCreateConvergesOnOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "store.key")
	keys := createConcurrently(t, 24, func() Provider { return &FileProvider{Path: path} })
	assertAllEqual(t, keys)
	onDisk, err := (&FileProvider{Path: path}).Keys()
	if err != nil || len(onDisk) != 1 || !bytes.Equal(onDisk[0].Master, keys[0]) {
		t.Fatalf("key file does not hold the key every caller got: %v", err)
	}
}

func TestChainConcurrentCreateConvergesOnOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.key")
	kc := &syncKeychain{}
	keys := createConcurrently(t, 24, func() Provider {
		return &ChainProvider{Keychain: kc, File: &FileProvider{Path: path}}
	})
	assertAllEqual(t, keys)
	if !bytes.Equal(kc.stored, keys[0]) {
		t.Fatal("keychain does not hold the key every caller got")
	}
	if kc.sets != 1 {
		t.Fatalf("keychain written %d times, want 1", kc.sets)
	}
}

const helperEnv = "BELAY_STOREKEY_HELPER_PATH"

func TestHelperCreateKey(t *testing.T) {
	path := os.Getenv(helperEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	k, err := (&FileProvider{Path: path}).Create()
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(1)
	}
	fmt.Println("KEY", Encode(k.Master))
}

func TestConcurrentCreateAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.key")
	const n = 8
	outs := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperCreateKey$", "-test.count=1")
			cmd.Env = append(os.Environ(), helperEnv+"="+path)
			out, err := cmd.CombinedOutput()
			outs[i], errs[i] = string(out), err
		}(i)
	}
	wg.Wait()
	var keys [][]byte
	for i := range outs {
		if errs[i] != nil {
			t.Fatalf("helper %d: %v\n%s", i, errs[i], outs[i])
		}
		for _, line := range strings.Split(outs[i], "\n") {
			if strings.HasPrefix(line, "KEY ") {
				k, err := Decode(strings.TrimPrefix(line, "KEY "))
				if err != nil {
					t.Fatal(err)
				}
				keys = append(keys, k)
			}
		}
	}
	if len(keys) != n {
		t.Fatalf("got %d keys from %d processes", len(keys), n)
	}
	assertAllEqual(t, keys)
}

func TestChainRefusesNewKeyWhenKeychainLookupFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.key")
	kc := &syncKeychain{getErr: errors.New("no session bus")}
	c := &ChainProvider{Keychain: kc, File: &FileProvider{Path: path}}
	if _, err := c.Create(); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Create = %v, want ErrKeyUnavailable", err)
	}
	other, _ := Generate()
	if _, err := c.Install(other, false); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Install = %v, want ErrKeyUnavailable", err)
	}
	if _, err := c.Install(other, true); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("forced Install = %v, want ErrKeyUnavailable", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("no key file may be written while the keychain cannot be asked")
	}
	if kc.sets != 0 {
		t.Fatal("keychain must not be written after a failed lookup")
	}
}

func TestChainKeychainErrorStillReturnsFileKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.key")
	fileKey, _ := Generate()
	if _, err := (&FileProvider{Path: path}).Install(fileKey, false); err != nil {
		t.Fatal(err)
	}
	c := &ChainProvider{Keychain: &syncKeychain{getErr: errors.New("locked")}, File: &FileProvider{Path: path}}
	keys, err := c.Keys()
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Keys error = %v, want ErrKeyUnavailable", err)
	}
	if len(keys) != 1 || !bytes.Equal(keys[0].Master, fileKey) {
		t.Fatal("the key file key must still be returned")
	}
	if _, err := c.Create(); err == nil {
		t.Fatal("Create must refuse while the keychain lookup fails")
	}
}

func TestParseSecretToolLookup(t *testing.T) {
	k, _ := Generate()
	cases := []struct {
		name           string
		out, errOut    string
		code           int
		found, wantErr bool
	}{
		{"found", Encode(k) + "\n", "", 0, true, false},
		{"not found", "", "", 1, false, false},
		{"dbus failure", "", "Cannot autolaunch D-Bus without X11 $DISPLAY", 1, false, true},
		{"other exit", "", "", 2, false, true},
		{"empty success", "", "", 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found, err := parseSecretToolLookup(c.out, c.errOut, c.code)
			if (err != nil) != c.wantErr || found != c.found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if found && !bytes.Equal(got, k) {
				t.Fatal("wrong key")
			}
		})
	}
}

func TestFileProviderLeavesExistingDirMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := (&FileProvider{Path: filepath.Join(dir, "store.key")}).Create(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing dir mode changed to %v", info.Mode().Perm())
	}
}
