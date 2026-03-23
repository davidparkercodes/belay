// Package config handles Belay project configuration loading and defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// BelayDir is the name of the Belay data directory within a project.
	BelayDir = ".belay"

	// ConfigFile is the name of the TOML configuration file.
	ConfigFile = "config.toml"

	// DefaultAPIPort is the default HTTP API port.
	DefaultAPIPort = 33412

	// DefaultDebounceMs is the default debounce window in milliseconds.
	DefaultDebounceMs = 50

	// DefaultSegmentMaxBytes is the default maximum segment file size (64 MB).
	DefaultSegmentMaxBytes = 64 * 1024 * 1024

	// DefaultHotHours is the default hot tier retention in hours.
	DefaultHotHours = 24

	// DefaultWarmDays is the default warm tier retention in days.
	DefaultWarmDays = 7

	// DefaultColdDays is the default cold tier retention in days.
	DefaultColdDays = 30

	// DefaultArchiveDays is the default archive tier retention in days.
	DefaultArchiveDays = 365

	// DefaultMaxStorageGB is the default storage budget in gigabytes.
	DefaultMaxStorageGB = 10

	// DefaultMaxFileSizeMB is the default max file size for content capture (50 MB).
	DefaultMaxFileSizeMB = 50

	// DefaultShutdownTimeoutSec is the default graceful shutdown timeout in seconds.
	DefaultShutdownTimeoutSec = 10
)

// Config holds all Belay configuration for a project.
type Config struct {
	ProjectRoot string `toml:"-"`

	BelayPath string `toml:"-"`

	Daemon DaemonConfig `toml:"daemon"`

	Watcher WatcherConfig `toml:"watcher"`

	Storage StorageConfig `toml:"storage"`

	Retention RetentionConfig `toml:"retention"`

	API APIConfig `toml:"api"`

	Safety SafetyConfig `toml:"safety"`
}

// SafetyConfig controls destructive operation safeguards.
type SafetyConfig struct {
	AllowWrites bool `toml:"allow_writes"`
}

// DaemonConfig holds daemon process settings.
type DaemonConfig struct {
	LogLevel string `toml:"log_level"`

	LogMaxSizeMB int `toml:"log_max_size_mb"`

	LogMaxFiles int `toml:"log_max_files"`

	// ShutdownTimeoutSec is the maximum time to wait for graceful shutdown.
	// If cleanup takes longer, the daemon force-exits.
	ShutdownTimeoutSec int `toml:"shutdown_timeout_sec"`
}

// WatcherConfig holds filesystem watcher settings.
type WatcherConfig struct {
	DebounceMs int `toml:"debounce_ms"`

	ExcludeHidden bool `toml:"exclude_hidden"`

	// MaxFileSizeMB is the maximum file size (in MB) for content capture.
	// Files exceeding this limit will still emit events but without content storage.
	MaxFileSizeMB int `toml:"max_file_size_mb"`
}

// StorageConfig holds event log and object store settings.
type StorageConfig struct {
	SegmentMaxBytes int64 `toml:"segment_max_bytes"`

	FsyncMode string `toml:"fsync_mode"`

	FsyncIntervalMs int `toml:"fsync_interval_ms"`

	CompressionEnabled bool `toml:"compression_enabled"`
}

// RetentionConfig holds tiered retention policy durations.
type RetentionConfig struct {
	HotHours      int `toml:"hot_hours"`
	WarmDays      int `toml:"warm_days"`
	ColdDays      int `toml:"cold_days"`
	ArchiveDays   int `toml:"archive_days"`
	MaxStorageGB  int `toml:"max_storage_gb"`
}

// APIConfig holds HTTP API server settings.
type APIConfig struct {
	Port int `toml:"port"`

	// Host is the address to bind the API server to. Default: "127.0.0.1".
	Host string `toml:"host"`

	Enabled bool `toml:"enabled"`
}

// DefaultConfig returns a Config with sensible defaults for the given project root.
func DefaultConfig(projectRoot string) *Config {
	belayPath := filepath.Join(projectRoot, BelayDir)
	return &Config{
		ProjectRoot: projectRoot,
		BelayPath:  belayPath,
		Daemon: DaemonConfig{
			LogLevel:           "info",
			LogMaxSizeMB:       10,
			LogMaxFiles:        3,
			ShutdownTimeoutSec: DefaultShutdownTimeoutSec,
		},
		Watcher: WatcherConfig{
			DebounceMs:    DefaultDebounceMs,
			ExcludeHidden: false,
			MaxFileSizeMB: DefaultMaxFileSizeMB,
		},
		Storage: StorageConfig{
			SegmentMaxBytes:    DefaultSegmentMaxBytes,
			FsyncMode:          "interval",
			FsyncIntervalMs:    100,
			CompressionEnabled: true,
		},
		Retention: RetentionConfig{
			HotHours:     DefaultHotHours,
			WarmDays:     DefaultWarmDays,
			ColdDays:     DefaultColdDays,
			ArchiveDays:  DefaultArchiveDays,
			MaxStorageGB: DefaultMaxStorageGB,
		},
		API: APIConfig{
			Port:    DefaultAPIPort,
			Host:    "127.0.0.1",
			Enabled: true,
		},
	}
}

// EventsDir returns the path to the event log segments directory.
func (c *Config) EventsDir() string {
	return filepath.Join(c.BelayPath, "events")
}

// ObjectsDir returns the path to the content-addressable object store directory.
func (c *Config) ObjectsDir() string {
	return filepath.Join(c.BelayPath, "objects")
}

// StashesDir returns the path to the session stashes directory.
func (c *Config) StashesDir() string {
	return filepath.Join(c.BelayPath, "stashes")
}

// IndexPath returns the path to the SQLite index database.
func (c *Config) IndexPath() string {
	return filepath.Join(c.BelayPath, "index.db")
}

// PIDPath returns the path to the daemon PID file.
func (c *Config) PIDPath() string {
	return filepath.Join(c.BelayPath, "daemon.pid")
}

// LogPath returns the path to the daemon log file.
func (c *Config) LogPath() string {
	return filepath.Join(c.BelayPath, "daemon.log")
}

// SocketPath returns the path to the daemon Unix socket.
func (c *Config) SocketPath() string {
	return filepath.Join(c.BelayPath, "daemon.sock")
}

// ConfigPath returns the path to the TOML configuration file.
func (c *Config) ConfigPath() string {
	return filepath.Join(c.BelayPath, ConfigFile)
}

// ToTOML renders the configuration as a documented TOML string.
func (c *Config) ToTOML() string {
	return fmt.Sprintf(`# Belay Configuration
# Local version control for AI agentic coding

[daemon]
# Log level: debug, info, warn, error
log_level = %q

# Max daemon log size in MB before rotation
log_max_size_mb = %d

# Number of rotated log files to keep
log_max_files = %d

# Maximum seconds to wait for graceful shutdown before force-exiting
shutdown_timeout_sec = %d

[watcher]
# Debounce window for rapid file writes (milliseconds)
# Lower = more granular history, higher = less noise
debounce_ms = %d

# Skip hidden files and directories
exclude_hidden = %v

# Max file size in MB for content capture (files larger than this
# still generate events but content is not stored)
max_file_size_mb = %d

[storage]
# Max event log segment size in bytes (default 64MB)
segment_max_bytes = %d

# Fsync mode: "every" (safest), "interval" (balanced), "batch" (fastest)
fsync_mode = %q

# Fsync interval in ms (when mode = "interval")
fsync_interval_ms = %d

# Enable gzip compression for stored objects
compression_enabled = %v

[retention]
# Hours of full-fidelity retention (every event kept)
hot_hours = %d

# Days of deduplicated retention (rapid edits collapsed)
warm_days = %d

# Days of summarized retention (session boundaries only)
cold_days = %d

# Days of minimal retention (daily snapshots only, 0 = forever)
archive_days = %d

# Storage budget in GB (triggers aggressive compaction when exceeded)
max_storage_gb = %d

[api]
# HTTP API port for dashboard and external tools
port = %d

# Address to bind the API server to
host = %q

# Enable the embedded API server
enabled = %v

[safety]
# IMPORTANT: When false (default), all destructive commands run in dry-run mode.
# Destructive commands: restore, commit, replay --output, snapshot --output
# Set to true ONLY when you are confident Belay is working correctly.
allow_writes = %v
`,
		c.Daemon.LogLevel, c.Daemon.LogMaxSizeMB, c.Daemon.LogMaxFiles, c.Daemon.ShutdownTimeoutSec,
		c.Watcher.DebounceMs, c.Watcher.ExcludeHidden, c.Watcher.MaxFileSizeMB,
		c.Storage.SegmentMaxBytes, c.Storage.FsyncMode, c.Storage.FsyncIntervalMs, c.Storage.CompressionEnabled,
		c.Retention.HotHours, c.Retention.WarmDays, c.Retention.ColdDays, c.Retention.ArchiveDays, c.Retention.MaxStorageGB,
		c.API.Port, c.API.Host, c.API.Enabled,
		c.Safety.AllowWrites,
	)
}

// WritesAllowed reports whether destructive operations are enabled.
func (c *Config) WritesAllowed() bool {
	return c.Safety.AllowWrites
}

// FindProjectRoot walks up from the current directory to find the nearest .belay/ directory.
func FindProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for {
		belayPath := filepath.Join(dir, BelayDir)
		if info, err := os.Stat(belayPath); err == nil && info.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not a belay project (no .belay/ directory found in %s or any parent)", dir)
		}
		dir = parent
	}
}

// Load reads the TOML config file from the project root, falling back to defaults if absent.
func Load(projectRoot string) (*Config, error) {
	cfg := DefaultConfig(projectRoot)

	configPath := cfg.ConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(configPath, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", configPath, err)
	}

	cfg.ProjectRoot = projectRoot
	cfg.BelayPath = filepath.Join(projectRoot, BelayDir)

	return cfg, nil
}
