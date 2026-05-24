# install.ps1 - install a locally-built StayAwake
#
# For development / installing from a clone. Build first, then run this:
#   go build -ldflags "-s -w" -o stayawake.exe .
#   powershell -ExecutionPolicy Bypass -File install.ps1
#
# End users don't need this - they use the one-liner (see README / bootstrap.ps1).
# No administrator rights required.

$ErrorActionPreference = "Stop"

$src = Join-Path $PSScriptRoot "stayawake.exe"
if (-not (Test-Path $src)) {
    throw "stayawake.exe not found. Build it first:  go build -ldflags `"-s -w`" -o stayawake.exe ."
}

$dir = Join-Path $env:LOCALAPPDATA "stayawake"
$exe = Join-Path $dir "stayawake.exe"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Copy-Item $src $exe -Force
Write-Host "Installed to $dir"

$p = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $p) { $p = "" }
$entries = $p.Split(";") | Where-Object { $_ -ne "" }
if ($entries -notcontains $dir) {
    [Environment]::SetEnvironmentVariable("Path", (($entries + $dir) -join ";"), "User")
    $env:Path += ";$dir"
    Write-Host "Added to PATH (open a new terminal to use 'stayawake')."
}

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

Write-Host "Done. Try:  stayawake on | off | default"
