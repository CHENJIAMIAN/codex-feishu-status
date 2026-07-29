[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$NoStart
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$startScript = Join-Path $PSScriptRoot 'Start-CodexFeishuStatus.ps1'
$trayLauncher = Join-Path $PSScriptRoot 'Start-CodexFeishuStatusTray.vbs'
$binary = Join-Path $root 'dist\codex-feishu-status.exe'
$trayScript = Join-Path $PSScriptRoot 'CodexFeishuStatusTray.ps1'
$bridgeTaskName = 'Codex Feishu Status Bridge'
$trayTaskName = 'Codex Feishu Status Tray'
$taskNames = @($bridgeTaskName, $trayTaskName)

function Get-StatusTask {
    param([Parameter(Mandatory)][string]$Name)

    Get-ScheduledTask -TaskName $Name -ErrorAction SilentlyContinue
}

function Get-ManagedProcessRoots {
    param([Parameter(Mandatory)][string]$Name)

    switch ($Name) {
        'Codex Feishu Status Bridge' {
            Get-CimInstance Win32_Process | Where-Object {
                ($_.Name -eq 'pwsh.exe' -and $_.CommandLine -like '*Start-CodexFeishuStatus.ps1*' -and $_.CommandLine -like '*-Mode watch-all*') -or
                ($_.Name -eq 'codex-feishu-status.exe' -and $_.CommandLine -match [regex]::Escape($binary) -and $_.CommandLine -match '\bwatch-all\b')
            }
        }
        'Codex Feishu Status Tray' {
            Get-CimInstance Win32_Process | Where-Object {
                ($_.Name -eq 'wscript.exe' -and $_.CommandLine -like '*Start-CodexFeishuStatusTray.vbs*') -or
                ($_.Name -eq 'pwsh.exe' -and $_.CommandLine -match [regex]::Escape($trayScript))
            }
        }
        default {
            throw "未知的托管任务：$Name"
        }
    }
}

function Stop-ManagedProcessTrees {
    param([Parameter(Mandatory)][string]$Name)

    foreach ($process in @(Get-ManagedProcessRoots -Name $Name)) {
        & taskkill.exe /PID $process.ProcessId /T /F 2>$null | Out-Null
        if ($LASTEXITCODE -notin @(0, 128)) {
            throw "无法停止 $Name 的进程树，PID：$($process.ProcessId)"
        }
    }
}

function Remove-StatusTask {
    param([Parameter(Mandatory)][string]$Name)

    $task = Get-StatusTask -Name $Name
    if ($null -ne $task -and $task.State -eq 'Running') {
        Stop-ScheduledTask -TaskName $Name -ErrorAction Stop
    }
    if ($null -ne $task) {
        Unregister-ScheduledTask -TaskName $Name -Confirm:$false
    }
    Stop-ManagedProcessTrees -Name $Name

    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        Start-Sleep -Milliseconds 250
    } while (@(Get-ManagedProcessRoots -Name $Name).Count -gt 0 -and [DateTime]::UtcNow -lt $deadline)

    if (@(Get-ManagedProcessRoots -Name $Name).Count -gt 0) {
        throw "未能在限定时间内停止托管进程：$Name"
    }
}

function Start-StatusTask {
    param([Parameter(Mandatory)][string]$Name)

    $task = Get-StatusTask -Name $Name
    if ($null -eq $task) {
        throw "未找到计划任务：$Name"
    }
    if (@(Get-ManagedProcessRoots -Name $Name).Count -eq 0 -and $task.State -ne 'Running') {
        Start-ScheduledTask -TaskName $Name -ErrorAction Stop
    }
}

if ($Uninstall) {
    foreach ($taskName in $taskNames) {
        Remove-StatusTask -Name $taskName
    }
    Write-Host '已移除 Codex 飞书状态服务和托盘任务。'
    exit 0
}

foreach ($path in @($startScript, $trayLauncher)) {
    if (-not (Test-Path -LiteralPath $path)) {
        throw "缺少脚本：$path"
    }
}

$pwsh = Join-Path $env:ProgramFiles 'PowerShell\7\pwsh.exe'
if (-not (Test-Path -LiteralPath $pwsh)) {
    $pwsh = (Get-Command pwsh.exe -ErrorAction Stop).Source
}
$wscript = Join-Path $env:WINDIR 'System32\wscript.exe'
if (-not (Test-Path -LiteralPath $wscript)) {
    throw "未找到 wscript.exe：$wscript"
}

$currentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$principal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
$bridgeSettings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -MultipleInstances IgnoreNew `
    -RestartCount 999 `
    -RestartInterval (New-TimeSpan -Minutes 1)
$traySettings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -MultipleInstances IgnoreNew `
    -RestartCount 999 `
    -RestartInterval (New-TimeSpan -Minutes 1)

$bridgeArguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{0}" -Mode watch-all' -f $startScript
$trayArguments = '//B "{0}"' -f $trayLauncher
$bridgeAction = New-ScheduledTaskAction -Execute $pwsh -Argument $bridgeArguments -WorkingDirectory $root
$trayAction = New-ScheduledTaskAction -Execute $wscript -Argument $trayArguments -WorkingDirectory $root

foreach ($taskName in $taskNames) {
    Remove-StatusTask -Name $taskName
}

Register-ScheduledTask `
    -TaskName $bridgeTaskName `
    -Action $bridgeAction `
    -Trigger $trigger `
    -Settings $bridgeSettings `
    -Principal $principal `
    -Description '登录后常驻运行 Codex 飞书会话总览，并在异常退出后自动重启 / Run the Codex Feishu status bridge at logon.' `
    -Force | Out-Null
Register-ScheduledTask `
    -TaskName $trayTaskName `
    -Action $trayAction `
    -Trigger $trigger `
    -Settings $traySettings `
    -Principal $principal `
    -Description '登录后无窗口启动 Codex 飞书状态托盘 / Start the Codex Feishu status tray without a shell window at logon.' `
    -Force | Out-Null

if (-not $NoStart) {
    Start-StatusTask -Name $bridgeTaskName
    Start-StatusTask -Name $trayTaskName
}

Write-Host '已注册 Codex 飞书状态服务和托盘任务。'
Write-Host "服务任务：$bridgeTaskName"
Write-Host "托盘任务：$trayTaskName"
