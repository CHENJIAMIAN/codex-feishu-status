package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/CHENJIAMIAN/codex-feishu-status/internal/config"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/cxr"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/feishu"
	"github.com/CHENJIAMIAN/codex-feishu-status/internal/status"
)

const minimumUpdateInterval = time.Second

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "pair":
		return runPair(args[1:])
	case "watch":
		return runWatch(args[1:])
	case "watch-all":
		return runWatchAll(args[1:])
	case "status":
		return runStatus(args[1:])
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func runInit(args []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", config.DefaultStateDir(), "状态文件目录")
	appID := flags.String("app-id", "", "飞书 App ID")
	secretName := flags.String("secret-name", config.DefaultSecretName, "DPAPI 凭据名称")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*stateDir)
	if err != nil {
		return err
	}
	if configuredAppID := strings.TrimSpace(*appID); configuredAppID != "" {
		cfg.AppID = configuredAppID
	}
	if strings.TrimSpace(cfg.AppID) == "" {
		return errors.New("init 需要 --app-id <你的飞书 App ID>")
	}
	cfg.SecretName = strings.TrimSpace(*secretName)
	if err := config.Save(*stateDir, cfg); err != nil {
		return err
	}
	fmt.Printf("已初始化：%s\n", config.FilePath(*stateDir))
	return nil
}

func runPair(args []string) error {
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", config.DefaultStateDir(), "状态文件目录")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*stateDir)
	if err != nil {
		return err
	}
	secret, err := readSecret(cfg)
	if err != nil {
		return err
	}
	code, err := pairingCode()
	if err != nil {
		return err
	}

	fmt.Printf("请在要接收状态卡的飞书私聊或群聊中，向机器人发送：绑定 %s\n", code)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	chatID, err := feishu.WaitForBinding(ctx, cfg.AppID, secret, code)
	if err != nil {
		return err
	}
	cfg.ChatID = chatID
	if err := config.Save(*stateDir, cfg); err != nil {
		return err
	}

	messenger, err := feishu.NewMessenger(cfg.AppID, secret)
	if err != nil {
		return err
	}
	if err := messenger.SendText(ctx, chatID, "Codex 实时状态已绑定。之后启动 watch-all 时会在这里更新会话总览；发送 /help 可打开设置卡。"); err != nil {
		return err
	}
	fmt.Println("绑定完成。")
	return nil
}

func runWatch(args []string) error {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", config.DefaultStateDir(), "状态文件目录")
	sessionID := flags.String("session-id", "", "要跟随的固定 Codex 会话 ID")
	cxrExecutable := flags.String("cxr", "", "cxr 可执行文件、.ps1 或 .js 路径")
	dryRun := flags.Bool("dry-run", false, "只输出卡片 JSON，不调用飞书")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*sessionID) == "" {
		return errors.New("watch 需要 --session-id；固定会话可避免多开 Codex 时跟错任务")
	}

	cfg, err := config.Load(*stateDir)
	if err != nil {
		return err
	}
	if !*dryRun && strings.TrimSpace(cfg.ChatID) == "" {
		return errors.New("尚未绑定飞书会话，请先运行 pair")
	}

	var messenger feishu.Messenger
	if !*dryRun {
		secret, err := readSecret(cfg)
		if err != nil {
			return err
		}
		messenger, err = feishu.NewMessenger(cfg.AppID, secret)
		if err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return monitor(ctx, cfg, *stateDir, *sessionID, *cxrExecutable, *dryRun, messenger)
}

func runStatus(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", config.DefaultStateDir(), "状态文件目录")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*stateDir)
	if err != nil {
		return err
	}
	fmt.Printf("配置：%s\n", config.FilePath(*stateDir))
	fmt.Printf("App ID：%s\n", cfg.AppID)
	fmt.Printf("已绑定：%t\n", cfg.ChatID != "")
	fmt.Printf("状态卡：%d 个会话\n", len(cfg.MessageIDs))
	fmt.Printf("总览卡：%t\n", cfg.OverviewMessageID != "")
	return nil
}

func monitor(ctx context.Context, cfg config.Config, stateDir, sessionID, executable string, dryRun bool, messenger feishu.Messenger) error {
	events := make(chan map[string]any, 32)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- cxr.Watch(ctx, executable, sessionID, func(event map[string]any) error {
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		close(events)
	}()

	snapshot := status.New(sessionID)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastPublished time.Time
	pending := false
	eventsOpen := true

	publish := func() error {
		contents, err := snapshot.Card(time.Now())
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Println(contents)
			return nil
		}

		messageID := cfg.MessageIDs[sessionID]
		if messageID == "" {
			messageID, err = messenger.CreateCard(ctx, cfg.ChatID, contents)
			if err != nil {
				return err
			}
			cfg.MessageIDs[sessionID] = messageID
			if err := config.Save(stateDir, cfg); err != nil {
				return err
			}
			return nil
		}
		return messenger.PatchCard(ctx, messageID, contents)
	}

	for eventsOpen {
		select {
		case event, ok := <-events:
			if !ok {
				eventsOpen = false
				continue
			}
			snapshot = snapshot.Apply(event)
			pending = true
			if lastPublished.IsZero() || time.Since(lastPublished) >= minimumUpdateInterval {
				if err := publish(); err != nil {
					return err
				}
				lastPublished = time.Now()
				pending = false
			}
		case <-ticker.C:
			if pending && time.Since(lastPublished) >= minimumUpdateInterval {
				if err := publish(); err != nil {
					return err
				}
				lastPublished = time.Now()
				pending = false
			}
		case <-ctx.Done():
			return nil
		}
	}

	watchErr := <-watchDone
	if pending && ctx.Err() == nil {
		if wait := minimumUpdateInterval - time.Since(lastPublished); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil
			}
		}
		if err := publish(); err != nil {
			return err
		}
	}
	if ctx.Err() != nil || cxr.IsContextCancellation(watchErr) {
		return nil
	}
	return watchErr
}

func readSecret(cfg config.Config) (string, error) {
	secret := strings.TrimSpace(os.Getenv(cfg.SecretName))
	if secret != "" {
		return secret, nil
	}
	return "", fmt.Errorf("未注入 %s；请通过 Invoke-CodexSecretCommand.ps1 启动本程序", cfg.SecretName)
}

func pairingCode() (string, error) {
	bytes := make([]byte, 4)
	if _, err := cryptorand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	return strings.ToUpper(hex.EncodeToString(bytes)), nil
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, "Codex 飞书实时状态桥接")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "命令：")
	fmt.Fprintln(output, "  init    初始化本地状态配置")
	fmt.Fprintln(output, "  pair    通过飞书机器人消息绑定私聊或群聊")
	fmt.Fprintln(output, "  watch   跟随一个固定 Codex 会话并更新状态卡")
	fmt.Fprintln(output, "  watch-all 发现活跃 Codex 会话并更新飞书总览与设置卡")
	fmt.Fprintln(output, "  status  查看本地配置状态")
}
