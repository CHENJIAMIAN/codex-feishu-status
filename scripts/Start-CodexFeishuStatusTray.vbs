Option Explicit

Dim shell, fileSystem, scriptPath, pwshPath, command, quote
Set shell = CreateObject("WScript.Shell")
Set fileSystem = CreateObject("Scripting.FileSystemObject")

scriptPath = fileSystem.BuildPath(fileSystem.GetParentFolderName(WScript.ScriptFullName), "CodexFeishuStatusTray.ps1")
pwshPath = shell.ExpandEnvironmentStrings("%ProgramFiles%") & "\PowerShell\7\pwsh.exe"
If Not fileSystem.FileExists(pwshPath) Then
  pwshPath = "pwsh.exe"
End If

quote = Chr(34)
command = quote & pwshPath & quote & " -NoLogo -NoProfile -STA -ExecutionPolicy Bypass -File " & quote & scriptPath & quote
WScript.Quit shell.Run(command, 0, True)
