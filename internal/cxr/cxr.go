package cxr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxEventLineSize = 32 * 1024 * 1024

// Session is the metadata used to label an observed Codex session in Feishu.
type Session struct {
	ID               string    `json:"id"`
	UpdatedAt        time.Time `json:"updatedAt"`
	FirstUserMessage string    `json:"firstUserMessage"`
	WorkingDirectory string    `json:"workingDirectory"`
}

func List(ctx context.Context, executable string, count int) ([]Session, error) {
	if count <= 0 {
		count = 100
	}
	command, err := newCommand(ctx, executable, []string{
		"--list-sessions",
		"--count", strconv.Itoa(count),
		"--json",
	})
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &output
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start cxr session list: %w", err)
	}
	stopTermination := terminateWhenCanceled(ctx, command)
	defer stopTermination()
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("list cxr sessions: %s", message)
	}
	return parseSessions(output.Bytes())
}

func parseSessions(output []byte) ([]Session, error) {
	var sessions []Session
	if err := json.Unmarshal(output, &sessions); err != nil {
		return nil, fmt.Errorf("parse cxr session list: %w", err)
	}
	filtered := sessions[:0]
	for _, session := range sessions {
		if strings.TrimSpace(session.ID) != "" {
			filtered = append(filtered, session)
		}
	}
	return filtered, nil
}

func WatchArgs(sessionID string) []string {
	args := []string{
		"--watch",
		"--count", "1",
		"--all-events",
		"--ndjson",
		"--no-clipboard",
	}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, "--id", sessionID)
	}
	return args
}

func Watch(ctx context.Context, executable, sessionID string, onEvent func(map[string]any) error) error {
	command, err := newCommand(ctx, executable, WatchArgs(sessionID))
	if err != nil {
		return err
	}

	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create cxr stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr

	if err := command.Start(); err != nil {
		return fmt.Errorf("start cxr: %w", err)
	}
	stopTermination := terminateWhenCanceled(ctx, command)
	defer stopTermination()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxEventLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			killProcessTree(command)
			_ = command.Wait()
			return fmt.Errorf("cxr emitted invalid NDJSON: %w", err)
		}
		if err := onEvent(event); err != nil {
			killProcessTree(command)
			_ = command.Wait()
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		killProcessTree(command)
		_ = command.Wait()
		return fmt.Errorf("read cxr output: %w", err)
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("cxr stopped: %s", message)
	}
	return nil
}

// terminateWhenCanceled prevents pwsh's cxr shim from leaving its Node child
// behind when the bridge is restarted or a session leaves the active window.
func terminateWhenCanceled(ctx context.Context, command *exec.Cmd) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessTree(command)
		case <-done:
		}
	}()
	return func() { close(done) }
}

func killProcessTree(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill.exe", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = command.Process.Kill()
}

func newCommand(ctx context.Context, executable string, args []string) (*exec.Cmd, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(executable)
	if command == "" && runtime.GOOS == "windows" {
		npmShim := filepath.Join(os.Getenv("APPDATA"), "npm", "cxr.ps1")
		if _, err := os.Stat(npmShim); err == nil {
			return exec.Command("pwsh", append([]string{
				"-NoLogo", "-NoProfile", "-NonInteractive", "-File", npmShim,
			}, args...)...), nil
		}
	}
	if command == "" {
		command = "cxr"
	}

	switch strings.ToLower(filepath.Ext(command)) {
	case ".ps1":
		return exec.Command("pwsh", append([]string{
			"-NoLogo", "-NoProfile", "-NonInteractive", "-File", command,
		}, args...)...), nil
	case ".js", ".mjs":
		return exec.Command("node", append([]string{command}, args...)...), nil
	default:
		return exec.Command(command, args...), nil
	}
}

func IsContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
