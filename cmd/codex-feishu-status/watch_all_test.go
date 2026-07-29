package main

import (
	"testing"

	"github.com/CHENJIAMIAN/codex-feishu-status/internal/config"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/feishu"
)

func TestHelpCommands(t *testing.T) {
	for _, command := range []string{"/help", "help", "设置", "/设置"} {
		if !isHelpCommand(command) {
			t.Fatalf("%q should be a help command", command)
		}
	}
	if isHelpCommand("/helper") {
		t.Fatal("unexpected help command match")
	}
}

func TestApplySettingsActionPersistsWindow(t *testing.T) {
	store := newConfigStore(t.TempDir(), config.Config{})
	toast, resendOverview, err := applySettingsAction(store, feishu.CardAction{Value: map[string]any{
		"action": "set_window",
		"value":  "60",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if toast == "" {
		t.Fatal("expected success toast")
	}
	if !resendOverview {
		t.Fatal("settings change should resend the overview")
	}
	if got := store.Snapshot().OverviewPreferences.ActiveWindowMinutes; got != 60 {
		t.Fatalf("ActiveWindowMinutes = %d, want 60", got)
	}
}

func TestConfigStorePersistsSettingsCardMessageID(t *testing.T) {
	store := newConfigStore(t.TempDir(), config.Config{})
	if err := store.Update(func(cfg *config.Config) {
		cfg.SettingsMessageID = "settings-message"
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().SettingsMessageID; got != "settings-message" {
		t.Fatalf("SettingsMessageID = %q", got)
	}
}

func TestReusableMessageID(t *testing.T) {
	if got := reusableMessageID(map[string]string{"a": "message-a"}); got != "message-a" {
		t.Fatalf("message ID = %q", got)
	}
	if got := reusableMessageID(map[string]string{"a": "message-a", "b": "message-b"}); got != "" {
		t.Fatalf("message ID = %q, want empty", got)
	}
}

func TestRefreshActionResendsOverview(t *testing.T) {
	store := newConfigStore(t.TempDir(), config.Config{})
	_, resendOverview, err := applySettingsAction(store, feishu.CardAction{Value: map[string]any{
		"action": "refresh",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !resendOverview {
		t.Fatal("refresh should resend the overview")
	}
}
