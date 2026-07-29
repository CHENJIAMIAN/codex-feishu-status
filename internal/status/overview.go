package status

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// OverviewOptions controls the presentation of locally observed session states.
type OverviewOptions struct {
	ActiveWindow       time.Duration
	IncludeRecent      bool
	MaxVisibleSessions int
}

func OverviewCard(snapshots []Snapshot, options OverviewOptions, now time.Time) (string, error) {
	visible, total := overviewSnapshots(snapshots, options)
	mode := "仅显示进行中的会话"
	if options.IncludeRecent {
		mode = "显示近期活动的会话"
	}
	window := overviewWindow(options.ActiveWindow)

	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]string{
				"tag":     "lark_md",
				"content": fmt.Sprintf("**%s**\n活跃窗口：%s · %s", mode, window, overviewCount(total, len(visible))),
			},
		},
	}
	if len(visible) == 0 {
		elements = append(elements, map[string]any{
			"tag": "div",
			"text": map[string]string{
				"tag":     "plain_text",
				"content": "暂无符合当前条件的 Codex 会话",
			},
		})
	} else {
		for index, snapshot := range visible {
			elements = append(elements, map[string]any{
				"tag": "div",
				"fields": []any{
					field("会话", shortSessionID(snapshot.SessionID)),
					field("状态", snapshot.Status),
					field("当前活动", snapshot.Activity),
					field("已运行", elapsed(snapshot.StartedAt, now)),
				},
			})
			if prompt := safePromptText(snapshot.FirstUserPrompt); prompt != "" {
				elements = append(elements, map[string]any{
					"tag": "div",
					"text": map[string]string{
						"tag":     "lark_md",
						"content": "**首个请求**\n" + prompt,
					},
				})
			}
			directory := safeDirectoryText(snapshot.WorkingDirectory)
			if directory == "" {
				directory = "未记录"
			}
			elements = append(elements, map[string]any{
				"tag": "div",
				"text": map[string]string{
					"tag":     "lark_md",
					"content": "**工作目录**\n" + directory,
				},
			})
			if index < len(visible)-1 {
				elements = append(elements, map[string]string{"tag": "hr"})
			}
		}
	}
	elements = append(elements,
		map[string]string{"tag": "hr"},
		map[string]any{
			"tag": "note",
			"elements": []any{map[string]string{
				"tag":     "plain_text",
				"content": "最后刷新：" + now.In(time.Local).Format("15:04:05") + " · 发送 /help 调整显示设置",
			}},
		},
	)

	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": overviewTemplate(visible),
			"title": map[string]string{
				"tag":     "plain_text",
				"content": "Codex 会话总览",
			},
		},
		"elements": elements,
	}
	contents, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("encode Feishu overview card: %w", err)
	}
	return string(contents), nil
}

func SettingsCard(options OverviewOptions) (string, error) {
	windowMinutes := int(options.ActiveWindow.Round(time.Minute).Minutes())
	if windowMinutes <= 0 {
		windowMinutes = 1
	}
	mode := "仅进行中"
	if options.IncludeRecent {
		mode = "包含近期活动"
	}

	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]string{
				"tag":     "plain_text",
				"content": "Codex 状态设置",
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"fields": []any{
					field("活跃窗口", strconv.Itoa(windowMinutes)+" 分钟"),
					field("显示范围", mode),
				},
			},
			map[string]any{
				"tag":     "action",
				"actions": windowButtons(windowMinutes),
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					settingsButton("仅进行中", "set_include_recent", "false", !options.IncludeRecent),
					settingsButton("包含近期活动", "set_include_recent", "true", options.IncludeRecent),
					settingsButton("立即刷新", "refresh", "", false),
				},
			},
			map[string]any{
				"tag": "note",
				"elements": []any{map[string]string{
					"tag":     "plain_text",
					"content": "设置只影响当前已绑定的飞书聊天；总览展示首个请求和工作目录。",
				}},
			},
		},
	}
	contents, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("encode Feishu settings card: %w", err)
	}
	return string(contents), nil
}

func overviewSnapshots(snapshots []Snapshot, options OverviewOptions) ([]Snapshot, int) {
	filtered := make([]Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !options.IncludeRecent && snapshot.Phase != PhaseWorking && snapshot.Phase != PhaseWaiting {
			continue
		}
		filtered = append(filtered, snapshot)
	}
	sort.SliceStable(filtered, func(left, right int) bool {
		if filtered[left].UpdatedAt.Equal(filtered[right].UpdatedAt) {
			return filtered[left].SessionID < filtered[right].SessionID
		}
		return filtered[left].UpdatedAt.After(filtered[right].UpdatedAt)
	})
	total := len(filtered)
	limit := options.MaxVisibleSessions
	if limit <= 0 || limit > total {
		limit = total
	}
	return filtered[:limit], total
}

func overviewCount(total, visible int) string {
	if total == visible {
		return fmt.Sprintf("%d 条会话", total)
	}
	return fmt.Sprintf("已显示 %d / %d 条会话", visible, total)
}

func overviewWindow(duration time.Duration) string {
	minutes := int(duration.Round(time.Minute).Minutes())
	if minutes <= 0 {
		minutes = 1
	}
	return strconv.Itoa(minutes) + " 分钟"
}

func overviewTemplate(snapshots []Snapshot) string {
	for _, snapshot := range snapshots {
		if snapshot.Phase == PhaseFailed {
			return "red"
		}
	}
	for _, snapshot := range snapshots {
		if snapshot.Phase == PhaseWaiting {
			return "orange"
		}
	}
	for _, snapshot := range snapshots {
		if snapshot.Phase == PhaseWorking {
			return "blue"
		}
	}
	return "green"
}

func windowButtons(selected int) []any {
	buttons := make([]any, 0, 4)
	for _, minutes := range []int{1, 5, 15, 30} {
		buttons = append(buttons, settingsButton(strconv.Itoa(minutes)+" 分钟", "set_window", strconv.Itoa(minutes), minutes == selected))
	}
	return buttons
}

func settingsButton(label, action, value string, selected bool) map[string]any {
	buttonType := "default"
	if selected {
		buttonType = "primary"
	}
	payload := map[string]string{"action": action}
	if value != "" {
		payload["value"] = value
	}
	return map[string]any{
		"tag":  "button",
		"type": buttonType,
		"text": map[string]string{
			"tag":     "plain_text",
			"content": label,
		},
		"value": payload,
	}
}
