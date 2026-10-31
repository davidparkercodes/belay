package storekey

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	keychainTimeout = 5 * time.Second
	macItemNotFound = 44
)

func systemKeychain() Keychain {
	switch runtime.GOOS {
	case "darwin":
		return &macKeychain{}
	case "linux", "freebsd", "openbsd", "netbsd":
		return &secretServiceKeychain{}
	}
	return nil
}

var runKeychainTool = runTool

func runTool(stdin string, name string, args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", "", -1, fmt.Errorf("%s timed out", name)
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			return "", "", -1, err
		}
	}
	return out.String(), errOut.String(), code, nil
}

type macKeychain struct{}

func (m *macKeychain) Name() string { return "macOS keychain" }

func (m *macKeychain) Available() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

func (m *macKeychain) Get() ([]byte, bool, error) {
	out, _, code, err := runKeychainTool("", "security", "find-generic-password", "-s", KeychainService, "-a", KeychainAccount, "-w")
	if err != nil {
		return nil, false, err
	}
	if code == macItemNotFound {
		return nil, false, nil
	}
	if code != 0 {
		return nil, false, fmt.Errorf("security exited %d", code)
	}
	master, err := Decode(out)
	if err != nil {
		return nil, false, err
	}
	return master, true, nil
}

// Set goes through `security -i` so the key travels on stdin instead of the process argument list.
func (m *macKeychain) Set(master []byte) error {
	script := fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"Belay store key\" -w %s\n", KeychainService, KeychainAccount, Encode(master))
	_, _, code, err := runKeychainTool(script, "security", "-i")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("security exited %d", code)
	}
	return nil
}

type secretServiceKeychain struct{}

func (s *secretServiceKeychain) Name() string { return "Secret Service keyring" }

func (s *secretServiceKeychain) Available() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return false
	}
	_, err := exec.LookPath("secret-tool")
	return err == nil
}

func (s *secretServiceKeychain) Get() ([]byte, bool, error) {
	out, errOut, code, err := runKeychainTool("", "secret-tool", "lookup", "service", KeychainService, "account", KeychainAccount)
	if err != nil {
		return nil, false, err
	}
	return parseSecretToolLookup(out, errOut, code)
}

// secret-tool lookup exits 1 with nothing on stderr when no item matches; any
// stderr output or other exit code is a failure to ask, not an answer.
func parseSecretToolLookup(out, errOut string, code int) ([]byte, bool, error) {
	if code != 0 {
		if code == 1 && strings.TrimSpace(errOut) == "" && strings.TrimSpace(out) == "" {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secret-tool exited %d: %s", code, strings.TrimSpace(errOut))
	}
	if strings.TrimSpace(out) == "" {
		return nil, false, nil
	}
	master, err := Decode(out)
	if err != nil {
		return nil, false, err
	}
	return master, true, nil
}

func (s *secretServiceKeychain) Set(master []byte) error {
	_, _, code, err := runKeychainTool(Encode(master), "secret-tool", "store", "--label=Belay store key", "service", KeychainService, "account", KeychainAccount)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("secret-tool exited %d", code)
	}
	return nil
}
