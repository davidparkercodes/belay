package commands

import (
	"testing"
)

func TestGCCmd_BasicProperties(t *testing.T) {
	cmd := newGCCmd()

	if cmd.Use != "gc" {
		t.Errorf("Use = %q, want %q", cmd.Use, "gc")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
}

func TestGCCmd_Flags(t *testing.T) {
	cmd := newGCCmd()

	tests := []struct {
		name     string
		defValue string
	}{
		{"dry-run", "false"},
		{"json", "false"},
		{"gc-only", "false"},
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

// --- formatBytes tests ---

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 bytes"},
		{1, "1 bytes"},
		{512, "512 bytes"},
		{1023, "1023 bytes"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{2048, "2.00 KB"},
		{1048576, "1.00 MB"},
		{1572864, "1.50 MB"},
		{1073741824, "1.00 GB"},
		{2147483648, "2.00 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatBytes(tt.input)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
