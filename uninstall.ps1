# uninstall.ps1 - Don't Sleep
# Removes the `dontsleep` PATH entry and shortcuts, restores default power
# settings, and (for a real install under %LOCALAPPDATA%) deletes the folder.
#
# Run directly:   powershell -ExecutionPolicy Bypass -File uninstall.ps1
# Or via the CLI: dontsleep uninstall   (which runs this from a temp copy)
#
# Parameters:
#   -InstallDir <path>  Folder to remove (defaults to this script's folder).
#   -Purge              Also delete the install folder (only honored under %LOCALAPPDATA%).
#   -NoRestore          Skip restoring default power settings.

param(
    [string]$InstallDir = $PSScriptRoot,
    [switch]$Purge,
    [switch]$NoRestore
)

$ErrorActionPreference = "SilentlyContinue"
Write-Host "Don't Sleep - uninstall" -ForegroundColor Cyan

# Give the caller (e.g. `dontsleep uninstall`) a moment to exit first.
Start-Sleep -Milliseconds 1200

# 0) Restore Windows default power settings so the PC isn't left unable to sleep.
if (-not $NoRestore) {
    $py = (Get-Command python).Source
    if (-not $py) { $py = (Get-Command py).Source }
    $cli = Join-Path $InstallDir "cli.py"
    if ($py -and (Test-Path $cli)) {
        & $py $cli default | Out-Null
        Write-Host "Restored Windows default power settings."
    }
}

# 1) Stop a running server and wait for the port to free (its CWD is the folder).
try { Invoke-WebRequest -Uri "http://127.0.0.1:8765/api/quit" -Method POST -TimeoutSec 2 | Out-Null } catch {}
Start-Sleep -Seconds 1

# 2) Remove the install dir from the user PATH.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath) {
    $target = $InstallDir.TrimEnd('\')
    $kept = $userPath.Split(";") | Where-Object { $_ -ne "" -and $_.TrimEnd('\') -ne $target }
    [Environment]::SetEnvironmentVariable("Path", ($kept -join ";"), "User")
    Write-Host "Removed from user PATH."
}

# 3) Remove shortcuts.
@(
    (Join-Path ([Environment]::GetFolderPath("Desktop")) "Don't Sleep.lnk"),
    (Join-Path ([Environment]::GetFolderPath("Programs")) "Don't Sleep.lnk")
) | ForEach-Object {
    if (Test-Path $_) { Remove-Item $_ -Force; Write-Host "Removed shortcut: $_" }
}

# 4) Delete the install folder, but ONLY if it is a standard install location.
if ($Purge) {
    $local = $env:LOCALAPPDATA.TrimEnd('\')
    $isInstalled = $InstallDir.TrimEnd('\').ToLower() -eq (Join-Path $local "dontsleep").ToLower()
    if ($isInstalled) {
        Remove-Item -LiteralPath $InstallDir -Recurse -Force
        Write-Host "Deleted $InstallDir"
    } else {
        Write-Host "Left folder in place (not a standard install path): $InstallDir" -ForegroundColor Yellow
    }
} else {
    Write-Host "Left files in place: $InstallDir"
}

Write-Host ""
Write-Host "Uninstalled. Open a new terminal for the PATH change to take effect." -ForegroundColor Green
