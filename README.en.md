[中文](./README.md)

# Codex Feishu Live Status

<!-- codex-github-rules:bilingual-summary -->
> **English summary**: Sync live local Codex session status, first prompts, and working directories to Feishu cards.

---

This local status bridge collects active Codex sessions into a live-updating Feishu card. It lets you see what each session is doing, whether it is still running, and its working directory without repeatedly switching back to terminal tabs.

> It only observes Codex session events provided by cxr and sends Feishu cards. It does not control Codex, run tasks, or modify your code.

## What It Does

- watch-all discovers recent active main-agent sessions and shows status, current activity, duration, first user prompt, and working directory on one overview card.
- Separators keep multiple sessions readable; multiline text renders as real line breaks.
- Send /help to the bot to choose a 1, 5, 15, 30, or 60 minute activity window and switch between running-only and recent activity.
- Updating a setting or choosing Refresh Now posts a new overview card and makes it the target for subsequent live updates.
- watch compatibility mode follows one fixed session id for a long-running task.

## Information Boundary

Sent to Feishu: short session id, status, current activity or tool name, duration, last activity, first user prompt, and working directory.

Never sent: later prompts, model replies, tool arguments, command output, file contents, environment variables, keys, or App Secret. First prompts and working directories can still contain business information, so bind the bot only to a trusted direct message or group.

Local chat ids, Feishu message ids, logs, and build artifacts are ignored by Git.

## Prerequisites

- Windows 10/11 and PowerShell 7. The provided startup scripts target Windows; Go code keeps cross-platform cxr invocation logic.
- Go 1.25 or later.
- A working cxr command for which cxr --list-sessions --count 1 --json can read local Codex sessions.
- A Feishu custom app with bot capability, an App ID, and an App Secret.

## Configure the Feishu App

In the Feishu developer console, enable the bot and long connection, subscribe to im.message.receive_v1 and card.action.trigger, grant the IM permissions needed to create and update cards and receive messages, publish the app version, and add the bot to the target direct message or group.

Do not write the App Secret into the repository, data/config.json, or a .env file.

## Quick Start

Build after cloning:

~~~powershell
go build -o .\dist\codex-feishu-status.exe .\cmd\codex-feishu-status
~~~

Use a secret manager to inject FEISHU_CODEX_STATUS_APP_SECRET only into the launch process. In the Codex Windows environment, the shared DPAPI vault can be used:

~~~powershell
& "$env:USERPROFILE\.codex\scripts\Set-CodexSecret.ps1" FEISHU_CODEX_STATUS_APP_SECRET
~~~

Initialize with your own Feishu App ID:

~~~powershell
.\dist\codex-feishu-status.exe init --app-id <your-feishu-app-id>
~~~

Start pairing. The terminal prints a one-time verification code:

~~~powershell
.\scripts\Start-CodexFeishuStatus.ps1 -Mode pair
~~~

Send Bind <verification-code> in the Feishu direct message or group that should receive status cards. Then start the persistent overview:

~~~powershell
.\scripts\Start-CodexFeishuStatus.ps1 -Mode watch-all
~~~

Without the Codex DPAPI wrapper, the startup script uses FEISHU_CODEX_STATUS_APP_SECRET already present in the process; it fails clearly when the variable is absent.

## Persistent Service and Tray

Run this installation script once to register two current-user logon tasks. The overview bridge retries every 15 seconds after an unexpected exit; Task Scheduler also provides restart fallback. The tray uses wscript.exe to start a PowerShell STA process without a console window, leaving only a notification-area icon.

~~~powershell
.\scripts\Install-CodexFeishuStatus.ps1
~~~

The tray menu provides bilingual status, overview refresh, service start/stop, and project-directory actions. Refresh restarts the bridge, reconnects Feishu, and updates the existing overview card. App Secret remains in the DPAPI-backed launch flow and is never stored in a scheduled task.

Rerun the install script after changing binaries or scripts. Remove persistent tasks with:

~~~powershell
.\scripts\Install-CodexFeishuStatus.ps1 -Uninstall
~~~

## Card Settings and Limits

Send /help, Settings, or Configure in a bound chat to open the settings card. Settings apply to that chat only and immediately save and repost the overview.

By default, the service scans the latest 20 sessions every 5 seconds, creates cxr --watch --id listeners for at most the newest 8 sessions, and shows at most 16 sessions. These limits avoid excessive child processes; settings refresh is prioritized over stream processing.

## Single-Session Mode

~~~powershell
.\scripts\Start-CodexFeishuStatus.ps1 -Mode watch -SessionId <session-uuid>
~~~

watch invokes cxr --watch --all-events --ndjson --no-clipboard and throttles Feishu updates to once per second. Do not run watch and watch-all for the same session because they update different cards.

Inspect card JSON without sending it:

~~~powershell
.\scripts\Start-CodexFeishuStatus.ps1 -Mode watch -SessionId <session-uuid> -DryRun
.\dist\codex-feishu-status.exe watch-all --dry-run --once
~~~

## Command Reference

| Command | Purpose |
| --- | --- |
| init --app-id <id> | Save local app configuration, never App Secret |
| pair | Bind one Feishu chat with a one-time code |
| watch-all | Discover and show multiple recently active sessions |
| watch --session-id <id> | Follow one fixed session |
| status | Show local binding state without secrets |

## Verification

~~~powershell
go test ./...
go vet ./...
go build -o .\dist\codex-feishu-status.exe .\cmd\codex-feishu-status
~~~
