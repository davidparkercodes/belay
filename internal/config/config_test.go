package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// --- DefaultConfig Tests ---

func TestDefaultConfig_ProjectRoot(t *testing.T) {
	root := "/tmp/my-project"
	cfg := DefaultConfig(root)

	if cfg.ProjectRoot != root {
		t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, root)
	}
}

func TestDefaultConfig_BelayPath(t *testing.T) {
	root := "/tmp/my-project"
	cfg := DefaultConfig(root)

	expected := filepath.Join(root, BelayDir)
	if cfg.BelayPath != expected {
		t.Errorf("BelayPath = %q, want %q", cfg.BelayPath, expected)
	}
}

func TestDefaultConfig_DaemonDefaults(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("Daemon.LogLevel = %q, want %q", cfg.Daemon.LogLevel, "info")
	}
	if cfg.Daemon.LogMaxSizeMB != 10 {
		t.Errorf("Daemon.LogMaxSizeMB = %d, want %d", cfg.Daemon.LogMaxSizeMB, 10)
	}
	if cfg.Daemon.LogMaxFiles != 3 {
		t.Errorf("Daemon.LogMaxFiles = %d, want %d", cfg.Daemon.LogMaxFiles, 3)
	}
}

func TestDefaultConfig_WatcherDefaults(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.Watcher.DebounceMs != DefaultDebounceMs {
		t.Errorf("Watcher.DebounceMs = %d, want %d", cfg.Watcher.DebounceMs, DefaultDebounceMs)
	}
	if cfg.Watcher.ExcludeHidden != false {
		t.Errorf("Watcher.ExcludeHidden = %v, want false", cfg.Watcher.ExcludeHidden)
	}
	if cfg.Watcher.MaxFileSizeMB != DefaultMaxFileSizeMB {
		t.Errorf("Watcher.MaxFileSizeMB = %d, want %d", cfg.Watcher.MaxFileSizeMB, DefaultMaxFileSizeMB)
	}
}

func TestDefaultConfig_StorageDefaults(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.Storage.SegmentMaxBytes != DefaultSegmentMaxBytes {
		t.Errorf("Storage.SegmentMaxBytes = %d, want %d", cfg.Storage.SegmentMaxBytes, DefaultSegmentMaxBytes)
	}
	if cfg.Storage.FsyncMode != "interval" {
		t.Errorf("Storage.FsyncMode = %q, want %q", cfg.Storage.FsyncMode, "interval")
	}
	if cfg.Storage.FsyncIntervalMs != 100 {
		t.Errorf("Storage.FsyncIntervalMs = %d, want %d", cfg.Storage.FsyncIntervalMs, 100)
	}
	if cfg.Storage.CompressionEnabled != true {
		t.Errorf("Storage.CompressionEnabled = %v, want true", cfg.Storage.CompressionEnabled)
	}
}

func TestDefaultConfig_RetentionDefaults(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.Retention.HotHours != DefaultHotHours {
		t.Errorf("Retention.HotHours = %d, want %d", cfg.Retention.HotHours, DefaultHotHours)
	}
	if cfg.Retention.WarmDays != DefaultWarmDays {
		t.Errorf("Retention.WarmDays = %d, want %d", cfg.Retention.WarmDays, DefaultWarmDays)
	}
	if cfg.Retention.ColdDays != DefaultColdDays {
		t.Errorf("Retention.ColdDays = %d, want %d", cfg.Retention.ColdDays, DefaultColdDays)
	}
	if cfg.Retention.ArchiveDays != DefaultArchiveDays {
		t.Errorf("Retention.ArchiveDays = %d, want %d", cfg.Retention.ArchiveDays, DefaultArchiveDays)
	}
	if cfg.Retention.MaxStorageGB != DefaultMaxStorageGB {
		t.Errorf("Retention.MaxStorageGB = %d, want %d", cfg.Retention.MaxStorageGB, DefaultMaxStorageGB)
	}
}

func TestDefaultConfig_APIDefaults(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.API.Port != DefaultAPIPort {
		t.Errorf("API.Port = %d, want %d", cfg.API.Port, DefaultAPIPort)
	}
	if cfg.API.Enabled != true {
		t.Errorf("API.Enabled = %v, want true", cfg.API.Enabled)
	}
}

func TestDefaultConfig_SafetyDefaults(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.Safety.AllowWrites != false {
		t.Errorf("Safety.AllowWrites = %v, want false", cfg.Safety.AllowWrites)
	}
}

// --- Constants Tests ---

func TestConstants(t *testing.T) {
	tests := []struct {
		name  string
		got   interface{}
		want  interface{}
	}{
		{"BelayDir", BelayDir, ".belay"},
		{"ConfigFile", ConfigFile, "config.toml"},
		{"DefaultAPIPort", DefaultAPIPort, 33412},
		{"DefaultDebounceMs", DefaultDebounceMs, 50},
		{"DefaultSegmentMaxBytes", int64(DefaultSegmentMaxBytes), int64(64 * 1024 * 1024)},
		{"DefaultHotHours", DefaultHotHours, 24},
		{"DefaultWarmDays", DefaultWarmDays, 7},
		{"DefaultColdDays", DefaultColdDays, 30},
		{"DefaultArchiveDays", DefaultArchiveDays, 365},
		{"DefaultMaxStorageGB", DefaultMaxStorageGB, 10},
		{"DefaultMaxFileSizeMB", DefaultMaxFileSizeMB, 50},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

// --- Path Helper Tests ---

func TestPathHelpers(t *testing.T) {
	root := "/home/user/project"
	cfg := DefaultConfig(root)
	belayPath := filepath.Join(root, BelayDir)

	tests := []struct {
		name   string
		method func() string
		want   string
	}{
		{"EventsDir", cfg.EventsDir, filepath.Join(belayPath, "events")},
		{"ObjectsDir", cfg.ObjectsDir, filepath.Join(belayPath, "objects")},
		{"StashesDir", cfg.StashesDir, filepath.Join(belayPath, "stashes")},
		{"IndexPath", cfg.IndexPath, filepath.Join(belayPath, "index.db")},
		{"PIDPath", cfg.PIDPath, filepath.Join(belayPath, "daemon.pid")},
		{"LogPath", cfg.LogPath, filepath.Join(belayPath, "daemon.log")},
		{"SocketPath", cfg.SocketPath, filepath.Join(belayPath, "daemon.sock")},
		{"ConfigPath", cfg.ConfigPath, filepath.Join(belayPath, ConfigFile)},
	}

	for _, tt := range tests {
		got := tt.method()
		if got != tt.want {
			t.Errorf("%s() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestPathHelpers_DifferentRoots(t *testing.T) {
	roots := []string{
		"/",
		"/tmp",
		"/home/user/deeply/nested/project",
		"/Users/david/Code/my-project",
	}

	for _, root := range roots {
		cfg := DefaultConfig(root)
		belayPath := filepath.Join(root, BelayDir)

		if cfg.EventsDir() != filepath.Join(belayPath, "events") {
			t.Errorf("root=%q: EventsDir() = %q, want events subdir", root, cfg.EventsDir())
		}
		if cfg.ConfigPath() != filepath.Join(belayPath, ConfigFile) {
			t.Errorf("root=%q: ConfigPath() = %q, want config.toml", root, cfg.ConfigPath())
		}
	}
}

// --- WritesAllowed Tests ---

func TestWritesAllowed_DefaultFalse(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")

	if cfg.WritesAllowed() {
		t.Error("WritesAllowed() should be false by default")
	}
}

func TestWritesAllowed_WhenEnabled(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")
	cfg.Safety.AllowWrites = true

	if !cfg.WritesAllowed() {
		t.Error("WritesAllowed() should be true when AllowWrites is set")
	}
}

// --- ToTOML Tests ---

func TestToTOML_ContainsAllSections(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")
	output := cfg.ToTOML()

	sections := []string{
		"[daemon]",
		"[watcher]",
		"[storage]",
		"[retention]",
		"[api]",
		"[safety]",
	}

	for _, section := range sections {
		if !strings.Contains(output, section) {
			t.Errorf("ToTOML output missing section %q", section)
		}
	}
}

func TestToTOML_ContainsDefaultValues(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")
	output := cfg.ToTOML()

	expectations := []struct {
		name  string
		value string
	}{
		{"log_level", `log_level = "info"`},
		{"log_max_size_mb", "log_max_size_mb = 10"},
		{"log_max_files", "log_max_files = 3"},
		{"debounce_ms", "debounce_ms = 50"},
		{"exclude_hidden", "exclude_hidden = false"},
		{"max_file_size_mb", "max_file_size_mb = 50"},
		{"segment_max_bytes", "segment_max_bytes = 67108864"},
		{"fsync_mode", `fsync_mode = "interval"`},
		{"fsync_interval_ms", "fsync_interval_ms = 100"},
		{"compression_enabled", "compression_enabled = true"},
		{"hot_hours", "hot_hours = 24"},
		{"warm_days", "warm_days = 7"},
		{"cold_days", "cold_days = 30"},
		{"archive_days", "archive_days = 365"},
		{"max_storage_gb", "max_storage_gb = 10"},
		{"port", "port = 33412"},
		{"enabled", "enabled = true"},
		{"allow_writes", "allow_writes = false"},
	}

	for _, exp := range expectations {
		if !strings.Contains(output, exp.value) {
			t.Errorf("ToTOML output missing %q (expected %q)", exp.name, exp.value)
		}
	}
}

func TestToTOML_IsValidTOML(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")
	output := cfg.ToTOML()

	var parsed map[string]interface{}
	if _, err := toml.Decode(output, &parsed); err != nil {
		t.Fatalf("ToTOML produced invalid TOML: %v", err)
	}

	// Verify key sections were parsed
	if _, ok := parsed["daemon"]; !ok {
		t.Error("parsed TOML missing 'daemon' section")
	}
	if _, ok := parsed["api"]; !ok {
		t.Error("parsed TOML missing 'api' section")
	}
	if _, ok := parsed["safety"]; !ok {
		t.Error("parsed TOML missing 'safety' section")
	}
}

func TestToTOML_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create config with non-default values
	original := DefaultConfig(dir)
	original.Daemon.LogLevel = "debug"
	original.Daemon.LogMaxSizeMB = 50
	original.Daemon.LogMaxFiles = 10
	original.Watcher.DebounceMs = 200
	original.Watcher.ExcludeHidden = true
	original.Watcher.MaxFileSizeMB = 100
	original.Storage.SegmentMaxBytes = 128 * 1024 * 1024
	original.Storage.FsyncMode = "every"
	original.Storage.FsyncIntervalMs = 500
	original.Storage.CompressionEnabled = false
	original.Retention.HotHours = 48
	original.Retention.WarmDays = 14
	original.Retention.ColdDays = 60
	original.Retention.ArchiveDays = 730
	original.Retention.MaxStorageGB = 50
	original.API.Port = 9999
	original.API.Enabled = false
	original.Safety.AllowWrites = true

	// Write TOML to file
	tomlContent := original.ToTOML()
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(tomlContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load it back
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify all values roundtripped
	if loaded.Daemon.LogLevel != "debug" {
		t.Errorf("Daemon.LogLevel = %q, want %q", loaded.Daemon.LogLevel, "debug")
	}
	if loaded.Daemon.LogMaxSizeMB != 50 {
		t.Errorf("Daemon.LogMaxSizeMB = %d, want %d", loaded.Daemon.LogMaxSizeMB, 50)
	}
	if loaded.Daemon.LogMaxFiles != 10 {
		t.Errorf("Daemon.LogMaxFiles = %d, want %d", loaded.Daemon.LogMaxFiles, 10)
	}
	if loaded.Watcher.DebounceMs != 200 {
		t.Errorf("Watcher.DebounceMs = %d, want %d", loaded.Watcher.DebounceMs, 200)
	}
	if loaded.Watcher.ExcludeHidden != true {
		t.Errorf("Watcher.ExcludeHidden = %v, want true", loaded.Watcher.ExcludeHidden)
	}
	if loaded.Watcher.MaxFileSizeMB != 100 {
		t.Errorf("Watcher.MaxFileSizeMB = %d, want %d", loaded.Watcher.MaxFileSizeMB, 100)
	}
	if loaded.Storage.SegmentMaxBytes != 128*1024*1024 {
		t.Errorf("Storage.SegmentMaxBytes = %d, want %d", loaded.Storage.SegmentMaxBytes, 128*1024*1024)
	}
	if loaded.Storage.FsyncMode != "every" {
		t.Errorf("Storage.FsyncMode = %q, want %q", loaded.Storage.FsyncMode, "every")
	}
	if loaded.Storage.FsyncIntervalMs != 500 {
		t.Errorf("Storage.FsyncIntervalMs = %d, want %d", loaded.Storage.FsyncIntervalMs, 500)
	}
	if loaded.Storage.CompressionEnabled != false {
		t.Errorf("Storage.CompressionEnabled = %v, want false", loaded.Storage.CompressionEnabled)
	}
	if loaded.Retention.HotHours != 48 {
		t.Errorf("Retention.HotHours = %d, want %d", loaded.Retention.HotHours, 48)
	}
	if loaded.Retention.WarmDays != 14 {
		t.Errorf("Retention.WarmDays = %d, want %d", loaded.Retention.WarmDays, 14)
	}
	if loaded.Retention.ColdDays != 60 {
		t.Errorf("Retention.ColdDays = %d, want %d", loaded.Retention.ColdDays, 60)
	}
	if loaded.Retention.ArchiveDays != 730 {
		t.Errorf("Retention.ArchiveDays = %d, want %d", loaded.Retention.ArchiveDays, 730)
	}
	if loaded.Retention.MaxStorageGB != 50 {
		t.Errorf("Retention.MaxStorageGB = %d, want %d", loaded.Retention.MaxStorageGB, 50)
	}
	if loaded.API.Port != 9999 {
		t.Errorf("API.Port = %d, want %d", loaded.API.Port, 9999)
	}
	if loaded.API.Enabled != false {
		t.Errorf("API.Enabled = %v, want false", loaded.API.Enabled)
	}
	if loaded.Safety.AllowWrites != true {
		t.Errorf("Safety.AllowWrites = %v, want true", loaded.Safety.AllowWrites)
	}
}

func TestToTOML_CustomValues(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")
	cfg.Daemon.LogLevel = "debug"
	cfg.API.Port = 8080
	cfg.Safety.AllowWrites = true

	output := cfg.ToTOML()

	if !strings.Contains(output, `log_level = "debug"`) {
		t.Error("ToTOML should reflect custom log_level")
	}
	if !strings.Contains(output, "port = 8080") {
		t.Error("ToTOML should reflect custom port")
	}
	if !strings.Contains(output, "allow_writes = true") {
		t.Error("ToTOML should reflect allow_writes = true")
	}
}

func TestToTOML_ContainsComments(t *testing.T) {
	cfg := DefaultConfig("/tmp/test")
	output := cfg.ToTOML()

	if !strings.Contains(output, "# Belay Configuration") {
		t.Error("ToTOML should contain header comment")
	}
	if !strings.Contains(output, "# AI-aware local version control") {
		t.Error("ToTOML should contain description comment")
	}
	if !strings.Contains(output, "# IMPORTANT:") {
		t.Error("ToTOML should contain safety warning comment")
	}
}

// --- Load Tests ---

func TestLoad_NoConfigFile_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Should return defaults since no config file exists
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("Daemon.LogLevel = %q, want %q", cfg.Daemon.LogLevel, "info")
	}
	if cfg.API.Port != DefaultAPIPort {
		t.Errorf("API.Port = %d, want %d", cfg.API.Port, DefaultAPIPort)
	}
	if cfg.Safety.AllowWrites != false {
		t.Errorf("Safety.AllowWrites = %v, want false", cfg.Safety.AllowWrites)
	}
}

func TestLoad_NoBelayDir_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()

	// No .belay/ directory at all — Load should still return defaults
	// because the config file simply won't exist
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ProjectRoot != dir {
		t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, dir)
	}
	if cfg.API.Port != DefaultAPIPort {
		t.Errorf("API.Port = %d, want %d", cfg.API.Port, DefaultAPIPort)
	}
}

func TestLoad_WithConfigFile(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	configContent := `
[daemon]
log_level = "debug"
log_max_size_mb = 25
log_max_files = 5

[watcher]
debounce_ms = 100
exclude_hidden = true
max_file_size_mb = 75

[storage]
segment_max_bytes = 33554432
fsync_mode = "every"
fsync_interval_ms = 50
compression_enabled = false

[retention]
hot_hours = 12
warm_days = 3
cold_days = 14
archive_days = 180
max_storage_gb = 5

[api]
port = 8080
enabled = false

[safety]
allow_writes = true
`
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Check that ProjectRoot and BelayPath are set correctly
	if cfg.ProjectRoot != dir {
		t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, dir)
	}
	expectedBelayPath := filepath.Join(dir, BelayDir)
	if cfg.BelayPath != expectedBelayPath {
		t.Errorf("BelayPath = %q, want %q", cfg.BelayPath, expectedBelayPath)
	}

	// Verify loaded values
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("Daemon.LogLevel = %q, want %q", cfg.Daemon.LogLevel, "debug")
	}
	if cfg.Daemon.LogMaxSizeMB != 25 {
		t.Errorf("Daemon.LogMaxSizeMB = %d, want %d", cfg.Daemon.LogMaxSizeMB, 25)
	}
	if cfg.Daemon.LogMaxFiles != 5 {
		t.Errorf("Daemon.LogMaxFiles = %d, want %d", cfg.Daemon.LogMaxFiles, 5)
	}
	if cfg.Watcher.DebounceMs != 100 {
		t.Errorf("Watcher.DebounceMs = %d, want %d", cfg.Watcher.DebounceMs, 100)
	}
	if cfg.Watcher.ExcludeHidden != true {
		t.Errorf("Watcher.ExcludeHidden = %v, want true", cfg.Watcher.ExcludeHidden)
	}
	if cfg.Watcher.MaxFileSizeMB != 75 {
		t.Errorf("Watcher.MaxFileSizeMB = %d, want %d", cfg.Watcher.MaxFileSizeMB, 75)
	}
	if cfg.Storage.SegmentMaxBytes != 33554432 {
		t.Errorf("Storage.SegmentMaxBytes = %d, want %d", cfg.Storage.SegmentMaxBytes, 33554432)
	}
	if cfg.Storage.FsyncMode != "every" {
		t.Errorf("Storage.FsyncMode = %q, want %q", cfg.Storage.FsyncMode, "every")
	}
	if cfg.Storage.FsyncIntervalMs != 50 {
		t.Errorf("Storage.FsyncIntervalMs = %d, want %d", cfg.Storage.FsyncIntervalMs, 50)
	}
	if cfg.Storage.CompressionEnabled != false {
		t.Errorf("Storage.CompressionEnabled = %v, want false", cfg.Storage.CompressionEnabled)
	}
	if cfg.Retention.HotHours != 12 {
		t.Errorf("Retention.HotHours = %d, want %d", cfg.Retention.HotHours, 12)
	}
	if cfg.Retention.WarmDays != 3 {
		t.Errorf("Retention.WarmDays = %d, want %d", cfg.Retention.WarmDays, 3)
	}
	if cfg.Retention.ColdDays != 14 {
		t.Errorf("Retention.ColdDays = %d, want %d", cfg.Retention.ColdDays, 14)
	}
	if cfg.Retention.ArchiveDays != 180 {
		t.Errorf("Retention.ArchiveDays = %d, want %d", cfg.Retention.ArchiveDays, 180)
	}
	if cfg.Retention.MaxStorageGB != 5 {
		t.Errorf("Retention.MaxStorageGB = %d, want %d", cfg.Retention.MaxStorageGB, 5)
	}
	if cfg.API.Port != 8080 {
		t.Errorf("API.Port = %d, want %d", cfg.API.Port, 8080)
	}
	if cfg.API.Enabled != false {
		t.Errorf("API.Enabled = %v, want false", cfg.API.Enabled)
	}
	if cfg.Safety.AllowWrites != true {
		t.Errorf("Safety.AllowWrites = %v, want true", cfg.Safety.AllowWrites)
	}
}

func TestLoad_PartialConfig_MergesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Only override a few fields
	configContent := `
[daemon]
log_level = "warn"

[api]
port = 12345
`
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Overridden values
	if cfg.Daemon.LogLevel != "warn" {
		t.Errorf("Daemon.LogLevel = %q, want %q", cfg.Daemon.LogLevel, "warn")
	}
	if cfg.API.Port != 12345 {
		t.Errorf("API.Port = %d, want %d", cfg.API.Port, 12345)
	}

	// Non-overridden values should keep defaults
	if cfg.Daemon.LogMaxSizeMB != 10 {
		t.Errorf("Daemon.LogMaxSizeMB = %d, want default %d", cfg.Daemon.LogMaxSizeMB, 10)
	}
	if cfg.Watcher.DebounceMs != DefaultDebounceMs {
		t.Errorf("Watcher.DebounceMs = %d, want default %d", cfg.Watcher.DebounceMs, DefaultDebounceMs)
	}
	if cfg.Storage.CompressionEnabled != true {
		t.Errorf("Storage.CompressionEnabled = %v, want default true", cfg.Storage.CompressionEnabled)
	}
	if cfg.Safety.AllowWrites != false {
		t.Errorf("Safety.AllowWrites = %v, want default false", cfg.Safety.AllowWrites)
	}
}

func TestLoad_CorruptedTOML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write invalid TOML
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(`[invalid\ngarbage = = =`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should return error for corrupted TOML")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error should mention 'parse config', got: %v", err)
	}
}

func TestLoad_ConfigFileIsDirectory_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	// Create config.toml as a directory instead of a file
	configDirPath := filepath.Join(belayDir, ConfigFile)
	if err := os.MkdirAll(configDirPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should return error when config.toml is a directory")
	}
}

func TestLoad_SetsProjectRootAndBelayPath(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write a config that might try to override ProjectRoot/BelayPath
	// (they have `toml:"-"` so they should not be affected)
	configContent := `
[api]
port = 5555
`
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// ProjectRoot and BelayPath must be set by Load, not from TOML
	if cfg.ProjectRoot != dir {
		t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, dir)
	}
	if cfg.BelayPath != belayDir {
		t.Errorf("BelayPath = %q, want %q", cfg.BelayPath, belayDir)
	}
}

// --- FindProjectRoot Tests ---

func TestFindProjectRoot_InBelayProject(t *testing.T) {
	dir := t.TempDir()
	// Resolve symlinks (macOS /var -> /private/var) so comparison with os.Getwd() works
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Change to the project root
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot: %v", err)
	}

	if root != dir {
		t.Errorf("FindProjectRoot() = %q, want %q", root, dir)
	}
}

func TestFindProjectRoot_FromNestedSubdirectory(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create a deeply nested subdirectory
	nested := filepath.Join(dir, "src", "pkg", "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot: %v", err)
	}

	if root != dir {
		t.Errorf("FindProjectRoot() = %q, want %q", root, dir)
	}
}

func TestFindProjectRoot_NoBelay_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	_, err = FindProjectRoot()
	if err == nil {
		t.Fatal("FindProjectRoot should return error when no .belay/ found")
	}
	if !strings.Contains(err.Error(), "not a belay project") {
		t.Errorf("error should mention 'not a belay project', got: %v", err)
	}
}

func TestFindProjectRoot_BelayIsFile_NotDir(t *testing.T) {
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	// Create .belay as a file, not a directory
	belayFilePath := filepath.Join(dir, BelayDir)
	if err := os.WriteFile(belayFilePath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	_, err = FindProjectRoot()
	if err == nil {
		t.Fatal("FindProjectRoot should return error when .belay is a file, not a directory")
	}
}

func TestFindProjectRoot_FindsNearestBelay(t *testing.T) {
	// Create nested belay projects: parent has .belay/, child also has .belay/
	parent := t.TempDir()
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(parent, BelayDir), 0o755); err != nil {
		t.Fatalf("MkdirAll parent belay: %v", err)
	}

	child := filepath.Join(parent, "subproject")
	if err := os.MkdirAll(filepath.Join(child, BelayDir), 0o755); err != nil {
		t.Fatalf("MkdirAll child belay: %v", err)
	}

	deepNested := filepath.Join(child, "src", "lib")
	if err := os.MkdirAll(deepNested, 0o755); err != nil {
		t.Fatalf("MkdirAll deep nested: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(deepNested); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot: %v", err)
	}

	// Should find the child's .belay/ (nearest), not the parent's
	if root != child {
		t.Errorf("FindProjectRoot() = %q, want nearest root %q", root, child)
	}
}

func TestFindProjectRoot_SymlinkedBelay(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	// Create a real .belay directory elsewhere
	realBelay := filepath.Join(dir, "real-belay-data")
	if err := os.MkdirAll(realBelay, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create project directory with symlinked .belay
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	symlinkPath := filepath.Join(project, BelayDir)
	if err := os.Symlink(realBelay, symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(project); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot: %v", err)
	}

	if root != project {
		t.Errorf("FindProjectRoot() = %q, want %q", root, project)
	}
}

// --- Load + ToTOML Integration Tests ---

func TestLoad_ToTOML_DefaultRoundtrip(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write defaults to file
	defaults := DefaultConfig(dir)
	tomlContent := defaults.ToTOML()
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(tomlContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load and verify it matches defaults
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Daemon.LogLevel != defaults.Daemon.LogLevel {
		t.Errorf("LogLevel roundtrip: got %q, want %q", loaded.Daemon.LogLevel, defaults.Daemon.LogLevel)
	}
	if loaded.API.Port != defaults.API.Port {
		t.Errorf("Port roundtrip: got %d, want %d", loaded.API.Port, defaults.API.Port)
	}
	if loaded.Storage.SegmentMaxBytes != defaults.Storage.SegmentMaxBytes {
		t.Errorf("SegmentMaxBytes roundtrip: got %d, want %d", loaded.Storage.SegmentMaxBytes, defaults.Storage.SegmentMaxBytes)
	}
	if loaded.Retention.HotHours != defaults.Retention.HotHours {
		t.Errorf("HotHours roundtrip: got %d, want %d", loaded.Retention.HotHours, defaults.Retention.HotHours)
	}
	if loaded.Safety.AllowWrites != defaults.Safety.AllowWrites {
		t.Errorf("AllowWrites roundtrip: got %v, want %v", loaded.Safety.AllowWrites, defaults.Safety.AllowWrites)
	}
}

func TestLoad_EmptyConfigFile_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write an empty config file (valid TOML, just no overrides)
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// All values should be defaults since the file is empty
	if cfg.API.Port != DefaultAPIPort {
		t.Errorf("API.Port = %d, want default %d", cfg.API.Port, DefaultAPIPort)
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("Daemon.LogLevel = %q, want default %q", cfg.Daemon.LogLevel, "info")
	}
}

// --- Edge Cases ---

func TestDefaultConfig_EmptyRoot(t *testing.T) {
	cfg := DefaultConfig("")

	if cfg.ProjectRoot != "" {
		t.Errorf("ProjectRoot = %q, want empty string", cfg.ProjectRoot)
	}
	if cfg.BelayPath != BelayDir {
		t.Errorf("BelayPath = %q, want %q", cfg.BelayPath, BelayDir)
	}
}

func TestPathHelpers_WithEmptyBelayPath(t *testing.T) {
	cfg := &Config{BelayPath: ""}

	// All path helpers should still work, just returning relative paths
	if cfg.EventsDir() != "events" {
		t.Errorf("EventsDir() with empty BelayPath = %q, want %q", cfg.EventsDir(), "events")
	}
	if cfg.IndexPath() != "index.db" {
		t.Errorf("IndexPath() with empty BelayPath = %q, want %q", cfg.IndexPath(), "index.db")
	}
}

func TestConfig_StructFieldTags(t *testing.T) {
	// Verify ProjectRoot and BelayPath have toml:"-" by testing that
	// TOML decode doesn't populate them from a config file with those fields
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Try to set ProjectRoot and BelayPath in TOML (they should be ignored)
	configContent := `
ProjectRoot = "/hacked/root"
BelayPath = "/hacked/belay"

[api]
port = 7777
`
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// ProjectRoot and BelayPath should be set by Load, not from TOML
	if cfg.ProjectRoot != dir {
		t.Errorf("ProjectRoot = %q, should not be overridden by TOML", cfg.ProjectRoot)
	}
	if cfg.BelayPath != belayDir {
		t.Errorf("BelayPath = %q, should not be overridden by TOML", cfg.BelayPath)
	}
	// But regular TOML fields should work
	if cfg.API.Port != 7777 {
		t.Errorf("API.Port = %d, want %d", cfg.API.Port, 7777)
	}
}

func TestLoad_PermissionDenied(t *testing.T) {
	// Skip if running as root (permissions don't apply)
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte("[api]\nport = 1234\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Remove read permission
	if err := os.Chmod(configPath, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(configPath, 0o644) // restore so TempDir cleanup works
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should return error when config file is not readable")
	}
}

func TestLoad_OnlyCommentsInConfig(t *testing.T) {
	dir := t.TempDir()
	belayDir := filepath.Join(dir, BelayDir)
	if err := os.MkdirAll(belayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A file with only comments is valid TOML
	configContent := `# This is a comment
# Another comment
# No actual config values
`
	configPath := filepath.Join(belayDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.API.Port != DefaultAPIPort {
		t.Errorf("API.Port = %d, want default %d", cfg.API.Port, DefaultAPIPort)
	}
}
