param(
    [Parameter(Mandatory)]
    [ValidateSet('pair', 'watch', 'watch-all', 'status')]
    [string]$Mode,
    [string]$SessionId,
    [switch]$DryRun
)

$root = Split-Path -Parent $PSScriptRoot
$binary = Join-Path $root 'dist\codex-feishu-status.exe'

if (-not (Test-Path -LiteralPath $binary)) {
    throw "未找到 $binary。请先在项目根目录运行 go build -o .\dist\codex-feishu-status.exe .\cmd\codex-feishu-status"
}

$arguments = @($Mode)
if ($Mode -eq 'watch') {
    if ([string]::IsNullOrWhiteSpace($SessionId)) {
        throw 'watch 模式需要 -SessionId。'
    }
    $arguments += @('--session-id', $SessionId)
}
if ($DryRun) {
    $arguments += '--dry-run'
}

if ($Mode -eq 'status' -or $DryRun) {
    & $binary @arguments
    exit $LASTEXITCODE
}

$vaultRunner = Join-Path $env:USERPROFILE '.codex\scripts\Invoke-CodexSecretCommand.ps1'
if (Test-Path -LiteralPath $vaultRunner) {
    & $vaultRunner `
        -SecretName FEISHU_CODEX_STATUS_APP_SECRET `
        -FilePath $binary `
        -ArgumentList $arguments
} elseif (-not [string]::IsNullOrWhiteSpace($env:FEISHU_CODEX_STATUS_APP_SECRET)) {
    & $binary @arguments
} else {
    throw '请通过密钥管理器注入 FEISHU_CODEX_STATUS_APP_SECRET，或安装 Codex DPAPI 凭据包装器。'
}
exit $LASTEXITCODE
