"""
dontsleep - command line interface
----------------------------------
Usage:
  dontsleep                 Open the web UI (starts the server if needed).
  dontsleep on              Prevent sleep now (apply keep-awake values).
  dontsleep off             Restore the previous values.
  dontsleep default         Restore the saved Windows default values.
  dontsleep status          Print the current settings.
  dontsleep stop            Stop the background web server (settings are untouched).
  dontsleep uninstall       Restore defaults, then remove PATH entry, shortcuts,
                            and the install folder. Add -y to skip the prompt.
  dontsleep help            Show this help.

`on` / `off` / `default` / `status` work without the server running. They use the
same logic as the web UI, so changes show up there too.
"""

import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
import webbrowser

import core
import powercfg_manager as pm

HOST = "127.0.0.1"
PORT = 8765
URL = f"http://{HOST}:{PORT}/"
BASE_DIR = os.path.dirname(os.path.abspath(__file__))
SERVER = os.path.join(BASE_DIR, "server.py")

# Windows process-creation flags to detach the background server.
_DETACHED = 0x00000008 | 0x00000200  # DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
_NEW_CONSOLE = 0x00000010  # CREATE_NEW_CONSOLE


def server_running():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(0.4)
        return s.connect_ex((HOST, PORT)) == 0


def start_server():
    py = sys.executable
    pyw = os.path.join(os.path.dirname(py), "pythonw.exe")
    exe = pyw if os.path.exists(pyw) else py
    subprocess.Popen(
        [exe, SERVER, "--no-browser"],
        cwd=BASE_DIR, creationflags=_DETACHED,
        stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        close_fds=True,
    )
    for _ in range(50):
        if server_running():
            return True
        time.sleep(0.1)
    return False


def fmt(unit, v):
    if v is None:
        return "n/a"
    if unit == "percent":
        return f"{v}%"
    if unit == "bool":
        return "On" if v else "Off"
    if v == 0:
        return "Never"
    if v % 3600 == 0:
        return f"{v // 3600}h"
    if v % 60 == 0:
        return f"{v // 60}m"
    return f"{v}s"


def print_status(s):
    state = "PREVENTED (awake)" if s["prevented"] else "CAN SLEEP"
    print(f"\nDon't Sleep: {state}\n")
    print(f"  {'setting':34} {'on':>4}  {'AC':>8} {'DC':>8}")
    print("  " + "-" * 58)
    for x in s["settings"]:
        on = "[x]" if x["enabled"] else "[ ]"
        if x["available"]:
            ac, dc = fmt(x["unit"], x["ac"]), fmt(x["unit"], x["dc"])
        else:
            ac = dc = "-"
        print(f"  {x['name']:34} {on:>4}  {ac:>8} {dc:>8}")
    print()


def cmd_open():
    if not server_running():
        print("Starting Don't Sleep server…")
        if not start_server():
            print("Could not start the server.", file=sys.stderr)
            return 1
    webbrowser.open(URL)
    print(f"Opened {URL}")
    return 0


def cmd_action(which):
    try:
        if which == "on":
            s = core.prevent()
            print("Sleep prevented.")
        elif which == "off":
            s = core.restore("previous")
            print("Restored previous values.")
        elif which == "default":
            s = core.restore("defaults")
            print("Restored default values.")
    except FileNotFoundError as exc:
        print(f"Nothing to restore: {exc}", file=sys.stderr)
        return 1
    except pm.PowercfgError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1
    print_status(s)
    return 0


def cmd_stop():
    if not server_running():
        print("Server is not running.")
        return 0
    try:
        urllib.request.urlopen(URL + "api/quit", data=b"", timeout=2)
    except Exception:
        pass
    print("Stopped the Don't Sleep server.")
    return 0


def cmd_uninstall(argv):
    assume_yes = "-y" in argv or "--yes" in argv
    if not assume_yes:
        try:
            ans = input(
                "Remove dontsleep (PATH entry, shortcuts, install folder) and "
                "restore default power settings? [y/N] "
            )
        except EOFError:
            ans = ""
        if ans.strip().lower() not in ("y", "yes"):
            print("Cancelled.")
            return 0

    ps = os.path.join(BASE_DIR, "uninstall.ps1")
    if not os.path.exists(ps):
        print("uninstall.ps1 not found next to cli.py.", file=sys.stderr)
        return 1

    # Run from a temp copy so the script can delete the install folder itself.
    tmp = os.path.join(tempfile.gettempdir(), "dontsleep-uninstall.ps1")
    shutil.copyfile(ps, tmp)
    args = ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", tmp,
            "-InstallDir", BASE_DIR]

    # Only delete the folder when this is a real install under %LOCALAPPDATA%.
    local = os.environ.get("LOCALAPPDATA", "")
    installed = local and os.path.normcase(os.path.abspath(BASE_DIR)) == \
        os.path.normcase(os.path.join(local, "dontsleep"))
    if installed:
        args.append("-Purge")

    subprocess.Popen(
        args, cwd=tempfile.gettempdir(), creationflags=_NEW_CONSOLE, close_fds=True,
    )
    print("Uninstalling in a new window: restoring defaults, removing PATH entry "
          "and shortcuts" + (", deleting the install folder." if installed else "."))
    return 0


def main(argv):
    cmd = (argv[0].lower() if argv else "open")
    if cmd in ("on", "off", "default"):
        return cmd_action(cmd)
    if cmd == "status":
        print_status(core.status())
        return 0
    if cmd == "stop":
        return cmd_stop()
    if cmd == "uninstall":
        return cmd_uninstall(argv[1:])
    if cmd in ("open", ""):
        return cmd_open()
    if cmd in ("help", "-h", "--help"):
        print(__doc__)
        return 0
    print(f"Unknown command: {cmd}\n", file=sys.stderr)
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
