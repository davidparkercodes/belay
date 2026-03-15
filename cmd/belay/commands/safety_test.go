package commands

import (
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/config"
)

func TestCheckSafetyGate_WritesDisabled(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Safety.AllowWrites = false

	err := checkSafetyGate(cfg, false, "restore", "--at <time> <file>")
	if err == nil {
		t.Fatal("expected error when writes are disabled")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "SAFETY") {
		t.Error("error should mention SAFETY")
	}
	if !strings.Contains(errMsg, "allow_writes") {
		t.Error("error should mention allow_writes config")
	}
	if !strings.Contains(errMsg, "restore") {
		t.Error("error should contain the command name")
	}
	if !strings.Contains(errMsg, "--execute") {
		t.Error("error should mention --execute flag")
	}
}

func TestCheckSafetyGate_WritesEnabledNoExecute(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Safety.AllowWrites = true

	err := checkSafetyGate(cfg, false, "restore", "--at <time> <file>")
	if err == nil {
		t.Fatal("expected error when --execute not passed")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "DRY RUN") {
		t.Error("error should mention DRY RUN")
	}
	if !strings.Contains(errMsg, "--execute") {
		t.Error("error should mention --execute")
	}
}

func TestCheckSafetyGate_WritesEnabledWithExecute(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Safety.AllowWrites = true

	err := checkSafetyGate(cfg, true, "restore", "--at <time> <file>")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestCheckSafetyGate_DifferentCommands(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Safety.AllowWrites = false

	commands := []struct {
		name     string
		flagHint string
	}{
		{"restore", "--at <time> <file>"},
		{"commit", "-s <session-id>"},
		{"replay", "--output <dir> <session-id>"},
		{"snapshot", "--at <time> --output <dir>"},
	}

	for _, tt := range commands {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSafetyGate(cfg, false, tt.name, tt.flagHint)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Errorf("error should contain command name %q", tt.name)
			}
			if !strings.Contains(err.Error(), tt.flagHint) {
				t.Errorf("error should contain flag hint %q", tt.flagHint)
			}
		})
	}
}

func TestSafetyBlockedMessage_Format(t *testing.T) {
	// Verify the message template is well-formed with two %s placeholders
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Safety.AllowWrites = false

	err := checkSafetyGate(cfg, false, "test-cmd", "--test-flag")
	if err == nil {
		t.Fatal("expected error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "belay test-cmd --execute --test-flag") {
		t.Errorf("message should contain formatted command suggestion, got: %s", msg)
	}
}
