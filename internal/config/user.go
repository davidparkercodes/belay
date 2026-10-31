package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	UpdateNotify = "notify"
	UpdateAuto   = "auto"
	UpdateOff    = "off"
)

type UserConfig struct {
	Update UserUpdateConfig `toml:"update"`
}

type UserUpdateConfig struct {
	Mode string `toml:"mode"`
}

func NormalizeUpdateMode(mode string) (normalized string, ok bool) {
	switch m := strings.ToLower(strings.TrimSpace(mode)); m {
	case UpdateNotify, UpdateAuto, UpdateOff:
		return m, true
	case "":
		return UpdateNotify, true
	default:
		return UpdateNotify, false
	}
}

func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config dir: %w", err)
	}
	return filepath.Join(dir, "belay", ConfigFile), nil
}

func LoadUserConfig() (*UserConfig, error) {
	path, err := UserConfigPath()
	if err != nil {
		return defaultUserConfig(), err
	}
	return LoadUserConfigFrom(path)
}

func LoadUserConfigFrom(path string) (*UserConfig, error) {
	cfg := defaultUserConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return defaultUserConfig(), fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Update.Mode, _ = NormalizeUpdateMode(cfg.Update.Mode)
	return cfg, nil
}

func defaultUserConfig() *UserConfig {
	return &UserConfig{Update: UserUpdateConfig{Mode: UpdateNotify}}
}

func SetUserUpdateMode(path, mode string) error {
	normalized, ok := NormalizeUpdateMode(mode)
	if !ok || strings.TrimSpace(mode) == "" {
		return fmt.Errorf("unknown update mode %q (use notify, auto or off)", mode)
	}
	doc := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(data), &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	update, _ := doc["update"].(map[string]interface{})
	if update == nil {
		update = map[string]interface{}{}
	}
	update["mode"] = normalized
	doc["update"] = update

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return fmt.Errorf("encode user config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
