[English](./README.en.md)

# Codex 飞书实时状态

<!-- codex-github-rules:bilingual-summary -->
> **中文简介**：将本机 Codex 会话的实时状态、首个请求和工作目录同步到飞书卡片

> **English summary**: Sync live local Codex session status, first prompts, and working directories to Feishu cards

---

把本机正在运行的 Codex 会话汇总成一张可实时更新的飞书卡片。无需反复切回终端，就能在飞书里判断每个会话正在做什么、是否仍在运行，以及它工作在哪个目录。

> 这是一个本地状态桥接器：它观察 `cxr` 提供的 Codex 会话事件并发送飞书卡片，不会控制 Codex、执行任务或改动你的代码。

## 能做什么

- `watch-all` 自动发现近期活跃的 Codex 主会话，在同一张总览卡中显示状态、当前活动、运行时长、首个用户请求和工作目录。
- 每条会话之间使用分割线，避免多会话时信息混在一起；文本会作为正常换行渲染，不会显示成字面量 `\n`。
- 向机器人发送 `/help` 可打开设置卡，选择 1、5、15、30 或 60 分钟活跃窗口，并切换“仅进行中”或“包含近期活动”。
- 在设置卡中修改选项或点击“立即刷新”后，会重新发送一张总览卡，并将它作为后续实时更新的目标。
- `watch` 兼容模式可固定跟随某一个会话 ID，适合只关注一项长任务。

## 它适合解决什么问题

Codex 同时开多个任务时，终端标签页很难一眼看出每个会话的用途和进度。这个工具把任务的第一句请求、所在目录和当前工具活动放在飞书中，适合在等待构建、测试、长时间命令或多任务并行时快速巡查。

## 信息边界

会发送到飞书：会话短 ID、状态、当前活动/工具名称、运行时长、最后活动时间、首个用户请求、工作目录。

不会发送到飞书：后续提示词、模型回复、工具参数、命令输出、文件内容、环境变量、密钥和 App Secret。首个请求与工作目录本身可能包含业务信息，请只绑定到你信任的飞书私聊或群聊。

本地的 Chat ID、飞书消息 ID、运行日志和构建产物均在 `.gitignore` 中排除，不会提交到仓库。

## 前提条件

- Windows 10/11 和 PowerShell 7。项目提供的启动脚本面向 Windows；Go 代码本身保留了跨平台的 `cxr` 调用逻辑。
- Go 1.25 或更高版本。
- 可用的 `cxr` 命令，且 `cxr --list-sessions --count 1 --json` 能读取本机 Codex 会话。
- 一个已创建机器人能力的飞书自建应用，以及该应用自己的 App ID 和 App Secret。

## 配置飞书应用

在飞书开发者后台完成以下配置，然后发布应用版本并把机器人加入目标私聊或群聊：

1. 为自建应用启用机器人能力和长连接。
2. 订阅事件 `im.message.receive_v1` 与 `card.action.trigger`。
3. 授予机器人创建和更新消息卡片、接收消息事件所需的 IM 权限。飞书控制台中的权限名称会随租户版本略有不同，以控制台对消息创建、消息更新和接收消息的提示为准。
4. 不要把 App Secret 写入仓库、`data/config.json` 或 `.env` 文件。

## 快速开始

克隆后构建：

```powershell
go build -o .\dist\codex-feishu-status.exe .\cmd\codex-feishu-status
```

通过你的密钥管理器仅为启动进程注入 `FEISHU_CODEX_STATUS_APP_SECRET`。在 Codex Windows 环境中可使用统一 DPAPI 凭据库：

```powershell
& "$env:USERPROFILE\.codex\scripts\Set-CodexSecret.ps1" FEISHU_CODEX_STATUS_APP_SECRET
```

初始化时明确提供你自己的飞书 App ID：

```powershell
.\dist\codex-feishu-status.exe init --app-id <你的飞书 App ID>
```

启动绑定，终端会给出一次性验证码：

```powershell
.\scripts\Start-CodexFeishuStatus.ps1 -Mode pair
```

在想接收状态卡的飞书私聊或群聊中发送 `绑定 <验证码>`。绑定成功后，常驻运行总览：

```powershell
.\scripts\Start-CodexFeishuStatus.ps1 -Mode watch-all
```

如果本机没有 Codex DPAPI 凭据包装器，启动脚本会使用当前进程已注入的 `FEISHU_CODEX_STATUS_APP_SECRET`；没有该变量时会明确报错。其他系统可直接运行二进制，并由自己的进程密钥管理方案注入同名环境变量。

## 常驻运行与托盘

在 Windows 上运行下面的安装脚本一次，即可注册当前用户的两个登录任务：总览桥接器由监护脚本在异常退出后每 15 秒重试，计划任务也保留失败重启兜底；托盘则通过 `wscript.exe` 无窗口启动 PowerShell STA 进程，只保留通知区域图标。

```powershell
.\scripts\Install-CodexFeishuStatus.ps1
```

托盘菜单提供双语状态、刷新总览、启动/停止服务和打开项目目录。点击“刷新总览”会重启桥接器并重新连接飞书，随后更新现有总览卡。任务仍通过 `Start-CodexFeishuStatus.ps1` 使用 DPAPI 凭据包装器，不会把 App Secret 存进计划任务。

更新二进制或脚本后，再运行一次安装脚本即可更新任务定义并重新启动服务。移除常驻服务和托盘任务：

```powershell
.\scripts\Install-CodexFeishuStatus.ps1 -Uninstall
```

## 卡片设置与性能边界

在已绑定聊天中发送 `/help`、`设置` 或 `配置` 即可打开设置卡。设置仅作用于该聊天，卡片操作会立即保存并重新发送总览。

默认每 5 秒扫描最近更新的 20 个会话，最多同时为最新的 8 个会话建立 `cxr --watch --id <session-id>` 监听，总览默认显示最多 16 条。这些上限避免多会话时创建过多子进程；设置卡的刷新请求优先于事件流处理。

## 单会话模式

需要只关注一个长期任务时，可用固定会话 ID：

```powershell
.\scripts\Start-CodexFeishuStatus.ps1 `
  -Mode watch `
  -SessionId <会话 UUID>
```

`watch` 会调用 `cxr --watch --all-events --ndjson --no-clipboard`，并将飞书卡片更新节流到最多每秒一次。不要同时让 `watch` 和 `watch-all` 跟随同一个会话，否则它们会竞争更新不同卡片。

可以先检查不发送飞书的卡片 JSON：

```powershell
.\scripts\Start-CodexFeishuStatus.ps1 `
  -Mode watch `
  -SessionId <会话 UUID> `
  -DryRun

.\dist\codex-feishu-status.exe watch-all --dry-run --once
```

## 命令速查

| 命令 | 用途 |
| --- | --- |
| `init --app-id <id>` | 保存本地应用配置，不保存 App Secret |
| `pair` | 用一次性验证码绑定一个飞书聊天 |
| `watch-all` | 自动发现并展示近期活跃的多个会话 |
| `watch --session-id <id>` | 固定跟随一个会话 |
| `status` | 查看本地配置是否已绑定，不输出密钥 |

## 验证

```powershell
go test ./...
go vet ./...
go build -o .\dist\codex-feishu-status.exe .\cmd\codex-feishu-status
```
