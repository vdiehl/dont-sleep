"""
powercfg_manager.py
-------------------
Thin, stateless wrapper around the Windows `powercfg` CLI. It knows the set of
power settings the app can manage and how to read/write them; it does NOT know
about snapshots, config, or which settings are "enabled" (that lives in core.py).

Value semantics differ per setting and are described by `unit`:
  - "seconds"  : idle timeout in seconds. 0 = Never. Prevent target = 0.
  - "percent"  : processor state 0-100. Prevent target = 100 (full clock).
  - "bool"     : 0/1 flag. For idle-disable, 1 = CPU idle states disabled.

Some settings are "hidden": Windows omits them from `powercfg /query` output and
the Control Panel until their HIDE attribute is cleared (`-attributes`, needs
admin). Until then they read as unavailable.
"""

import ctypes
import re
import subprocess

# Subgroup GUIDs
SUB_SLEEP = "238c9fa8-0aad-41ed-83f4-97be242c8f20"
SUB_VIDEO = "7516b95f-f776-4464-8c53-06167f40cc99"
SUB_DISK = "0012ee47-9041-4b5d-9b77-535fba8b1442"
SUB_PROCESSOR = "54533251-82be-4824-96c1-47b60b740d00"

# Each managed setting. `prevent` is the value that keeps the machine awake.
SETTINGS = [
    # --- core: idle power-down timeouts (no admin needed) ---
    {"key": "standby",   "name": "Sleep after",            "category": "core",
     "sub": SUB_SLEEP, "guid": "29f6c1db-86da-48c5-9fdb-f2b67b1f44da",
     "unit": "seconds", "prevent": 0, "hidden": False,
     "note": "Lets the system enter sleep (S3/modern standby) when idle."},
    {"key": "hibernate", "name": "Hibernate after",        "category": "core",
     "sub": SUB_SLEEP, "guid": "9d7815a6-7ee4-497e-8888-515a05f02364",
     "unit": "seconds", "prevent": 0, "hidden": False,
     "note": "Lets the system hibernate when idle."},
    {"key": "monitor",   "name": "Turn off display after", "category": "core",
     "sub": SUB_VIDEO, "guid": "3c0bc021-c8a8-4e07-a973-6b14cbcb2b7e",
     "unit": "seconds", "prevent": 0, "hidden": False,
     "note": "Powers the display down when idle."},
    {"key": "disk",      "name": "Turn off hard disk after", "category": "core",
     "sub": SUB_DISK, "guid": "6738e2c4-e8a5-4a42-b16a-e040e769756e",
     "unit": "seconds", "prevent": 0, "hidden": False,
     "note": "Spins the disk down when idle."},

    # --- advanced: opt-in, off by default ---
    {"key": "proc_min",  "name": "Minimum processor state", "category": "advanced",
     "sub": SUB_PROCESSOR, "guid": "893dee8e-2bef-41e0-89c6-b55d0929964c",
     "unit": "percent", "prevent": 100, "hidden": False,
     "note": "Pin to 100% so the CPU never down-clocks. Higher power/heat."},
    {"key": "proc_max",  "name": "Maximum processor state", "category": "advanced",
     "sub": SUB_PROCESSOR, "guid": "bc5038f7-23e0-4960-96da-33abaf5935ec",
     "unit": "percent", "prevent": 100, "hidden": False,
     "note": "Allow the CPU to reach 100% (removes any frequency cap)."},
    {"key": "unattended", "name": "Unattended sleep timeout", "category": "advanced",
     "sub": SUB_SLEEP, "guid": "7bc4a2f9-d8fc-4469-b07b-33eb785aaca0",
     "unit": "seconds", "prevent": 0, "hidden": True,
     "note": "Sleep after the system wakes unattended (e.g. for a task). Hidden; needs admin to enable."},
    {"key": "idle_disable", "name": "Processor idle disable (C-states)", "category": "advanced",
     "sub": SUB_PROCESSOR, "guid": "5d76a2ca-e8c0-402f-a133-2158492d58ad",
     "unit": "bool", "prevent": 1, "hidden": True,
     "note": "Stops CPU cores entering low-power idle states. Aggressive: notably more power/heat. Hidden; needs admin."},
]

SETTINGS_BY_KEY = {s["key"]: s for s in SETTINGS}
CORE_KEYS = [s["key"] for s in SETTINGS if s["category"] == "core"]
ADVANCED_KEYS = [s["key"] for s in SETTINGS if s["category"] == "advanced"]

_NO_WINDOW = getattr(subprocess, "CREATE_NO_WINDOW", 0)
_HEX_LINE = re.compile(r"0x([0-9a-fA-F]+)")


class PowercfgError(Exception):
    """Raised when a powercfg invocation fails."""


def is_admin():
    """True if the current process is elevated (needed to unhide hidden settings)."""
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def _run(args, check=True):
    try:
        proc = subprocess.run(
            ["powercfg", *args],
            capture_output=True, text=True, creationflags=_NO_WINDOW,
        )
    except FileNotFoundError as exc:
        raise PowercfgError("powercfg.exe was not found. This app only runs on Windows.") from exc
    if check and proc.returncode != 0:
        msg = (proc.stderr or proc.stdout or "unknown error").strip()
        raise PowercfgError(f"powercfg {' '.join(args)} failed: {msg}")
    return proc


def query_setting(setting):
    """Return {"ac": int|None, "dc": int|None} for a setting. None => unavailable."""
    proc = _run(["/query", "SCHEME_CURRENT", setting["sub"], setting["guid"]], check=False)
    if proc.returncode != 0:
        return {"ac": None, "dc": None}
    ac = dc = None
    for line in proc.stdout.splitlines():
        m = _HEX_LINE.search(line)
        if not m:
            continue
        value = int(m.group(1), 16)
        if "AC" in line and "DC" not in line:
            ac = value
        elif "DC" in line:
            dc = value
    return {"ac": ac, "dc": dc}


def set_setting(setting, ac_value, dc_value):
    """Set a setting's AC and DC value on the active scheme (does not activate)."""
    _run(["/setacvalueindex", "SCHEME_CURRENT", setting["sub"], setting["guid"], str(int(ac_value))])
    _run(["/setdcvalueindex", "SCHEME_CURRENT", setting["sub"], setting["guid"], str(int(dc_value))])


def unhide_setting(setting):
    """Clear the HIDE attribute so a hidden setting becomes queryable. Needs admin."""
    if not setting.get("hidden"):
        return
    if not is_admin():
        raise PowercfgError(
            f"'{setting['name']}' is a hidden Windows setting and needs administrator "
            f"rights to enable. Re-run Don't Sleep as administrator."
        )
    _run(["-attributes", setting["sub"], setting["guid"], "-ATTRIB_HIDE"])


def apply_active():
    """Re-activate the current scheme so pending value changes take effect."""
    _run(["/setactive", "SCHEME_CURRENT"])


def read_all():
    """Raw current state of every known setting (no config/enabled logic)."""
    out = {}
    for s in SETTINGS:
        vals = query_setting(s)
        out[s["key"]] = {
            "ac": vals["ac"],
            "dc": vals["dc"],
            "available": vals["ac"] is not None or vals["dc"] is not None,
        }
    return out
