package commands

import (
	"testing"
)

func TestDiffCmd_BasicProperties(t *testing.T) {
	cmd := newDiffCmd()

	if cmd.Use != "diff [file]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "diff [file]")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestDiffCmd_Flags(t *testing.T) {
	cmd := newDiffCmd()

	tests := []struct {
		name     string
		defValue string
	}{
		{"session", ""},
		{"at", ""},
		{"from", ""},
		{"to", ""},
		{"stat", "false"},
	}

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

// --- containsLine tests ---

func TestContainsLine(t *testing.T) {
	tests := []struct {
		name   string
		lines  []string
		target string
		want   bool
	}{
		{"found in list", []string{"a", "b", "c"}, "b", true},
		{"not found", []string{"a", "b", "c"}, "d", false},
		{"empty list", []string{}, "a", false},
		{"empty target in non-empty list", []string{"a", "b"}, "", false},
		{"empty target in list with empty", []string{"a", "", "b"}, "", true},
		{"exact match required", []string{"abc", "def"}, "ab", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsLine(tt.lines, tt.target)
			if got != tt.want {
				t.Errorf("containsLine(%v, %q) = %v, want %v",
					tt.lines, tt.target, got, tt.want)
			}
		})
	}
}
