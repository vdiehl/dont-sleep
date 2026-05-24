# bootstrap.ps1 - one-line installer for Don't Sleep
#
#   irm https://raw.githubusercontent.com/vdiehl/dont-sleep/main/bootstrap.ps1 | iex
#
# It ensures Python is present (installing via winget if needed), downloads the
# app to %LOCALAPPDATA%\dontsleep, and runs install.ps1 to put `dontsleep` on
# PATH and create shortcuts. No administrator rights required.

$ErrorActionPreference = "Stop"

# ----- EDIT if your repo owner / name / branch differ -----------------------
$Owner  = "vdiehl"
$Repo   = "dont-sleep"
$Branch = "main"
# ----------------------------------------------------------------------------

$installDir = Join-Path $env:LOCALAPPDATA "dontsleep"

function Have($name) { [bool](Get-Command $name -ErrorAction SilentlyContinue) }
function Info($msg)  { Write-Host $msg -ForegroundColor Cyan }

Info "Don't Sleep - installer"

# 1) Ensure Python is available.
if (-not (Have python) -and -not (Have py)) {
    Info "Python not found - installing via winget..."
    if (-not (Have winget)) {
        throw "winget isn't available. Install Python 3 from https://python.org and re-run this installer."
    }
    winget install --id Python.Python.3.13 -e --source winget `
        --accept-package-agreements --accept-source-agreements --disable-interactivity
    # Refresh PATH for this session so the freshly installed python is found.
    $env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
                [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not (Have python) -and -not (Have py)) {
        throw "Python was installed but isn't on PATH yet. Open a NEW terminal and run the installer again."
    }
} else {
    Info "Python found."
}

# 2) Download the app as a zip and unpack it.
$zipUrl = "https://github.com/$Owner/$Repo/archive/refs/heads/$Branch.zip"
$tmp = Join-Path $env:TEMP ("dontsleep-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
$zip = Join-Path $tmp "app.zip"
Info "Downloading $zipUrl"
Invoke-WebRequest -Uri $zipUrl -OutFile $zip
Expand-Archive -Path $zip -DestinationPath $tmp -Force
$src = Get-ChildItem -Path $tmp -Directory | Select-Object -First 1   # e.g. dont-sleep-main
if (-not $src) { throw "Download looked empty - check the repo owner/name/branch in this script." }

# 3) Copy into a stable install dir. (The repo has no state/, so any existing
#    saved snapshots in $installDir survive an update.)
New-Item -ItemType Directory -Path $installDir -Force | Out-Null
Copy-Item -Path (Join-Path $src.FullName "*") -Destination $installDir -Recurse -Force
Remove-Item $tmp -Recurse -Force

# 4) Run the bundled installer (adds to PATH + makes shortcuts).
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $installDir "install.ps1")

Write-Host ""
Info "Installed to $installDir"
Write-Host "Open a NEW terminal, then run:  dontsleep"
