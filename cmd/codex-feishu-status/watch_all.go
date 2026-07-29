package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CHENJIAMIAN/codex-feishu-status/internal/config"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/cxr"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/feishu"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/status"
)

const (
	defaultAllScanInterval = 5 * time.Second
	allSessionScanCount    = 20
	maxConcurrentWatches   = 8
)

type configStore struct {
	stateDir string

	mu      sync.RWMutex
	config  config.Config
	changed chan struct{}
}

func newConfigStore(stateDir string, current config.Config) *configStore {
	return &configStore{
		stateDir: stateDir,
		config:   cloneConfig(current),
		changed:  make(chan struct{}, 1),
	}
}

func (store *configStore) Snapshot() config.Config {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneConfig(store.config)
}

func (store *configStore) Update(change func(*config.Config)) error {
	store.mu.Lock()
	next := cloneConfig(store.config)
	change(&next)
	if err := config.Save(store.stateDir, next); err != nil {
		store.mu.Unlock()
		return err
	}
	store.config = next
	store.mu.Unlock()
	store.Notify()
	return nil
}

func (store *configStore) Notify() {
	select {
	case store.changed <- struct{}{}:
	default:
	}
}

func cloneConfig(current config.Config) config.Config {
	copy := current
	copy.MessageIDs = make(map[string]string, len(current.MessageIDs))
	for sessionID, messageID := range current.MessageIDs {
		copy.MessageIDs[sessionID] = messageID
	}
	return copy
}

func runWatchAll(args []string) error {
	flags := flag.NewFlagSet("watch-all", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", config.DefaultStateDir(), "状态文件目录")
	cxrExecutable := flags.String("cxr", "", "cxr 可执行文件、.ps1 或 .js 路径")
	scanIntervalSeconds := flags.Int("scan-interval", int(defaultAllScanInterval.Seconds()), "会话扫描间隔（秒）")
	dryRun := flags.Bool("dry-run", false, "只输出总览卡 JSON，不调用飞书")
	once := flags.Bool("once", false, "扫描并发布一次后退出")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *scanIntervalSeconds < 1 {
		return errors.New("scan-interval 必须至少为 1 秒")
	}

	cfg, err := config.Load(*stateDir)
	if err != nil {
		return err
	}
	if !*dryRun && strings.TrimSpace(cfg.ChatID) == "" {
		return errors.New("尚未绑定飞书会话，请先运行 pair")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	state := newConfigStore(*stateDir, cfg)
	if *dryRun {
		return monitorAll(ctx, state, *cxrExecutable, time.Duration(*scanIntervalSeconds)*time.Second, true, nil, *once, nil)
	}

	secret, err := readSecret(cfg)
	if err != nil {
		return err
	}
	messenger, err := feishu.NewMessenger(cfg.AppID, secret)
	if err != nil {
		return err
	}
	if *once {
		return monitorAll(ctx, state, *cxrExecutable, time.Duration(*scanIntervalSeconds)*time.Second, false, messenger, true, nil)
	}

	monitorDone := make(chan error, 1)
	controlDone := make(chan error, 1)
	overviewRefreshes := make(chan overviewRefreshRequest)
	go func() {
		monitorDone <- monitorAll(ctx, state, *cxrExecutable, time.Duration(*scanIntervalSeconds)*time.Second, false, messenger, false, overviewRefreshes)
	}()
	go func() {
		controlDone <- feishu.RunControl(ctx, cfg.AppID, secret, controlCallbacks(state, messenger, overviewRefreshes))
	}()

	select {
	case err := <-monitorDone:
		if ctx.Err() != nil || cxr.IsContextCancellation(err) {
			return nil
		}
		if err == nil {
			return errors.New("会话总览监控意外停止")
		}
		return err
	case err := <-controlDone:
		if ctx.Err() != nil || cxr.IsContextCancellation(err) {
			return nil
		}
		if err == nil {
			return errors.New("飞书控制连接意外停止")
		}
		return fmt.Errorf("飞书控制连接：%w", err)
	case <-ctx.Done():
		return nil
	}
}

func controlCallbacks(state *configStore, messenger feishu.Messenger, overviewRefreshes chan<- overviewRefreshRequest) feishu.ControlCallbacks {
	return feishu.ControlCallbacks{
		OnText: func(ctx context.Context, message feishu.IncomingMessage) error {
			cfg := state.Snapshot()
			if message.ChatID != cfg.ChatID || !isHelpCommand(message.Text) {
				return nil
			}
			contents, err := status.SettingsCard(overviewOptions(cfg.OverviewPreferences))
			if err != nil {
				return err
			}
			messageID, err := messenger.CreateCard(ctx, cfg.ChatID, contents)
			if err != nil {
				return err
			}
			return state.Update(func(current *config.Config) {
				current.SettingsMessageID = messageID
			})
		},
		OnCardAction: func(ctx context.Context, action feishu.CardAction) (string, error) {
			cfg := state.Snapshot()
			if action.ChatID != cfg.ChatID {
				return "", nil
			}
			toast, resendOverview, err := applySettingsAction(state, action)
			if err != nil {
				return "", err
			}
			updated := state.Snapshot()
			contents, err := status.SettingsCard(overviewOptions(updated.OverviewPreferences))
			if err != nil {
				return "", err
			}
			if err := publishSettingsCard(ctx, state, messenger, action.MessageID, contents); err != nil {
				return "", err
			}
			refreshCtx, stopRefresh := context.WithTimeout(ctx, 2*time.Second)
			defer stopRefresh()
			if err := requestOverviewRefresh(refreshCtx, overviewRefreshes, resendOverview); err != nil {
				return toast + "，总览正在刷新", nil
			}
			return toast, nil
		},
	}
}

func publishSettingsCard(ctx context.Context, state *configStore, messenger feishu.Messenger, actionMessageID, contents string) error {
	cfg := state.Snapshot()
	messageID := strings.TrimSpace(actionMessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(cfg.SettingsMessageID)
	}
	if messageID != "" {
		if err := messenger.PatchCard(ctx, messageID, contents); err == nil {
			if messageID == cfg.SettingsMessageID {
				return nil
			}
			return state.Update(func(current *config.Config) {
				current.SettingsMessageID = messageID
			})
		}
	}

	messageID, err := messenger.CreateCard(ctx, cfg.ChatID, contents)
	if err != nil {
		return err
	}
	return state.Update(func(current *config.Config) {
		current.SettingsMessageID = messageID
	})
}

type overviewRefreshRequest struct {
	resend   bool
	response chan error
}

func requestOverviewRefresh(ctx context.Context, requests chan<- overviewRefreshRequest, resend bool) error {
	if requests == nil {
		return nil
	}
	response := make(chan error, 1)
	select {
	case requests <- overviewRefreshRequest{resend: resend, response: response}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isHelpCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/help", "help", "/设置", "设置", "配置":
		return true
	default:
		return false
	}
}

func applySettingsAction(state *configStore, action feishu.CardAction) (string, bool, error) {
	switch actionValue(action.Value, "action") {
	case "set_window":
		minutes, err := strconv.Atoi(actionValue(action.Value, "value"))
		if err != nil || !supportedWindow(minutes) {
			return "不支持的活跃窗口", false, nil
		}
		if err := state.Update(func(cfg *config.Config) {
			cfg.OverviewPreferences.ActiveWindowMinutes = minutes
		}); err != nil {
			return "", false, err
		}
		return "已更新活跃窗口", true, nil
	case "set_include_recent":
		includeRecent, err := strconv.ParseBool(actionValue(action.Value, "value"))
		if err != nil {
			return "不支持的显示范围", false, nil
		}
		if err := state.Update(func(cfg *config.Config) {
			cfg.OverviewPreferences.IncludeRecent = includeRecent
		}); err != nil {
			return "", false, err
		}
		return "已更新显示范围", true, nil
	case "refresh":
		state.Notify()
		return "正在重新发送总览", true, nil
	default:
		return "不支持的设置操作", false, nil
	}
}

func actionValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func supportedWindow(minutes int) bool {
	for _, candidate := range []int{1, 5, 15, 30, 60} {
		if minutes == candidate {
			return true
		}
	}
	return false
}

func overviewOptions(preferences config.OverviewPreferences) status.OverviewOptions {
	return status.OverviewOptions{
		ActiveWindow:       preferences.ActiveWindow(),
		IncludeRecent:      preferences.IncludeRecent,
		MaxVisibleSessions: preferences.MaxVisibleSessions,
	}
}

type trackedSession struct {
	snapshot        status.Snapshot
	sourceUpdatedAt time.Time
	cancel          context.CancelFunc
	watching        bool
}

type sessionUpdate struct {
	sessionID string
	event     map[string]any
	done      bool
	err       error
}

func monitorAll(ctx context.Context, state *configStore, executable string, scanInterval time.Duration, dryRun bool, messenger feishu.Messenger, once bool, overviewRefreshes <-chan overviewRefreshRequest) error {
	updates := make(chan sessionUpdate, 256)
	sessions := make(map[string]*trackedSession)

	publish := func(resend bool) error {
		cfg := state.Snapshot()
		snapshots := make([]status.Snapshot, 0, len(sessions))
		for _, session := range sessions {
			snapshots = append(snapshots, session.snapshot)
		}
		contents, err := status.OverviewCard(snapshots, overviewOptions(cfg.OverviewPreferences), time.Now())
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Println(contents)
			return nil
		}
		if resend {
			messageID, err := messenger.CreateCard(ctx, cfg.ChatID, contents)
			if err != nil {
				return err
			}
			return state.Update(func(current *config.Config) {
				current.OverviewMessageID = messageID
			})
		}

		messageID := cfg.OverviewMessageID
		if messageID == "" {
			messageID = reusableMessageID(cfg.MessageIDs)
			if messageID == "" {
				messageID, err = messenger.CreateCard(ctx, cfg.ChatID, contents)
				if err != nil {
					return err
				}
				if err := state.Update(func(current *config.Config) {
					current.OverviewMessageID = messageID
				}); err != nil {
					return err
				}
				return nil
			}
			if err := state.Update(func(current *config.Config) {
				current.OverviewMessageID = messageID
			}); err != nil {
				return err
			}
		}
		return messenger.PatchCard(ctx, messageID, contents)
	}

	discover := func() (bool, error) {
		listed, err := cxr.List(ctx, executable, allSessionScanCount)
		if err != nil {
			return false, err
		}
		now := time.Now()
		cutoff := now.Add(-state.Snapshot().OverviewPreferences.ActiveWindow())
		changed := false
		recent := make([]cxr.Session, 0, len(listed))
		for _, listedSession := range listed {
			if listedSession.UpdatedAt.IsZero() || listedSession.UpdatedAt.Before(cutoff) {
				continue
			}
			recent = append(recent, listedSession)
		}
		sort.Slice(recent, func(left, right int) bool {
			return recent[left].UpdatedAt.After(recent[right].UpdatedAt)
		})
		for _, listedSession := range recent {
			tracked, found := sessions[listedSession.ID]
			if !found {
				snapshot := status.New(listedSession.ID)
				snapshot.FirstUserPrompt = listedSession.FirstUserMessage
				snapshot.WorkingDirectory = listedSession.WorkingDirectory
				snapshot.UpdatedAt = listedSession.UpdatedAt
				tracked = &trackedSession{
					snapshot:        snapshot,
					sourceUpdatedAt: listedSession.UpdatedAt,
				}
				sessions[listedSession.ID] = tracked
				changed = true
				continue
			}
			if listedSession.UpdatedAt.After(tracked.sourceUpdatedAt) {
				tracked.sourceUpdatedAt = listedSession.UpdatedAt
				if !tracked.watching {
					startSessionWatch(ctx, executable, listedSession.ID, tracked, updates)
				}
				changed = true
			}
			if tracked.snapshot.FirstUserPrompt == "" && listedSession.FirstUserMessage != "" {
				tracked.snapshot.FirstUserPrompt = listedSession.FirstUserMessage
				changed = true
			}
			if listedSession.WorkingDirectory != "" && tracked.snapshot.WorkingDirectory != listedSession.WorkingDirectory {
				tracked.snapshot.WorkingDirectory = listedSession.WorkingDirectory
				changed = true
			}
		}
		desiredWatches := make(map[string]struct{}, maxConcurrentWatches)
		for index, listedSession := range recent {
			if index >= maxConcurrentWatches {
				break
			}
			desiredWatches[listedSession.ID] = struct{}{}
		}
		for sessionID, tracked := range sessions {
			if tracked.sourceUpdatedAt.Before(cutoff) {
				if tracked.cancel != nil {
					tracked.cancel()
				}
				delete(sessions, sessionID)
				changed = true
				continue
			}
			if _, desired := desiredWatches[sessionID]; !desired && tracked.watching && tracked.cancel != nil {
				tracked.cancel()
			}
		}
		for sessionID := range desiredWatches {
			tracked := sessions[sessionID]
			if tracked != nil && !tracked.watching {
				startSessionWatch(ctx, executable, sessionID, tracked, updates)
			}
		}
		return changed, nil
	}

	_, err := discover()
	dirty := true
	if err != nil {
		if once {
			return err
		}
		fmt.Fprintln(os.Stderr, "会话扫描失败：", err)
		dirty = true
	}
	if once {
		return publish(false)
	}
	if dirty {
		if err := publish(false); err != nil {
			fmt.Fprintln(os.Stderr, "总览卡更新失败：", err)
		}
	}

	scanTicker := time.NewTicker(scanInterval)
	publishTicker := time.NewTicker(minimumUpdateInterval)
	defer scanTicker.Stop()
	defer publishTicker.Stop()

	for {
		// A settings action should not wait behind a full stream of cxr events.
		select {
		case request := <-overviewRefreshes:
			err := publish(request.resend)
			if err == nil {
				dirty = false
			}
			request.response <- err
			continue
		default:
		}
		select {
		case update := <-updates:
			tracked, found := sessions[update.sessionID]
			if !found {
				continue
			}
			if update.event != nil {
				tracked.snapshot = tracked.snapshot.Apply(update.event)
				dirty = true
			}
			if update.done {
				tracked.watching = false
				tracked.cancel = nil
				if update.err != nil && ctx.Err() == nil && !cxr.IsContextCancellation(update.err) {
					fmt.Fprintf(os.Stderr, "会话 %s 监听停止：%v\n", update.sessionID, update.err)
				}
			}
		case <-scanTicker.C:
			changed, err := discover()
			if err != nil {
				fmt.Fprintln(os.Stderr, "会话扫描失败：", err)
			} else if changed {
				dirty = true
			}
		case <-state.changed:
			dirty = true
		case request := <-overviewRefreshes:
			err := publish(request.resend)
			if err == nil {
				dirty = false
			}
			request.response <- err
		case <-publishTicker.C:
			if dirty {
				if err := publish(false); err != nil {
					fmt.Fprintln(os.Stderr, "总览卡更新失败：", err)
					continue
				}
				dirty = false
			}
		case <-ctx.Done():
			for _, tracked := range sessions {
				if tracked.cancel != nil {
					tracked.cancel()
				}
			}
			return nil
		}
	}
}

func startSessionWatch(ctx context.Context, executable, sessionID string, tracked *trackedSession, updates chan<- sessionUpdate) {
	watchCtx, cancel := context.WithCancel(ctx)
	tracked.cancel = cancel
	tracked.watching = true
	go func() {
		err := cxr.Watch(watchCtx, executable, sessionID, func(event map[string]any) error {
			select {
			case updates <- sessionUpdate{sessionID: sessionID, event: event}:
				return nil
			case <-watchCtx.Done():
				return watchCtx.Err()
			}
		})
		select {
		case updates <- sessionUpdate{sessionID: sessionID, done: true, err: err}:
		case <-ctx.Done():
		}
	}()
}

func reusableMessageID(messageIDs map[string]string) string {
	ids := make([]string, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		if strings.TrimSpace(messageID) != "" {
			ids = append(ids, messageID)
		}
	}
	if len(ids) != 1 {
		return ""
	}
	return ids[0]
}
