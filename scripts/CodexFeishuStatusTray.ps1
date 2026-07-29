Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()

$root = Split-Path -Parent $PSScriptRoot
$binary = Join-Path $root 'dist\codex-feishu-status.exe'
$bridgeSupervisor = Join-Path $PSScriptRoot 'Run-CodexFeishuStatusBridge.ps1'
$startScript = Join-Path $PSScriptRoot 'Start-CodexFeishuStatus.ps1'
$bridgeTaskName = 'Codex Feishu Status Bridge'

$mutexCreated = $false
$mutex = New-Object System.Threading.Mutex($true, 'CodexFeishuStatus.Tray', [ref]$mutexCreated)
if (-not $mutexCreated) {
    exit 0
}

function Get-BridgeTask {
    Get-ScheduledTask -TaskName $bridgeTaskName -ErrorAction SilentlyContinue
}

function Get-BridgeProcess {
    $binaryPattern = [regex]::Escape($binary)
    Get-CimInstance Win32_Process | Where-Object {
        $_.Name -eq 'codex-feishu-status.exe' -and $_.CommandLine -match $binaryPattern -and $_.CommandLine -match '\bwatch-all\b'
    } | Select-Object -First 1
}

function Get-BridgeProcessRoots {
    $binaryPattern = [regex]::Escape($binary)
    Get-CimInstance Win32_Process | Where-Object {
        ($_.Name -eq 'pwsh.exe' -and $_.CommandLine -like "*-File `"$bridgeSupervisor`"*") -or
        ($_.Name -eq 'pwsh.exe' -and $_.CommandLine -like "*-File `"$startScript`" -Mode watch-all*") -or
        ($_.Name -eq 'codex-feishu-status.exe' -and $_.CommandLine -match $binaryPattern -and $_.CommandLine -match '\bwatch-all\b')
    }
}

function Wait-BridgeStopped {
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $task = Get-BridgeTask
        $processRoots = @(Get-BridgeProcessRoots)
        if (($null -eq $task -or $task.State -ne 'Running') -and $processRoots.Count -eq 0) {
            return
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)

    throw '状态服务没有在限定时间内停止。'
}

function Start-Bridge {
    $task = Get-BridgeTask
    if ($null -eq $task) {
        throw '未安装状态服务。请先运行 Install-CodexFeishuStatus.ps1。'
    }
    if ($task.State -eq 'Disabled') {
        Enable-ScheduledTask -TaskName $bridgeTaskName -ErrorAction Stop
        $task = Get-BridgeTask
    }
    if ($null -eq (Get-BridgeProcess)) {
        if ($task.State -eq 'Running' -or @(Get-BridgeProcessRoots).Count -gt 0) {
            Stop-Bridge -DisableTask
        }
        Enable-ScheduledTask -TaskName $bridgeTaskName -ErrorAction Stop
        Start-ScheduledTask -TaskName $bridgeTaskName -ErrorAction Stop
    }
}

function Stop-Bridge {
    param([switch]$DisableTask)

    $task = Get-BridgeTask
    if ($DisableTask -and $null -ne $task) {
        Disable-ScheduledTask -TaskName $bridgeTaskName -ErrorAction Stop
    }
    if ($null -ne $task -and $task.State -eq 'Running') {
        Stop-ScheduledTask -TaskName $bridgeTaskName -ErrorAction Stop
    }
    foreach ($processRoot in @(Get-BridgeProcessRoots)) {
        & taskkill.exe /PID $processRoot.ProcessId /T /F 2>$null | Out-Null
        if ($null -ne (Get-CimInstance Win32_Process -Filter "ProcessId = $($processRoot.ProcessId)" -ErrorAction SilentlyContinue)) {
            throw "无法停止状态服务进程树，PID：$($processRoot.ProcessId)"
        }
    }
    Wait-BridgeStopped
}

function Restart-Bridge {
    Stop-Bridge -DisableTask
    Start-Bridge
}

function Get-CodexIcon {
    $codexBin = Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\bin'
    $candidate = Get-ChildItem -Path $codexBin -Filter 'codex.exe' -Recurse -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1
    if ($null -ne $candidate) {
        try {
            return [System.Drawing.Icon]::ExtractAssociatedIcon($candidate.FullName)
        } catch {
        }
    }
    return [System.Drawing.Icon]([System.Drawing.SystemIcons]::Application.Clone())
}

$icon = Get-CodexIcon
$notifyIcon = New-Object System.Windows.Forms.NotifyIcon
$notifyIcon.Icon = $icon
$notifyIcon.Text = 'Codex 飞书状态 / Codex Feishu Status'
$notifyIcon.Visible = $true
$appContext = New-Object System.Windows.Forms.ApplicationContext

$menu = New-Object System.Windows.Forms.ContextMenuStrip
$statusItem = $menu.Items.Add('状态：检查中 / Status: Checking')
$statusItem.Enabled = $false
[void]$menu.Items.Add('-')
$refreshItem = $menu.Items.Add('刷新总览 / Refresh overview')
$startItem = $menu.Items.Add('启动服务 / Start service')
$stopItem = $menu.Items.Add('停止服务 / Stop service')
[void]$menu.Items.Add('-')
$openProjectItem = $menu.Items.Add('打开项目 / Open project')
$exitItem = $menu.Items.Add('退出托盘 / Exit tray')
$notifyIcon.ContextMenuStrip = $menu

function Update-TrayState {
    $running = $null -ne (Get-BridgeProcess)
    if ($running) {
        $statusItem.Text = '状态：运行中 / Status: Running'
        $notifyIcon.Text = 'Codex 飞书状态：运行中 / Running'
    } else {
        $statusItem.Text = '状态：已停止 / Status: Stopped'
        $notifyIcon.Text = 'Codex 飞书状态：已停止 / Stopped'
    }
    $startItem.Enabled = -not $running
    $stopItem.Enabled = $running
    $refreshItem.Enabled = $running
}

function Show-TrayError {
    param([Parameter(Mandatory)][string]$Message)

    $notifyIcon.ShowBalloonTip(
        5000,
        'Codex 飞书状态 / Codex Feishu Status',
        $Message,
        [System.Windows.Forms.ToolTipIcon]::Error
    )
}

$refreshItem.add_Click({
    try {
        Restart-Bridge
        Update-TrayState
        $notifyIcon.ShowBalloonTip(3000, 'Codex 飞书状态 / Codex Feishu Status', '已重新连接，正在更新总览卡 / Refreshing overview.', [System.Windows.Forms.ToolTipIcon]::Info)
    } catch {
        Show-TrayError -Message $_.Exception.Message
    }
})
$startItem.add_Click({
    try {
        Start-Bridge
        Update-TrayState
    } catch {
        Show-TrayError -Message $_.Exception.Message
    }
})
$stopItem.add_Click({
    try {
        Stop-Bridge -DisableTask
        Update-TrayState
    } catch {
        Show-TrayError -Message $_.Exception.Message
    }
})
$openProjectItem.add_Click({ Start-Process -FilePath 'explorer.exe' -ArgumentList $root })
$exitItem.add_Click({
    $statusTimer.Stop()
    $notifyIcon.Visible = $false
    $appContext.ExitThread()
})

$statusTimer = New-Object System.Windows.Forms.Timer
$statusTimer.Interval = 5000
$statusTimer.add_Tick({ Update-TrayState })

try {
    Update-TrayState
    $statusTimer.Start()
    $notifyIcon.ShowBalloonTip(3000, 'Codex 飞书状态 / Codex Feishu Status', '托盘已启动 / Tray is ready.', [System.Windows.Forms.ToolTipIcon]::Info)
    [System.Windows.Forms.Application]::Run($appContext)
} finally {
    $statusTimer.Stop()
    $notifyIcon.Visible = $false
    $notifyIcon.Dispose()
    $icon.Dispose()
    $mutex.ReleaseMutex() | Out-Null
    $mutex.Dispose()
}
