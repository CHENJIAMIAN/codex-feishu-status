package status

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Phase string

const (
	PhaseIdle    Phase = "idle"
	PhaseWorking Phase = "working"
	PhaseWaiting Phase = "waiting"
	PhaseDone    Phase = "done"
	PhaseFailed  Phase = "failed"
)

type Snapshot struct {
	SessionID        string
	FirstUserPrompt  string
	WorkingDirectory string
	Phase            Phase
	Status           string
	Activity         string
	StartedAt        time.Time
	UpdatedAt        time.Time
}

func New(sessionID string) Snapshot {
	return Snapshot{
		SessionID: sessionID,
		Phase:     PhaseIdle,
		Status:    "已连接，等待 Codex 活动",
		Activity:  "尚未收到会话事件",
	}
}

// Apply maps cxr's raw event shape to a privacy-preserving status snapshot.
// It deliberately ignores prompts, replies, command arguments, and tool output.
func (snapshot Snapshot) Apply(item map[string]any) Snapshot {
	details := object(item["details"])
	if details == nil {
		details = item
	}

	at := eventTime(item, details)
	if at.IsZero() {
		at = time.Now()
	}
	snapshot.UpdatedAt = at

	eventType := text(details["type"])
	payload := object(details["payload"])
	if eventType == "session_meta" {
		if sessionID := text(payload["session_id"]); sessionID != "" {
			snapshot.SessionID = sessionID
		}
		snapshot.set(PhaseIdle, "已连接，等待 Codex 活动", "已读取会话元数据", at)
		return snapshot
	}

	switch eventType {
	case "event_msg":
		switch text(payload["type"]) {
		case "task_started":
			snapshot.set(PhaseWorking, "正在处理请求", "开始新的任务轮次", at)
			snapshot.StartedAt = at
		case "task_complete":
			snapshot.set(PhaseDone, "本轮已完成", "等待下一条请求", at)
		case "user_message":
			snapshot.set(PhaseWorking, "正在处理请求", "收到新的用户请求", at)
			snapshot.StartedAt = at
		case "token_count":
			snapshot.set(PhaseWorking, "正在思考", "分析上下文", at)
		case "task_failed":
			snapshot.set(PhaseFailed, "任务执行异常", "等待进一步处理", at)
		}
	case "response_item":
		switch text(payload["type"]) {
		case "reasoning":
			snapshot.set(PhaseWorking, "正在思考", "分析任务", at)
		case "message":
			if text(payload["role"]) == "assistant" {
				snapshot.set(PhaseWorking, "正在回复", "整理回复内容", at)
			}
		case "custom_tool_call", "function_call":
			toolName := safeToolName(text(payload["name"]))
			if toolName == "request_user_input" {
				snapshot.set(PhaseWaiting, "等待用户输入", "Codex 需要补充信息", at)
			} else if toolName != "" {
				snapshot.set(PhaseWorking, "正在执行工具", "执行 "+toolName, at)
			} else {
				snapshot.set(PhaseWorking, "正在执行工具", "调用本地工具", at)
			}
		case "custom_tool_call_output", "function_call_output":
			snapshot.set(PhaseWorking, "正在分析工具结果", "整理工具执行结果", at)
		}
	case "turn_context":
		snapshot.set(PhaseWorking, "正在准备", "加载会话上下文", at)
	}

	if text(item["kind"]) == "assistant" {
		snapshot.set(PhaseWorking, "正在回复", "整理回复内容", at)
	}
	return snapshot
}

func (snapshot Snapshot) Card(now time.Time) (string, error) {
	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}

	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": cardTemplate(snapshot.Phase),
			"title": map[string]string{
				"tag":     "plain_text",
				"content": "Codex 实时状态",
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"fields": []any{
					field("状态", snapshot.Status),
					field("当前活动", snapshot.Activity),
					field("会话", shortSessionID(snapshot.SessionID)),
					field("已运行", elapsed(snapshot.StartedAt, now)),
				},
			},
			map[string]string{"tag": "hr"},
			map[string]any{
				"tag": "note",
				"elements": []any{map[string]string{
					"tag":     "plain_text",
					"content": "最后活动：" + updatedAt.In(time.Local).Format("15:04:05"),
				}},
			},
		},
	}

	contents, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("encode Feishu status card: %w", err)
	}
	return string(contents), nil
}

func (snapshot *Snapshot) set(phase Phase, status, activity string, at time.Time) {
	snapshot.Phase = phase
	snapshot.Status = status
	snapshot.Activity = activity
	if snapshot.StartedAt.IsZero() && phase == PhaseWorking {
		snapshot.StartedAt = at
	}
}

func field(label, value string) map[string]any {
	return map[string]any{
		"is_short": true,
		"text": map[string]string{
			"tag":     "lark_md",
			"content": "**" + label + "**\n" + safeCardText(value),
		},
	}
}

func cardTemplate(phase Phase) string {
	switch phase {
	case PhaseDone:
		return "green"
	case PhaseWaiting:
		return "orange"
	case PhaseFailed:
		return "red"
	default:
		return "blue"
	}
}

func elapsed(start, now time.Time) string {
	if start.IsZero() {
		return "尚未开始"
	}
	duration := now.Sub(start)
	if duration < 0 {
		duration = 0
	}
	duration = duration.Round(time.Second)
	if duration >= time.Hour {
		return fmt.Sprintf("%dh%02dm%02ds", int(duration.Hours()), int(duration.Minutes())%60, int(duration.Seconds())%60)
	}
	if duration >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(duration.Minutes()), int(duration.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(duration.Seconds()))
}

func shortSessionID(sessionID string) string {
	value := safeCardText(sessionID)
	if value == "" {
		return "未指定"
	}
	if len(value) > 12 {
		return value[:12] + "..."
	}
	return value
}

func eventTime(item, details map[string]any) time.Time {
	for _, candidate := range []string{text(item["timestamp"]), text(details["timestamp"])} {
		if parsed, err := time.Parse(time.RFC3339Nano, candidate); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func object(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func text(value any) string {
	stringValue, _ := value.(string)
	return strings.TrimSpace(stringValue)
}

func safeToolName(name string) string {
	if name == "" {
		return ""
	}
	var builder strings.Builder
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_-.:/", character) {
			builder.WriteRune(character)
		}
	}
	return strings.TrimSpace(builder.String())
}

func safeCardText(value string) string {
	return sanitizeCardText(value, 96)
}

func safePromptText(value string) string {
	return sanitizeCardText(value, 180)
}

func safeDirectoryText(value string) string {
	return sanitizeCardText(value, 160)
}

func sanitizeCardText(value string, maxRunes int) string {
	value = strings.NewReplacer("\n", " ", "\r", " ", "*", "", "`", "").Replace(value)
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return value
}
