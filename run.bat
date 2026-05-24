@echo off
REM Don't Sleep - launcher
REM Double-click this file (or run it from a terminal) to start the local app.
cd /d "%~dp0"

REM Prefer the Python launcher, fall back to python on PATH.
where py >nul 2>nul
if %errorlevel%==0 (
  py server.py
) else (
  python server.py
)

pause
