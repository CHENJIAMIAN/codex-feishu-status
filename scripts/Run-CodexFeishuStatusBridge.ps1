[CmdletBinding()]
param(
    [ValidateRange(5, 300)]
    [int]$RetryDelaySeconds = 15
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$startScript = Join-Path $PSScriptRoot 'Start-CodexFeishuStatus.ps1'
if (-not (Test-Path -LiteralPath $startScript)) {
    throw "缺少启动脚本：$startScript"
}

$pwsh = Join-Path $env:ProgramFiles 'PowerShell\7\pwsh.exe'
if (-not (Test-Path -LiteralPath $pwsh)) {
    $pwsh = (Get-Command pwsh.exe -ErrorAction Stop).Source
}

$arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{0}" -Mode watch-all' -f $startScript

while ($true) {
    try {
        $process = Start-Process -FilePath $pwsh -ArgumentList $arguments -WindowStyle Hidden -PassThru -Wait
        if ($process.ExitCode -eq 0) {
            exit 0
        }
        Write-Warning "状态桥接器异常退出，退出码：$($process.ExitCode)。$RetryDelaySeconds 秒后重试。"
    } catch {
        Write-Warning "状态桥接器启动失败：$($_.Exception.Message)。$RetryDelaySeconds 秒后重试。"
    }
    Start-Sleep -Seconds $RetryDelaySeconds
}
