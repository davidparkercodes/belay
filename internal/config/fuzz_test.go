package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzConfigLoad feeds arbitrary bytes as a config file to Load() and ensures
// it never panics. Returning an error is acceptable; crashing is not.
func FuzzConfigLoad(f *testing.F) {
	// Seed corpus: valid TOML, empty, partial, and binary data.
	f.Add([]byte(`[daemon]
log_level = "info"
log_max_size_mb = 10
log_max_files = 3

[watcher]
debounce_ms = 50
exclude_hidden = false

[storage]
segment_max_bytes = 67108864
fsync_mode = "interval"
fsync_interval_ms = 100
compression_enabled = true

[retention]
hot_hours = 24
warm_days = 7
cold_days = 30
archive_days = 365
max_storage_gb = 10

[api]
port = 33412
enabled = true

[safety]
allow_writes = false
`))
	f.Add([]byte(""))
	f.Add([]byte("# just a comment\n"))
	f.Add([]byte(`[daemon]
log_level = "debug"
`))
	f.Add([]byte(`[api]
port = 99999
enabled = "not a bool"
`))
	f.Add([]byte{0x00, 0xff, 0xfe, 0x89, 0x50, 0x4e, 0x47})
	f.Add([]byte(`[[[invalid
garbage = = = !!!
`))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		belayDir := filepath.Join(dir, BelayDir)
		if err := os.MkdirAll(belayDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		configPath := filepath.Join(belayDir, ConfigFile)
		if err := os.WriteFile(configPath, data, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		// Call Load -- we only care that it does not panic.
		// Errors are fine (invalid TOML, type mismatches, etc.).
		_, _ = Load(dir)
	})
}

// FuzzConfigToTOML feeds arbitrary string values into a Config and ensures
// ToTOML() never panics when rendering them.
func FuzzConfigToTOML(f *testing.F) {
	f.Add("info", "interval", 50, int64(67108864), 100, 33412, true, false)
	f.Add("debug", "every", 0, int64(0), 0, 0, false, true)
	f.Add("", "", -1, int64(-1), -1, -1, true, true)
	f.Add("warn\ninjected = true", "batch\"quoted", 999999, int64(1<<62), 2147483647, 65535, false, false)
	f.Add("\x00\x01\x02", "\xff\xfe\xfd", 0, int64(0), 0, 0, true, false)

	f.Fuzz(func(t *testing.T, logLevel, fsyncMode string, debounceMs int, segmentMaxBytes int64, fsyncIntervalMs int, apiPort int, compressionEnabled, allowWrites bool) {
		cfg := DefaultConfig("/tmp/fuzz-project")
		cfg.Daemon.LogLevel = logLevel
		cfg.Storage.FsyncMode = fsyncMode
		cfg.Watcher.DebounceMs = debounceMs
		cfg.Storage.SegmentMaxBytes = segmentMaxBytes
		cfg.Storage.FsyncIntervalMs = fsyncIntervalMs
		cfg.API.Port = apiPort
		cfg.Storage.CompressionEnabled = compressionEnabled
		cfg.Safety.AllowWrites = allowWrites

		// Must not panic.
		_ = cfg.ToTOML()
	})
}
