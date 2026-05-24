# bootstrap.ps1 - StayAwake one-line installer
#
#   irm https://stayawa.ke | iex
#
# Downloads the prebuilt stayawake.exe (no Python or any runtime needed),
# installs it to %LOCALAPPDATA%\stayawake, adds it to PATH, makes shortcuts,
# and launches the app. No administrator rights required.

$ErrorActionPreference = "Stop"

# Ensure TLS 1.2 so the download works on older / locked-down PowerShell.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch {}

# ----- EDIT if your repo owner / name differ --------------------------------
$Owner = "vdiehl"
$Repo  = "dont-sleep"
# ----------------------------------------------------------------------------

$exeUrl = "https://github.com/$Owner/$Repo/releases/latest/download/stayawake.exe"
$dir    = Join-Path $env:LOCALAPPDATA "stayawake"
$exe    = Join-Path $dir "stayawake.exe"

Write-Host "Installing StayAwake..." -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path $dir | Out-Null

Write-Host "Downloading $exeUrl"
Invoke-WebRequest -Uri $exeUrl -OutFile $exe

# Add the install dir to the user PATH (and this session) so `stayawake` works.
$p = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $p) { $p = "" }
$entries = $p.Split(";") | Where-Object { $_ -ne "" }
if ($entries -notcontains $dir) {
    [Environment]::SetEnvironmentVariable("Path", (($entries + $dir) -join ";"), "User")
    $env:Path += ";$dir"
    Write-Host "Added to PATH."
}

# Desktop + Start Menu shortcuts.
$ws = New-Object -ComObject WScript.Shell
foreach ($lnk in @(
        (Join-Path ([Environment]::GetFolderPath("Desktop")) "StayAwake.lnk"),
        (Join-Path ([Environment]::GetFolderPath("Programs")) "StayAwake.lnk"))) {
    $s = $ws.CreateShortcut($lnk)
    $s.TargetPath = $exe
    $s.WorkingDirectory = $dir
    $s.Description = "StayAwake - keep your Windows PC awake"
    $s.Save()
}

Write-Host "Installed to $dir" -ForegroundColor Green
Write-Host "Launching StayAwake..."
Start-Process -FilePath $exe   # opens the web UI - no new terminal needed
Write-Host "Done. From any new terminal you can also run:  stayawake on | off | default"
