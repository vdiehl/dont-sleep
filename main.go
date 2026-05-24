// Command stayawake keeps a Windows PC awake by toggling the powercfg settings
// that allow sleep. Single self-contained binary: it serves a local web UI and
// provides a CLI (stayawake on|off|default|status|stop), with no runtime to
// install.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"stayawake/internal/core"
	"stayawake/internal/jiggler"
)

// jig is the mouse-jiggler manager; it only runs inside the serve process.
var jig = jiggler.New()

func applyJiggler() {
	c := core.LoadConfig().Jiggler
	jig.Apply(c.Enabled, jiggler.Config{
		DistancePx: c.DistancePx, IntervalSec: c.IntervalSec, ResumeAfterSec: c.ResumeAfterSec,
	})
}

//go:embed all:static
var staticFS embed.FS

const (
	host = "127.0.0.1"
	port = 8765
)

func addr() string { return fmt.Sprintf("%s:%d", host, port) }
func url() string  { return fmt.Sprintf("http://%s/", addr()) }

func main() {
	cmd := "open"
	if len(os.Args) > 1 {
		cmd = strings.ToLower(os.Args[1])
	}
	var code int
	switch cmd {
	case "serve":
		code = cmdServe()
	case "open", "":
		code = cmdOpen()
	case "on":
		code = cmdAction("on")
	case "off":
		code = cmdAction("off")
	case "default":
		code = cmdAction("default")
	case "status":
		printStatus(core.GetStatus())
	case "stop":
		code = cmdStop()
	case "uninstall":
		code = cmdUninstall(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		fmt.Print(usage)
		code = 2
	}
	os.Exit(code)
}

const usage = `stayawake - keep your Windows PC awake

Usage:
  stayawake             Open the web UI (starts the server if needed).
  stayawake on          Prevent sleep now.
  stayawake off         Restore the previous values.
  stayawake default     Restore the saved Windows default values.
  stayawake status      Print the current settings.
  stayawake stop        Stop the background web server.
  stayawake uninstall   Restore defaults and remove from PATH/shortcuts.
`

// ---- HTTP server ----------------------------------------------------------

func cmdServe() int {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mux := http.NewServeMux()
	srv := &http.Server{Addr: addr(), Handler: mux}

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		s := core.GetStatus()
		s.JigglerState = jig.State()
		writeJSON(w, s, nil)
	})
	mux.HandleFunc("/api/prevent", func(w http.ResponseWriter, r *http.Request) {
		s, err := core.Prevent()
		writeJSON(w, s, err)
	})
	mux.HandleFunc("/api/restore-previous", func(w http.ResponseWriter, r *http.Request) {
		s, err := core.Restore("previous")
		writeJSON(w, s, err)
	})
	mux.HandleFunc("/api/restore-defaults", func(w http.ResponseWriter, r *http.Request) {
		s, err := core.Restore("defaults")
		writeJSON(w, s, err)
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled map[string]bool     `json:"enabled"`
			Jiggler *core.JigglerConfig `json:"jiggler"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		if body.Enabled != nil {
			if err := core.SetEnabled(body.Enabled); err != nil {
				writeJSON(w, core.Status{}, err)
				return
			}
		}
		if body.Jiggler != nil {
			if err := core.SetJiggler(*body.Jiggler); err != nil {
				writeJSON(w, core.Status{}, err)
				return
			}
		}
		applyJiggler()
		s := core.GetStatus()
		s.JigglerState = jig.State()
		writeJSON(w, s, nil)
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true}, nil)
		go func() {
			time.Sleep(150 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}()
	})
	mux.Handle("/", http.FileServer(http.FS(sub)))

	applyJiggler() // start the jiggler if it was left enabled
	fmt.Println("StayAwake serving at", url())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func writeJSON(w http.ResponseWriter, payload any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no saved") {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// ---- CLI ------------------------------------------------------------------

func serverRunning() bool {
	c, err := net.DialTimeout("tcp", addr(), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func startServerDetached() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command(exe, "serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	for i := 0; i < 50; i++ {
		if serverRunning() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func openBrowser(u string) {
	cmd := exec.Command("cmd", "/c", "start", "", u)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

func cmdOpen() int {
	if !serverRunning() {
		fmt.Println("Starting StayAwake...")
		if !startServerDetached() {
			fmt.Fprintln(os.Stderr, "Could not start the server.")
			return 1
		}
	}
	openBrowser(url())
	fmt.Println("Opened", url())
	return 0
}

func cmdAction(which string) int {
	var (
		s   core.Status
		err error
	)
	switch which {
	case "on":
		s, err = core.Prevent()
		if err == nil {
			fmt.Println("Sleep prevented.")
		}
	case "off":
		s, err = core.Restore("previous")
		if err == nil {
			fmt.Println("Restored previous values.")
		}
	case "default":
		s, err = core.Restore("defaults")
		if err == nil {
			fmt.Println("Restored default values.")
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	printStatus(s)
	return 0
}

func cmdStop() int {
	if !serverRunning() {
		fmt.Println("Server is not running.")
		return 0
	}
	_, _ = http.Post(url()+"api/quit", "application/json", bytes.NewReader([]byte("{}")))
	fmt.Println("Stopped the StayAwake server.")
	return 0
}

func fmtValue(unit string, v *int) string {
	if v == nil {
		return "-"
	}
	n := *v
	switch unit {
	case "percent":
		return strconv.Itoa(n) + "%"
	case "bool":
		if n != 0 {
			return "On"
		}
		return "Off"
	default: // seconds
		switch {
		case n == 0:
			return "Never"
		case n%3600 == 0:
			return strconv.Itoa(n/3600) + "h"
		case n%60 == 0:
			return strconv.Itoa(n/60) + "m"
		default:
			return strconv.Itoa(n) + "s"
		}
	}
}

func printStatus(s core.Status) {
	state := "CAN SLEEP"
	if s.Prevented {
		state = "PREVENTED (awake)"
	}
	fmt.Printf("\nStayAwake: %s\n\n", state)
	fmt.Printf("  %-34s %4s  %8s %8s\n", "setting", "on", "Plugged", "Battery")
	fmt.Println("  " + strings.Repeat("-", 58))
	for _, x := range s.Settings {
		on := "[ ]"
		if x.Enabled {
			on = "[x]"
		}
		ac, dc := "-", "-"
		if x.Available {
			ac = fmtValue(x.Unit, x.AC)
			dc = fmtValue(x.Unit, x.DC)
		}
		fmt.Printf("  %-34s %4s  %8s %8s\n", x.Name, on, ac, dc)
	}
	fmt.Println()
}

func cmdUninstall(args []string) int {
	assumeYes := false
	for _, a := range args {
		if a == "-y" || a == "--yes" {
			assumeYes = true
		}
	}
	if !assumeYes {
		fmt.Print("Remove StayAwake (restore default power settings, then remove the " +
			"PATH entry, shortcuts and install folder)? [y/N] ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("Cancelled.")
			return 0
		}
	}

	// Restore defaults first so the PC isn't left unable to sleep.
	if _, err := core.Restore("defaults"); err != nil {
		fmt.Println("Note: could not restore defaults:", err)
	} else {
		fmt.Println("Restored Windows default power settings.")
	}

	// Stop a running background server (its CWD may be the install folder).
	if serverRunning() {
		_, _ = http.Post(url()+"api/quit", "application/json", bytes.NewReader([]byte("{}")))
		time.Sleep(500 * time.Millisecond)
	}

	exe, _ := os.Executable()
	installDir := filepath.Dir(exe)
	stateDir := core.StateDir()
	local := os.Getenv("LOCALAPPDATA")
	// Only delete the folder if it's the standard install location (never a dev checkout).
	deleteDirs := local != "" && strings.EqualFold(filepath.Clean(installDir), filepath.Clean(filepath.Join(local, "stayawake")))

	tmp := filepath.Join(os.TempDir(), "stayawake-uninstall.ps1")
	if err := os.WriteFile(tmp, []byte(buildUninstallPS(installDir, stateDir, deleteDirs)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
		return 1
	}
	c := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", tmp)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010} // CREATE_NEW_CONSOLE
	if err := c.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
		return 1
	}
	fmt.Println("Finishing cleanup in a new window...")
	return 0
}

// buildUninstallPS returns a PowerShell script (run from %TEMP%) that removes the
// PATH entry and shortcuts and, for a standard install, deletes the install and
// state folders after this process exits. Paths are emitted as single-quoted PS
// literals so backslashes are preserved verbatim.
func buildUninstallPS(installDir, stateDir string, deleteDirs bool) string {
	del := ""
	if deleteDirs {
		del = fmt.Sprintf("Start-Sleep -Seconds 2\nRemove-Item -Recurse -Force -ErrorAction SilentlyContinue '%s','%s'\n", installDir, stateDir)
	}
	return fmt.Sprintf(`$ErrorActionPreference = "SilentlyContinue"
Write-Host "StayAwake - uninstall"
$dir = '%s'
$p = [Environment]::GetEnvironmentVariable("Path","User")
if ($p) {
  $kept = $p.Split(";") | Where-Object { $_ -ne "" -and $_.TrimEnd('\') -ne $dir.TrimEnd('\') }
  [Environment]::SetEnvironmentVariable("Path", ($kept -join ";"), "User")
  Write-Host "Removed from PATH."
}
@(
  (Join-Path ([Environment]::GetFolderPath("Desktop")) "StayAwake.lnk"),
  (Join-Path ([Environment]::GetFolderPath("Programs")) "StayAwake.lnk")
) | ForEach-Object { if (Test-Path $_) { Remove-Item $_ -Force } }
%sWrite-Host "Done. Open a new terminal for the PATH change to take effect."
`, installDir, del)
}
