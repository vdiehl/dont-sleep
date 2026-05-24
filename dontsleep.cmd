@echo off
REM dontsleep CLI shim. Put this folder on PATH (run install.ps1) so you can
REM type: dontsleep | dontsleep on | dontsleep off | dontsleep default
where py >nul 2>nul
if %errorlevel%==0 (
  py "%~dp0cli.py" %*
) else (
  python "%~dp0cli.py" %*
)
