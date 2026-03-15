package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewRootCmd_BasicProperties(t *testing.T) {
	cmd := NewRootCmd("1.0.0")

	if cmd.Use != "belay" {
		t.Errorf("Use = %q, want %q", cmd.Use, "belay")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
	if cmd.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", cmd.Version, "1.0.0")
	}
	if !cmd.SilenceUsage {
		t.Error("SilenceUsage should be true")
	}
}

func TestNewRootCmd_VersionTemplate(t *testing.T) {
	cmd := NewRootCmd("2.3.4")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "2.3.4") {
		t.Errorf("version output = %q, want to contain %q", output, "2.3.4")
	}
}

func TestNewRootCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := NewRootCmd("1.0.0")

	expectedCommands := []string{
		"init",
		"status",
		"daemon",
		"log",
		"diff",
		"sessions",
		"restore",
		"gc",
		"replay",
		"conflicts",
		"commit",
		"snapshot",
		"record",
		"rebuild-index",
	}

	registered := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		registered[sub.Name()] = true
	}

	for _, name := range expectedCommands {
		if !registered[name] {
			t.Errorf("subcommand %q not registered on root", name)
		}
	}
}

func TestNewRootCmd_HelpOutput(t *testing.T) {
	cmd := NewRootCmd("1.0.0")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "belay") {
		t.Error("help output should contain 'belay'")
	}
	if !strings.Contains(output, "Available Commands") {
		t.Error("help output should contain 'Available Commands'")
	}
}

func TestNewRootCmd_UnknownSubcommand(t *testing.T) {
	cmd := NewRootCmd("1.0.0")

	var buf bytes.Buffer
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"nonexistent-command"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}
