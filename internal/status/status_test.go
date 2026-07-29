package status

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApplyMapsToolCallsWithoutLeakingArguments(t *testing.T) {
	snapshot := New("test-session-123456789")
	snapshot = snapshot.Apply(map[string]any{
		"timestamp": "2026-07-29T04:00:00Z",
		"details": map[string]any{
			"type": "response_item",
			"payload": map[string]any{
				"type":  "custom_tool_call",
				"name":  "shell_command",
				"input": "dangerously-private-command --token should-not-appear",
			},
		},
	})

	if snapshot.Phase != PhaseWorking {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, PhaseWorking)
	}
	if snapshot.Status != "正在执行工具" {
		t.Fatalf("status = %q", snapshot.Status)
	}
	if snapshot.Activity != "执行 shell_command" {
		t.Fatalf("activity = %q", snapshot.Activity)
	}
	card, err := snapshot.Card(time.Date(2026, 7, 29, 4, 0, 2, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card, "should-not-appear") {
		t.Fatalf("card leaked tool input: %s", card)
	}
}

func TestApplyMapsUserInputAndCompletion(t *testing.T) {
	snapshot := New("session")
	snapshot = snapshot.Apply(rawResponseItem("custom_tool_call", "request_user_input"))
	if snapshot.Phase != PhaseWaiting || snapshot.Status != "等待用户输入" {
		t.Fatalf("waiting snapshot = %#v", snapshot)
	}

	snapshot = snapshot.Apply(map[string]any{
		"details": map[string]any{
			"type":    "event_msg",
			"payload": map[string]any{"type": "task_complete"},
		},
	})
	if snapshot.Phase != PhaseDone || snapshot.Status != "本轮已完成" {
		t.Fatalf("completed snapshot = %#v", snapshot)
	}
}

func TestCardUsesMultiUpdateConfiguration(t *testing.T) {
	snapshot := New("session-123456789")
	card, err := snapshot.Card(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(card), &decoded); err != nil {
		t.Fatal(err)
	}
	config := decoded["config"].(map[string]any)
	if config["update_multi"] != true {
		t.Fatalf("update_multi = %#v, want true", config["update_multi"])
	}
}

func TestOverviewCardFiltersCompletedSessionsAndLimitsRows(t *testing.T) {
	now := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	working := New("working-session")
	working.Phase = PhaseWorking
	working.Status = "正在处理请求"
	working.Activity = "分析任务"
	working.FirstUserPrompt = "修复飞书总览卡的性能问题"
	working.WorkingDirectory = `D:\CodexWork\codex-feishu-status`
	working.UpdatedAt = now
	completed := New("completed-session")
	completed.Phase = PhaseDone
	completed.Status = "本轮已完成"
	completed.Activity = "等待下一条请求"
	completed.UpdatedAt = now.Add(-time.Minute)

	card, err := OverviewCard([]Snapshot{completed, working}, OverviewOptions{
		ActiveWindow:       5 * time.Minute,
		MaxVisibleSessions: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card, "completed-session") {
		t.Fatalf("overview included completed session: %s", card)
	}
	if !strings.Contains(card, "working-sess") {
		t.Fatalf("overview omitted working session: %s", card)
	}
	if !strings.Contains(card, "修复飞书总览卡的性能问题") {
		t.Fatalf("overview omitted first user prompt: %s", card)
	}
	if !strings.Contains(card, `D:\\CodexWork\\codex-feishu-status`) {
		t.Fatalf("overview omitted working directory: %s", card)
	}
}

func TestSettingsCardIncludesActionValues(t *testing.T) {
	card, err := SettingsCard(OverviewOptions{
		ActiveWindow:  5 * time.Minute,
		IncludeRecent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, `"action":"set_window"`) || !strings.Contains(card, `"value":"60"`) || !strings.Contains(card, `"action":"refresh"`) {
		t.Fatalf("settings card actions missing: %s", card)
	}
}

func TestCardUsesRenderedNewlines(t *testing.T) {
	card, err := New("session").Card(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(card), &decoded); err != nil {
		t.Fatal(err)
	}
	elements := decoded["elements"].([]any)
	fields := elements[0].(map[string]any)["fields"].([]any)
	content := fields[0].(map[string]any)["text"].(map[string]any)["content"].(string)
	if !strings.Contains(content, "\n") || strings.Contains(content, "\\n") {
		t.Fatalf("card newline = %q", content)
	}
}

func TestPromptTextPreservesChineseRuneBoundaries(t *testing.T) {
	prompt := strings.Repeat("测", 181)
	formatted := safePromptText(prompt)
	if got := len([]rune(formatted)); got != 183 {
		t.Fatalf("prompt length = %d, want 183", got)
	}
}

func TestOverviewSeparatesSessionEntries(t *testing.T) {
	now := time.Now()
	first := New("first")
	first.Phase = PhaseWorking
	first.UpdatedAt = now
	second := New("second")
	second.Phase = PhaseWorking
	second.UpdatedAt = now.Add(-time.Second)

	card, err := OverviewCard([]Snapshot{first, second}, OverviewOptions{
		ActiveWindow:       5 * time.Minute,
		MaxVisibleSessions: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(card, `"tag":"hr"`); got != 2 {
		t.Fatalf("divider count = %d, want 2", got)
	}
}

func rawResponseItem(kind, name string) map[string]any {
	return map[string]any{
		"details": map[string]any{
			"type": "response_item",
			"payload": map[string]any{
				"type": kind,
				"name": name,
			},
		},
	}
}
