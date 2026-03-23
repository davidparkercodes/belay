package commands

import (
	"strings"
	"testing"
)

func TestAIInstructionsCmd_BasicProperties(t *testing.T) {
	cmd := newAIInstructionsCmd()

	if cmd.Use != "ai-instructions" {
		t.Errorf("Use = %q, want %q", cmd.Use, "ai-instructions")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestAIInstructionsBlock_Content(t *testing.T) {
	if !strings.Contains(aiInstructionsBlock, "Belay Integration") {
		t.Error("instructions should mention Belay Integration")
	}
	if !strings.Contains(aiInstructionsBlock, "belay log") {
		t.Error("instructions should mention belay log")
	}
	if !strings.Contains(aiInstructionsBlock, "belay diff") {
		t.Error("instructions should mention belay diff")
	}
	if !strings.Contains(aiInstructionsBlock, "belay sessions") {
		t.Error("instructions should mention belay sessions")
	}
	if !strings.Contains(aiInstructionsBlock, "belay restore") {
		t.Error("instructions should mention belay restore")
	}
	if !strings.Contains(aiInstructionsBlock, ".belay/") {
		t.Error("instructions should mention .belay/ directory")
	}
}
