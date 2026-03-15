package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusCmd_BasicProperties(t *testing.T) {
	cmd := newStatusCmd()

	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestStatusCmd_Flags(t *testing.T) {
	cmd := newStatusCmd()

	tests := []struct {
		name     string
		defValue string
	}{
		{"json", "false"},
		{"sessions", "false"},
		{"storage", "false"},
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

// --- Helper function tests ---

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := humanBytes(tt.input)
			if got != tt.want {
				t.Errorf("humanBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"shorter than limit", "abc", 5, "abc"},
		{"exact length", "abc", 3, "abc"},
		{"longer than limit", "abcdef", 3, "abc"},
		{"empty string", "", 5, ""},
		{"zero limit", "abc", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateStr(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()

	// Create a few files with known sizes
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world!"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	size, err := dirSize(dir)
	if err != nil {
		t.Fatalf("dirSize: %v", err)
	}
	if size != 11 { // "hello" (5) + "world!" (6)
		t.Errorf("dirSize = %d, want %d", size, 11)
	}
}

func TestDirSize_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	size, err := dirSize(dir)
	if err != nil {
		t.Fatalf("dirSize: %v", err)
	}
	if size != 0 {
		t.Errorf("dirSize of empty dir = %d, want 0", size)
	}
}

func TestDirSize_NonexistentDir(t *testing.T) {
	_, err := dirSize("/nonexistent-dir-12345")
	if err == nil {
		t.Error("dirSize should return error for nonexistent directory")
	}
}

func TestDirSize_IgnoresSubdirectories(t *testing.T) {
	dir := t.TempDir()

	// Create a file
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Create a subdirectory with a file (should not be counted)
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	size, err := dirSize(dir)
	if err != nil {
		t.Fatalf("dirSize: %v", err)
	}
	if size != 4 { // only "data" (4)
		t.Errorf("dirSize = %d, want 4 (should ignore subdirectories)", size)
	}
}

func TestFileSize_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	size := fileSize(path)
	if size != 11 {
		t.Errorf("fileSize = %d, want 11", size)
	}
}

func TestFileSize_NonexistentFile(t *testing.T) {
	size := fileSize("/nonexistent-file-12345")
	if size != 0 {
		t.Errorf("fileSize of nonexistent file = %d, want 0", size)
	}
}
