package commands

import (
	"strings"
	"testing"
)

func TestClaudeCodeCmd_BasicProperties(t *testing.T) {
	cmd := newClaudeCodeCmd()

	if cmd.Use != "claude-code" {
		t.Errorf("Use = %q, want %q", cmd.Use, "claude-code")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}

	// Should have setup subcommand
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "setup" {
			found = true
			break
		}
	}
	if !found {
		t.Error("claude-code command should have a 'setup' subcommand")
	}
}

func TestClaudeCodeSetupCmd_BasicProperties(t *testing.T) {
	cmd := newClaudeCodeSetupCmd()

	if cmd.Use != "setup" {
		t.Errorf("Use = %q, want %q", cmd.Use, "setup")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestClaudeCodePrompt_Content(t *testing.T) {
	// Prompt should instruct Claude to add a Belay section
	if !strings.Contains(claudeCodePrompt, "Belay Integration") {
		t.Error("prompt should mention Belay Integration section")
	}
	if !strings.Contains(claudeCodePrompt, "belay log") {
		t.Error("prompt should mention belay log")
	}
	if !strings.Contains(claudeCodePrompt, "belay diff") {
		t.Error("prompt should mention belay diff")
	}
	if !strings.Contains(claudeCodePrompt, "belay sessions") {
		t.Error("prompt should mention belay sessions")
	}
	if !strings.Contains(claudeCodePrompt, "belay restore") {
		t.Error("prompt should mention belay restore")
	}
	if !strings.Contains(claudeCodePrompt, ".belay/") {
		t.Error("prompt should mention .belay/ directory")
	}
}
