# StayAwake

A small **local app** for Windows, made with Claude, that toggles the power
settings which let your PC go to sleep — from a web UI **or** a `stayawake`
command — and lets you safely switch everything back. It can also gently **jiggle
the mouse** to keep the PC looking active.

It ships as a **single ~7 MB `stayawake.exe`** — no Python, no runtime, nothing
to install. It only ever calls the built-in Windows `powercfg` tool, nothing
leaves your machine, and the web server listens on `127.0.0.1` only.

## Screenshots

| Default — your PC can still sleep | After “Prevent sleep” |
|:---:|:---:|
| ![StayAwake showing the default state where the PC can sleep](docs/ui-can-sleep.png) | ![StayAwake showing sleep prevented, all timeouts set to Never](docs/ui-prevented.png) |

## Requirements

- **Windows** (the app drives `powercfg`, so it doesn't run on macOS/Linux).
- Nothing else to run it — the `.exe` is self-contained.
- **Go** is only needed to *build from source* (not to use the app).

## Install

**One line** — downloads the prebuilt exe to `%LOCALAPPDATA%\stayawake`, adds
`stayawake` to your PATH, makes shortcuts, and launches the app:

```powershell
irm https://stayawa.ke | iex
```

> Prefer the raw URL: `irm https://raw.githubusercontent.com/vdiehl/dont-sleep/main/bootstrap.ps1 | iex`

**From source** — build it yourself, then install:

```powershell
go build -ldflags "-s -w" -o stayawake.exe .
powershell -ExecutionPolicy Bypass -File install.ps1
```

The installer launches the app for you. After that, open any **new** terminal to
use the `stayawake` command.

## The `stayawake` command

| Command               | Action                                                        |
|-----------------------|---------------------------------------------------------------|
| `stayawake`           | Open the web UI (starts the server if needed).                |
| `stayawake on`        | Prevent sleep now (apply keep-awake values to enabled items). |
| `stayawake off`       | Restore the **previous** values.                              |
| `stayawake default`   | Restore the saved **Windows default** values.                 |
| `stayawake status`    | Print the current settings as a table.                        |
| `stayawake stop`      | Stop the background web server (power settings untouched).     |
| `stayawake uninstall` | Restore defaults, remove PATH entry + shortcuts + install folder. |

`on` / `off` / `default` / `status` run without the server and share the exact
same logic as the UI, so changes are reflected in both.

## What it controls

Settings are split into **core** (changed by default) and **advanced** (opt-in,
off by default). Each can be toggled in the UI; “Prevent sleep” only changes the
ones that are enabled.

**Core** (no admin needed) — idle power-down timeouts, set to *Never*:

| Setting            | Effect                          |
|--------------------|---------------------------------|
| Sleep after        | Stops the system entering sleep |
| Hibernate after    | Stops hibernation               |
| Turn off display   | Keeps the display on            |
| Turn off hard disk | Keeps the disk spinning         |

**Advanced** (opt-in):

| Setting                           | Keep-awake value | Notes                                   |
|-----------------------------------|------------------|-----------------------------------------|
| Minimum processor state           | 100%             | CPU never down-clocks. More power/heat. |
| Maximum processor state           | 100%             | Removes any CPU frequency cap.          |
| Unattended sleep timeout          | Never            | **Hidden** Windows setting — needs admin. |
| Processor idle disable (C-states) | On               | **Hidden**, aggressive — CPU never idles; runs hotter. Needs admin. |

The two hidden settings are unhidden on demand (via `powercfg -attributes`),
which needs administrator rights. Enable one without admin and the UI shows a
clear warning and skips it.

## Mouse jiggler

An optional mode (off by default) that performs a tiny **net-zero** nudge — it
moves the cursor a few pixels and instantly moves it back — so the PC looks
active (screensaver, “available” presence). Because it returns to the exact spot,
there's no drift and it's consistent across monitors and DPI scales. It **pauses
when you move the mouse** and **resumes after** the mouse is idle for a
configurable time (default 60s; `0` = never auto-resume). Distance, interval, and
resume time are all adjustable in the UI.

The jiggler keeps the PC looking *active*; the `powercfg` settings are what
actually stop sleep. They're complementary — use either or both.

## How “safely switch back” works

State lives in `%APPDATA%\stayawake` (per-machine, not in the repo):

- **`defaults.json`** — the first non-prevented state ever seen, written once.
  Used by *Restore defaults* / `stayawake default`. Newly-revealed hidden
  settings are backfilled the first time they become visible.
- **`previous.json`** — the state captured right *before* the most recent
  “Prevent”. Used by *Restore previous* / `stayawake off`.
- **`config.json`** — which settings are enabled, plus jiggler settings.

Everything is stored and re-applied in powercfg's native units (seconds /
percent / 0-1), so restores are exact.

## Project layout

| Path                         | Role                                                  |
|------------------------------|-------------------------------------------------------|
| `main.go`                    | Entry point: HTTP server + embedded UI + `stayawake` CLI |
| `internal/powercfg`          | `powercfg` wrapper + settings catalog (locale-proof parser) |
| `internal/core`              | Config, snapshots, prevent/restore, status            |
| `internal/jiggler`           | Mouse-jiggler mode                                     |
| `static/`                    | The web UI, embedded into the exe via `go:embed`      |
| `bootstrap.ps1`              | One-line web installer (`irm … | iex`): download exe + install + launch |
| `install.ps1`                | Install a locally-built exe                            |
| `.github/workflows/release.yml` | Builds & publishes `stayawake.exe` on a `v*` tag   |
| `docs/`                      | GitHub Pages landing page + screenshots               |
| `cloudflare-worker.js`       | Routes `stayawa.ke` (script for PowerShell, site for browsers) |

## Uninstall

```powershell
stayawake uninstall
```

It restores your Windows **default** power settings first (so the PC isn't left
unable to sleep), stops the server, then removes the PATH entry and shortcuts.
If installed under `%LOCALAPPDATA%\stayawake`, it also deletes the install and
state folders; a dev checkout is left in place. Add `-y` to skip the prompt.

## Building & releasing

```powershell
go test ./...
go build -ldflags "-s -w" -o stayawake.exe .
```

Pushing a tag like `v1.0.0` triggers the GitHub Actions workflow, which builds
`stayawake.exe` and attaches it to a Release. `bootstrap.ps1` always downloads
the latest release asset.

## Notes / limitations

- Changes apply to the **currently active power plan**.
- Core timeout changes work **without admin**. The two hidden advanced settings
  need an elevated run to be unhidden.
- Modern-standby (S0) laptops may behave slightly differently from classic S3.

See [`CHOICES.md`](CHOICES.md) for the design decisions and alternatives.
