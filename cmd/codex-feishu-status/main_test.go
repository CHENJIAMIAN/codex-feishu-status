package main

import (
	"strings"
	"testing"

	"github.com/CHENJIAMIAN/codex-feishu-status/internal/config"
)

func TestRunInitRequiresAppID(t *testing.T) {
	err := runInit([]string{"--state-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--app-id") {
		t.Fatalf("runInit error = %v, want missing app ID error", err)
	}
}

func TestRunInitPersistsExplicitAppID(t *testing.T) {
	stateDir := t.TempDir()
	if err := runInit([]string{"--state-dir", stateDir, "--app-id", "cli_test"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppID != "cli_test" {
		t.Fatalf("AppID = %q, want cli_test", cfg.AppID)
	}
}
