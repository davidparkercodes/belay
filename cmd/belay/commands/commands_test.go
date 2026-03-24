package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cmdTestCase defines a reusable test case for command flag defaults.
type cmdTestCase struct {
	name     string
	defValue string
}

// assertFlags is a test helper that checks flag registration and defaults.
func assertFlags(t *testing.T, cmd *cobra.Command, tests []cmdTestCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.name)
			if f == nil {
				t.Fatalf("flag --%s not registered", tt.name)
			}
			if f.DefValue != tt.defValue {
				t.Errorf("--%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
			}
		})
	}
}

// --- Commit command tests ---

func TestCommitCmd_BasicProperties(t *testing.T) {
	cmd := newCommitCmd()

	if cmd.Use != "commit" {
		t.Errorf("Use = %q, want %q", cmd.Use, "commit")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
}

func TestCommitCmd_Flags(t *testing.T) {
	cmd := newCommitCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"session", ""},
		{"message", ""},
		{"dry-run", "false"},
		{"execute", "false"},
		{"no-metadata", "false"},
	})
}

func TestCommitCmd_SessionFlagRequired(t *testing.T) {
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(newCommitCmd())

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"commit"})

	err := root.Execute()
	if err == nil {
		t.Fatal("commit without --session should fail")
	}
	if !strings.Contains(err.Error(), "session") {
		t.Errorf("error should mention 'session', got: %v", err)
	}
}

func TestCommitCmd_ShortFlags(t *testing.T) {
	cmd := newCommitCmd()

	// -s is shorthand for --session
	f := cmd.Flags().ShorthandLookup("s")
	if f == nil {
		t.Fatal("-s shorthand should be registered for --session")
	}
	if f.Name != "session" {
		t.Errorf("-s maps to %q, want %q", f.Name, "session")
	}

	// -m is shorthand for --message
	f = cmd.Flags().ShorthandLookup("m")
	if f == nil {
		t.Fatal("-m shorthand should be registered for --message")
	}
	if f.Name != "message" {
		t.Errorf("-m maps to %q, want %q", f.Name, "message")
	}
}

// --- Conflicts command tests ---

func TestConflictsCmd_BasicProperties(t *testing.T) {
	cmd := newConflictsCmd()

	if cmd.Use != "conflicts" {
		t.Errorf("Use = %q, want %q", cmd.Use, "conflicts")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestConflictsCmd_Flags(t *testing.T) {
	cmd := newConflictsCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"since", "24h"},
		{"file", ""},
		{"json", "false"},
	})
}

// --- Daemon command tests ---

func TestDaemonCmd_BasicProperties(t *testing.T) {
	cmd := newDaemonCmd("1.0.0")

	if cmd.Use != "daemon" {
		t.Errorf("Use = %q, want %q", cmd.Use, "daemon")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestDaemonCmd_Subcommands(t *testing.T) {
	cmd := newDaemonCmd("1.0.0")

	expectedSubs := []string{"start", "stop", "restart", "status"}
	registered := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		registered[sub.Name()] = true
	}

	for _, name := range expectedSubs {
		if !registered[name] {
			t.Errorf("subcommand %q not registered on daemon", name)
		}
	}
}

func TestDaemonStartCmd_ForegroundFlag(t *testing.T) {
	cmd := newDaemonCmd("1.0.0")

	var startCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "start" {
			startCmd = sub
			break
		}
	}
	if startCmd == nil {
		t.Fatal("start subcommand not found")
	}

	f := startCmd.Flags().Lookup("foreground")
	if f == nil {
		t.Fatal("--foreground flag not registered on daemon start")
	}
	if f.DefValue != "false" {
		t.Errorf("--foreground default = %q, want %q", f.DefValue, "false")
	}
}

func TestDaemonCmd_HelpOutput(t *testing.T) {
	cmd := newDaemonCmd("1.0.0")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "start") {
		t.Error("daemon help should mention 'start' subcommand")
	}
	if !strings.Contains(output, "stop") {
		t.Error("daemon help should mention 'stop' subcommand")
	}
}

// --- Record command tests ---

func TestRecordCmd_BasicProperties(t *testing.T) {
	cmd := newRecordCmd()

	if cmd.Use != "record <file-path>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "record <file-path>")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestRecordCmd_Flags(t *testing.T) {
	cmd := newRecordCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"operation", "modify"},
		{"session", ""},
		{"tool", ""},
	})
}

func TestRecordCmd_ShortFlags(t *testing.T) {
	cmd := newRecordCmd()

	// -o is shorthand for --operation
	f := cmd.Flags().ShorthandLookup("o")
	if f == nil {
		t.Fatal("-o shorthand should be registered for --operation")
	}
	if f.Name != "operation" {
		t.Errorf("-o maps to %q, want %q", f.Name, "operation")
	}

	// -s is shorthand for --session
	f = cmd.Flags().ShorthandLookup("s")
	if f == nil {
		t.Fatal("-s shorthand should be registered for --session")
	}
	if f.Name != "session" {
		t.Errorf("-s maps to %q, want %q", f.Name, "session")
	}
}

func TestRecordCmd_RequiresExactlyOneArg(t *testing.T) {
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(newRecordCmd())

	var errBuf bytes.Buffer
	root.SetErr(&errBuf)
	root.SetOut(&bytes.Buffer{})

	// No arguments
	root.SetArgs([]string{"record"})
	if err := root.Execute(); err == nil {
		t.Error("record with no args should fail")
	}

	// Too many arguments
	root2 := &cobra.Command{Use: "belay"}
	root2.AddCommand(newRecordCmd())
	root2.SetErr(&bytes.Buffer{})
	root2.SetOut(&bytes.Buffer{})
	root2.SetArgs([]string{"record", "file1", "file2"})
	if err := root2.Execute(); err == nil {
		t.Error("record with too many args should fail")
	}
}

// --- Rebuild-index command tests ---

func TestRebuildIndexCmd_BasicProperties(t *testing.T) {
	cmd := newRebuildIndexCmd()

	if cmd.Use != "rebuild-index" {
		t.Errorf("Use = %q, want %q", cmd.Use, "rebuild-index")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
}

func TestRebuildIndexCmd_Flags(t *testing.T) {
	cmd := newRebuildIndexCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"no-backup", "false"},
	})
}

// --- Replay command tests ---

func TestReplayCmd_BasicProperties(t *testing.T) {
	cmd := newReplayCmd()

	if cmd.Use != "replay [session-id]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "replay [session-id]")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestReplayCmd_Flags(t *testing.T) {
	cmd := newReplayCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"patch", "false"},
		{"output", ""},
		{"execute", "false"},
		{"stat", "false"},
		{"json", "false"},
	})
}

func TestReplayCmd_RequiresExactlyOneArg(t *testing.T) {
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(newReplayCmd())

	root.SetErr(&bytes.Buffer{})
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"replay"})
	if err := root.Execute(); err == nil {
		t.Error("replay with no args should fail")
	}
}

// --- Restore command tests ---

func TestRestoreCmd_BasicProperties(t *testing.T) {
	cmd := newRestoreCmd()

	if cmd.Use != "restore [file]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restore [file]")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestRestoreCmd_Flags(t *testing.T) {
	cmd := newRestoreCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"session", ""},
		{"event", ""},
		{"roughly-around", ""},
		{"all", "false"},
		{"dry-run", "false"},
		{"execute", "false"},
	})
}

func TestRestoreCmd_RequiresAtLeastOneArg(t *testing.T) {
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(newRestoreCmd())

	root.SetErr(&bytes.Buffer{})
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"restore"})
	if err := root.Execute(); err == nil {
		t.Error("restore with no args should fail")
	}
}

// --- Sessions command tests ---

func TestSessionsCmd_BasicProperties(t *testing.T) {
	cmd := newSessionsCmd()

	if cmd.Use != "sessions" {
		t.Errorf("Use = %q, want %q", cmd.Use, "sessions")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestSessionsCmd_Subcommands(t *testing.T) {
	cmd := newSessionsCmd()

	expectedSubs := []string{"list", "show", "active", "label"}
	registered := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		registered[sub.Name()] = true
	}

	for _, name := range expectedSubs {
		if !registered[name] {
			t.Errorf("subcommand %q not registered on sessions", name)
		}
	}
}

func TestSessionsListCmd_Flags(t *testing.T) {
	cmd := newSessionsListCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"json", "false"},
		{"active", "false"},
		{"with-events", "true"},
		{"all", "false"},
		{"limit", "0"},
	})
}

func TestSessionsShowCmd_RequiresOneArg(t *testing.T) {
	cmd := newSessionsCmd()
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(cmd)

	root.SetErr(&bytes.Buffer{})
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"sessions", "show"})
	if err := root.Execute(); err == nil {
		t.Error("sessions show with no args should fail")
	}
}

func TestSessionsLabelCmd_RequiresTwoArgs(t *testing.T) {
	cmd := newSessionsCmd()
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(cmd)

	root.SetErr(&bytes.Buffer{})
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"sessions", "label", "only-one-arg"})
	if err := root.Execute(); err == nil {
		t.Error("sessions label with one arg should fail")
	}
}

func TestSessionsActiveCmd_Flags(t *testing.T) {
	cmd := newSessionsActiveCmd()

	f := cmd.Flags().Lookup("json")
	if f == nil {
		t.Fatal("--json flag not registered on sessions active")
	}
	if f.DefValue != "false" {
		t.Errorf("--json default = %q, want %q", f.DefValue, "false")
	}
}

// --- Snapshot command tests ---

func TestSnapshotCmd_BasicProperties(t *testing.T) {
	cmd := newSnapshotCmd()

	if cmd.Use != "snapshot" {
		t.Errorf("Use = %q, want %q", cmd.Use, "snapshot")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestSnapshotCmd_Flags(t *testing.T) {
	cmd := newSnapshotCmd()

	assertFlags(t, cmd, []cmdTestCase{
		{"roughly-around", ""},
		{"output", ""},
		{"execute", "false"},
		{"file", ""},
		{"ls", "false"},
		{"json", "false"},
	})
}

func TestSnapshotCmd_AtFlagRequired(t *testing.T) {
	root := &cobra.Command{Use: "belay"}
	root.AddCommand(newSnapshotCmd())

	root.SetErr(&bytes.Buffer{})
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"snapshot"})

	err := root.Execute()
	if err == nil {
		t.Fatal("snapshot without --roughly-around should fail")
	}
	if !strings.Contains(err.Error(), "roughly-around") {
		t.Errorf("error should mention 'roughly-around', got: %v", err)
	}
}

// --- Cross-cutting tests ---

func TestAllCommandsHaveShortDescription(t *testing.T) {
	root := NewRootCmd("test")

	for _, sub := range root.Commands() {
		t.Run(sub.Name(), func(t *testing.T) {
			if sub.Short == "" {
				t.Errorf("command %q has empty Short description", sub.Name())
			}
		})
	}
}

func TestAllCommandsHaveRunEOrSubcommands(t *testing.T) {
	root := NewRootCmd("test")

	// Parent-only commands have subcommands but no RunE of their own.
	// Sessions has RunE on the parent (delegates to runSessionsList).
	pureParent := map[string]bool{
		"daemon":    true,
		"hook":      true,
		"git-hooks": true,
	}

	for _, sub := range root.Commands() {
		t.Run(sub.Name(), func(t *testing.T) {
			if pureParent[sub.Name()] {
				if len(sub.Commands()) == 0 {
					t.Errorf("command %q is parent-only but has no subcommands", sub.Name())
				}
				return
			}
			if sub.RunE == nil && sub.Run == nil {
				t.Errorf("command %q has no RunE or Run function", sub.Name())
			}
		})
	}
}

func TestCommandHelp_ContainsUsage(t *testing.T) {
	root := NewRootCmd("test")

	// Collect command names first to avoid mutation issues
	var names []string
	for _, sub := range root.Commands() {
		names = append(names, sub.Name())
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			// Create a fresh root for each test to avoid state leakage
			r := NewRootCmd("test")

			var buf bytes.Buffer
			r.SetOut(&buf)
			r.SetErr(&bytes.Buffer{})
			r.SetArgs([]string{name, "--help"})

			// Execute through root so Cobra resolves the subcommand correctly
			_ = r.Execute()

			output := buf.String()
			if output == "" {
				t.Errorf("command %q produced empty help output", name)
			}
			if !strings.Contains(output, name) {
				t.Errorf("help output for %q should contain the command name", name)
			}
		})
	}
}
