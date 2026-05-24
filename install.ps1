# install.ps1 - Don't Sleep
# Sets up the `dontsleep` command and shortcuts. No administrator rights needed:
# it only edits the *user* PATH and creates *user* shortcuts.
#
# Run:  powershell -ExecutionPolicy Bypass -File install.ps1

$ErrorActionPreference = "Stop"
$base = $PSScriptRoot

# 1) Add this folder to the user PATH so `dontsleep` resolves anywhere.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $userPath) { $userPath = "" }
$entries = $userPath.Split(";") | Where-Object { $_ -ne "" }
if ($entries -notcontains $base) {
    $newPath = (($entries + $base) -join ";")
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "Added to user PATH: $base"
    Write-Host "  (open a NEW terminal for `dontsleep` to be available)"
} else {
    Write-Host "Already on user PATH: $base"
}

# 2) Resolve a windowless Python (pythonw) to launch the app without a console.
$python = (Get-Command python -ErrorAction SilentlyContinue).Source
if (-not $python) { $python = (Get-Command py -ErrorAction SilentlyContinue).Source }
$pythonw = $null
if ($python) {
    $candidate = Join-Path (Split-Path $python) "pythonw.exe"
    if (Test-Path $candidate) { $pythonw = $candidate }
}
if (-not $pythonw) { $pythonw = $python }

# 3) Create Start Menu + Desktop shortcuts that open the web UI.
$cli = Join-Path $base "cli.py"
$ws = New-Object -ComObject WScript.Shell
$targets = @(
    (Join-Path ([Environment]::GetFolderPath("Desktop")) "Don't Sleep.lnk"),
    (Join-Path ([Environment]::GetFolderPath("Programs")) "Don't Sleep.lnk")
)
foreach ($lnkPath in $targets) {
    $lnk = $ws.CreateShortcut($lnkPath)
    $lnk.TargetPath = $pythonw
    $lnk.Arguments = "`"$cli`""
    $lnk.WorkingDirectory = $base
    $lnk.Description = "Don't Sleep - control Windows sleep settings"
    $lnk.Save()
    Write-Host "Shortcut created: $lnkPath"
}

Write-Host ""
Write-Host "Done. Try:  dontsleep on   |   dontsleep off   |   dontsleep default"
