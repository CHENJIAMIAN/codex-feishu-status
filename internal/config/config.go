package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultSecretName          = "FEISHU_CODEX_STATUS_APP_SECRET"
	DefaultActiveWindowMinutes = 5
	DefaultMaxVisibleSessions  = 16
	configFileName             = "config.json"
)

// Config contains local routing state only. Secrets stay in the Codex DPAPI vault.
type Config struct {
	AppID               string              `json:"app_id"`
	SecretName          string              `json:"secret_name"`
	ChatID              string              `json:"chat_id,omitempty"`
	MessageIDs          map[string]string   `json:"message_ids,omitempty"`
	OverviewMessageID   string              `json:"overview_message_id,omitempty"`
	SettingsMessageID   string              `json:"settings_message_id,omitempty"`
	OverviewPreferences OverviewPreferences `json:"overview_preferences"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

// OverviewPreferences controls which locally observed sessions appear on the Feishu overview card.
// It contains no Codex prompts, replies, or credentials.
type OverviewPreferences struct {
	ActiveWindowMinutes int  `json:"active_window_minutes"`
	IncludeRecent       bool `json:"include_recent"`
	MaxVisibleSessions  int  `json:"max_visible_sessions"`
}

func (preferences OverviewPreferences) ActiveWindow() time.Duration {
	return time.Duration(preferences.ActiveWindowMinutes) * time.Minute
}

func DefaultStateDir() string {
	if configured := strings.TrimSpace(os.Getenv("CODEX_FEISHU_STATUS_HOME")); configured != "" {
		return configured
	}

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(`D:\CodexWork`); err == nil {
			return `D:\CodexWork\codex-feishu-status\data`
		}
	}

	if userConfigDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(userConfigDir, "codex-feishu-status")
	}
	return "codex-feishu-status-data"
}

func FilePath(stateDir string) string {
	return filepath.Join(stateDir, configFileName)
}

func Load(stateDir string) (Config, error) {
	path := FilePath(stateDir)
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return withDefaults(Config{}), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read status config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(contents, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse status config: %w", err)
	}
	return withDefaults(cfg), nil
}

func Save(stateDir string, cfg Config) error {
	cfg = withDefaults(cfg)
	cfg.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create status config directory: %w", err)
	}

	contents, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode status config: %w", err)
	}
	contents = append(contents, '\n')

	path := FilePath(stateDir)
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, contents, 0o600); err != nil {
		return fmt.Errorf("write temporary status config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace status config: %w", err)
	}
	return nil
}

func withDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.SecretName) == "" {
		cfg.SecretName = DefaultSecretName
	}
	if cfg.MessageIDs == nil {
		cfg.MessageIDs = make(map[string]string)
	}
	if cfg.OverviewPreferences.ActiveWindowMinutes <= 0 {
		cfg.OverviewPreferences.ActiveWindowMinutes = DefaultActiveWindowMinutes
	}
	if cfg.OverviewPreferences.ActiveWindowMinutes > 60 {
		cfg.OverviewPreferences.ActiveWindowMinutes = 60
	}
	if cfg.OverviewPreferences.MaxVisibleSessions <= 0 {
		cfg.OverviewPreferences.MaxVisibleSessions = DefaultMaxVisibleSessions
	}
	if cfg.OverviewPreferences.MaxVisibleSessions > 20 {
		cfg.OverviewPreferences.MaxVisibleSessions = 20
	}
	return cfg
}
