"""
core.py
-------
Application logic shared by the web server and the `dontsleep` CLI:
  - which settings are "enabled" (state/config.json)
  - snapshots of defaults and previous values (state/defaults.json, previous.json)
  - the prevent / restore actions
  - a single status payload describing everything for the UI/CLI

Everything is kept in seconds/percent/bool exactly as powercfg reports it, so
restores are exact.
"""

import datetime
import json
import os

import powercfg_manager as pm

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
STATE_DIR = os.path.join(BASE_DIR, "state")
DEFAULTS_FILE = os.path.join(STATE_DIR, "defaults.json")
PREVIOUS_FILE = os.path.join(STATE_DIR, "previous.json")
CONFIG_FILE = os.path.join(STATE_DIR, "config.json")


# --------------------------------------------------------------------------- #
# Config: which settings "Prevent sleep" touches
# --------------------------------------------------------------------------- #
def default_config():
    return {"enabled": {s["key"]: (s["category"] == "core") for s in pm.SETTINGS}}


def load_config():
    cfg = default_config()
    if os.path.exists(CONFIG_FILE):
        try:
            with open(CONFIG_FILE, "r", encoding="utf-8") as fh:
                saved = json.load(fh)
            for k, v in (saved.get("enabled") or {}).items():
                if k in cfg["enabled"]:
                    cfg["enabled"][k] = bool(v)
        except (OSError, ValueError):
            pass
    return cfg


def save_config(cfg):
    os.makedirs(STATE_DIR, exist_ok=True)
    with open(CONFIG_FILE, "w", encoding="utf-8") as fh:
        json.dump(cfg, fh, indent=2)


def set_enabled(updates):
    """Merge {key: bool} into the enabled config and persist."""
    cfg = load_config()
    for k, v in updates.items():
        if k in cfg["enabled"]:
            cfg["enabled"][k] = bool(v)
    save_config(cfg)
    return cfg


def enabled_keys():
    cfg = load_config()
    return [k for k, on in cfg["enabled"].items() if on]


# --------------------------------------------------------------------------- #
# Snapshots
# --------------------------------------------------------------------------- #
def _now():
    return datetime.datetime.now().isoformat(timespec="seconds")


def _snapshot_from_raw(raw):
    return {k: {"ac": v["ac"], "dc": v["dc"]} for k, v in raw.items() if v["available"]}


def _save_snapshot(path, raw):
    os.makedirs(STATE_DIR, exist_ok=True)
    payload = {"capturedAt": _now(), "settings": _snapshot_from_raw(raw)}
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2)
    return payload


def _load_snapshot(path):
    if not os.path.exists(path):
        return None
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        return None


def _is_prevented_raw(raw, keys):
    """True if every enabled+available setting in `keys` sits at its prevent value."""
    considered = 0
    for key in keys:
        meta = pm.SETTINGS_BY_KEY.get(key)
        vals = raw.get(key)
        if not meta or not vals or not vals["available"]:
            continue
        present = [v for v in (vals["ac"], vals["dc"]) if v is not None]
        if not present:
            continue
        considered += 1
        if any(v != meta["prevent"] for v in present):
            return False
    return considered > 0


def _capture_defaults_if_needed(raw):
    """Record the first non-prevented state as defaults; backfill newly-seen settings."""
    existing = _load_snapshot(DEFAULTS_FILE)
    if existing is None:
        if not _is_prevented_raw(raw, enabled_keys()):
            _save_snapshot(DEFAULTS_FILE, raw)
        return
    # Backfill: if a setting only became visible later (e.g. unhidden), record its
    # current value as the default so "Restore defaults" can revert it too.
    changed = False
    for key, vals in _snapshot_from_raw(raw).items():
        if key not in existing["settings"]:
            existing["settings"][key] = vals
            changed = True
    if changed:
        with open(DEFAULTS_FILE, "w", encoding="utf-8") as fh:
            json.dump(existing, fh, indent=2)


# --------------------------------------------------------------------------- #
# Status
# --------------------------------------------------------------------------- #
def status():
    raw = pm.read_all()
    _capture_defaults_if_needed(raw)
    cfg = load_config()
    admin = pm.is_admin()

    settings = []
    for s in pm.SETTINGS:
        vals = raw[s["key"]]
        enabled = cfg["enabled"][s["key"]]
        settings.append({
            "key": s["key"],
            "name": s["name"],
            "category": s["category"],
            "unit": s["unit"],
            "note": s["note"],
            "preventValue": s["prevent"],
            "hidden": s["hidden"],
            "enabled": enabled,
            "available": vals["available"],
            "needsAdmin": bool(s["hidden"] and not vals["available"] and not admin),
            "ac": vals["ac"],
            "dc": vals["dc"],
        })

    defaults = _load_snapshot(DEFAULTS_FILE)
    previous = _load_snapshot(PREVIOUS_FILE)
    return {
        "settings": settings,
        "prevented": _is_prevented_raw(raw, enabled_keys()),
        "isAdmin": admin,
        "defaults": defaults,
        "previous": previous,
        "hasDefaults": defaults is not None,
        "hasPrevious": previous is not None,
    }


# --------------------------------------------------------------------------- #
# Actions
# --------------------------------------------------------------------------- #
def prevent():
    """Set every enabled setting to its prevent value, saving the prior state first."""
    keys = enabled_keys()

    # Unhide any enabled hidden settings so we can read + write them (needs admin).
    for key in keys:
        meta = pm.SETTINGS_BY_KEY[key]
        if meta["hidden"]:
            current = pm.query_setting(meta)
            if current["ac"] is None and current["dc"] is None:
                pm.unhide_setting(meta)  # raises PowercfgError if not admin

    raw = pm.read_all()
    _capture_defaults_if_needed(raw)
    if not _is_prevented_raw(raw, keys):
        _save_snapshot(PREVIOUS_FILE, raw)

    applied = []
    for key in keys:
        meta = pm.SETTINGS_BY_KEY[key]
        vals = raw[key]
        if not vals["available"]:
            continue
        pm.set_setting(meta, meta["prevent"], meta["prevent"])
        applied.append(key)
    if applied:
        pm.apply_active()
    return status()


def restore(which):
    """Re-apply a saved snapshot. `which` is 'previous' or 'defaults'."""
    path = PREVIOUS_FILE if which == "previous" else DEFAULTS_FILE
    snap = _load_snapshot(path)
    if not snap:
        raise FileNotFoundError(f"No saved {which} snapshot to restore.")
    applied = []
    for key, vals in snap["settings"].items():
        meta = pm.SETTINGS_BY_KEY.get(key)
        if not meta:
            continue
        ac = vals.get("ac")
        dc = vals.get("dc")
        if ac is None and dc is None:
            continue
        pm.set_setting(meta, ac if ac is not None else 0, dc if dc is not None else 0)
        applied.append(key)
    if applied:
        pm.apply_active()
    return status()
