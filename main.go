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

func pidPath() string   { return filepath.Join(core.StateDir(), "jiggler.pid") }
func statePath() string { return filepath.Join(core.StateDir(), "jiggler.state") }
func selfExe() string   { e, _ := os.Executable(); return e }

// applyJiggler starts or stops the standalone jiggler daemon to match config.
func applyJiggler() {
	if core.LoadConfig().Jiggler.Enabled {
		_ = jiggler.Start(pidPath(), selfExe())
	} else {
		jiggler.Stop(pidPath())
	}
}

// jigglerStatus is "off" (daemon not running) or "active"/"paused".
func jigglerStatus() string {
	if !jiggler.IsRunning(pidPath()) {
		return "off"
	}
	return jiggler.ReadState(statePath())
}

func jigglerLoad() (bool, jiggler.Config) {
	c := core.LoadConfig().Jiggler
	return c.Enabled, jiggler.Config{DistancePx: c.DistancePx, IntervalSec: c.IntervalSec, ResumeAfterSec: c.ResumeAfterSec}
}

//go:embed all:static
var staticFS embed.FS

const (
	host        = "127.0.0.1"
	defaultPort = 8765
)

func addrFor(p int) string { return fmt.Sprintf("%s:%d", host, p) }
func urlFor(p int) string  { return fmt.Sprintf("http://%s/", addrFor(p)) }

// The active port is written here by `serve` so `open`/`stop` can find it
// (needed because the port may be chosen via --port or auto-bumped if busy).
func portFile() string { return filepath.Join(core.StateDir(), "port") }

func writePortFile(p int) {
	_ = os.MkdirAll(core.StateDir(), 0o755)
	_ = os.WriteFile(portFile(), []byte(strconv.Itoa(p)), 0o644)
}

func readActivePort() (int, bool) {
	b, err := os.ReadFile(portFile())
	if err != nil {
		return 0, false
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return p, true
}

// parsePort pulls a `--port N` flag out of args, defaulting to def.
func parsePort(args []string, def int) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--port" {
			if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 && n < 65536 {
				return n
			}
		}
	}
	return def
}

// hasPortFlag reports whether the user explicitly passed a valid --port.
func hasPortFlag(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--port" {
			if _, err := strconv.Atoi(args[i+1]); err == nil {
				return true
			}
		}
	}
	return false
}

// listen binds 127.0.0.1, trying `tries` consecutive ports from `start`.
// tries == 1 means honor exactly that port (used for an explicit --port).
func listen(start, tries int) (net.Listener, int, error) {
	var lastErr error
	for p := start; p < start+tries && p < 65536; p++ {
		ln, err := net.Listen("tcp", addrFor(p))
		if err == nil {
			return ln, p, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("could not bind a port near %d: %v", start, lastErr)
}

func main() {
	// First non-flag token is the command; flags (e.g. --port) may follow or,
	// for the default "open", come first (`stayawake --port 9000`).
	args := os.Args[1:]
	cmd := "open"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = strings.ToLower(args[0])
		args = args[1:]
	}
	var code int
	switch cmd {
	case "serve":
		code = cmdServe(args)
	case "open", "":
		code = cmdOpen(args)
	case "on":
		code = cmdAction("on")
	case "off":
		code = cmdAction("off")
	case "default":
		code = cmdAction("default")
	case "status":
		printStatus(core.GetStatus())
	case "enable":
		code = cmdSetEnabled(args, true)
	case "disable":
		code = cmdSetEnabled(args, false)
	case "jiggle":
		code = cmdJiggle(args)
	case "__jiggle": // internal: the detached daemon entry point
		jiggler.RunDaemon(pidPath(), statePath(), jigglerLoad)
	case "stop":
		code = cmdStop()
	case "uninstall":
		code = cmdUninstall(args)
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
  stayawake enable <s>  Enable setting(s) for 'on' (no arg lists them; 'all' for all).
  stayawake disable <s> Disable setting(s) for 'on'.
  stayawake jiggle on   Start the mouse jiggler (flags: --distance N --interval N --resume N).
  stayawake jiggle off  Stop the mouse jiggler.
  stayawake stop        Stop the background web server.
  stayawake uninstall   Restore defaults and remove from PATH/shortcuts.

Flags:
  --port N              Web UI port. The default (8765) auto-bumps to the next
                        free port if busy; an explicit --port must be free or it
                        errors. Works with the default open and with 'serve'.
`

// ---- HTTP server ----------------------------------------------------------

func cmdServe(args []string) int {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		s := core.GetStatus()
		s.JigglerState = jigglerStatus()
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
		s.JigglerState = jigglerStatus()
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

	desired := parsePort(args, defaultPort)
	tries := 21 // default port: auto-bump to the next free one if busy
	if hasPortFlag(args) {
		tries = 1 // explicit --port: honor it exactly, or fail
	}
	ln, actual, err := listen(desired, tries)
	if err != nil {
		if tries == 1 {
			fmt.Fprintf(os.Stderr, "Port %d is already in use. Choose another with --port.\n", desired)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	writePortFile(actual)
	defer os.Remove(portFile())

	applyJiggler() // start the jiggler if it was left enabled
	fmt.Println("StayAwake serving at", urlFor(actual))
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
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

// serverRunning returns the active port if a server is reachable, else ok=false.
func serverRunning() (int, bool) {
	p, ok := readActivePort()
	if !ok {
		return 0, false
	}
	c, err := net.DialTimeout("tcp", addrFor(p), 400*time.Millisecond)
	if err != nil {
		return 0, false
	}
	_ = c.Close()
	return p, true
}

func startServerDetached(desiredPort int) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command(exe, "serve", "--port", strconv.Itoa(desiredPort))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	for i := 0; i < 50; i++ {
		if _, ok := serverRunning(); ok {
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

func cmdOpen(args []string) int {
	if p, ok := serverRunning(); ok {
		openBrowser(urlFor(p))
		fmt.Println("Opened", urlFor(p))
		return 0
	}
	fmt.Println("Starting StayAwake...")
	if !startServerDetached(parsePort(args, defaultPort)) {
		fmt.Fprintln(os.Stderr, "Could not start the server.")
		return 1
	}
	p, ok := serverRunning()
	if !ok {
		fmt.Fprintln(os.Stderr, "Server did not come up.")
		return 1
	}
	openBrowser(urlFor(p))
	fmt.Println("Opened", urlFor(p))
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

func cmdSetEnabled(args []string, value bool) int {
	verb := "enable"
	if !value {
		verb = "disable"
	}
	st := core.GetStatus()

	if len(args) == 0 {
		fmt.Printf("\nUsage: stayawake %s <setting> [<setting>...]   (or 'all')\n\n", verb)
		fmt.Println("Available settings (use the key):")
		for _, s := range st.Settings {
			mark := "[ ]"
			if s.Enabled {
				mark = "[x]"
			}
			fmt.Printf("  %s  %-13s %-34s (%s)\n", mark, s.Key, s.Name, s.Category)
		}
		fmt.Println()
		return 0
	}

	valid := map[string]bool{}
	for _, s := range st.Settings {
		valid[s.Key] = true
	}
	updates := map[string]bool{}
	for _, a := range args {
		k := strings.ToLower(a)
		if k == "all" {
			for _, s := range st.Settings {
				updates[s.Key] = value
			}
			continue
		}
		if !valid[k] {
			fmt.Fprintf(os.Stderr, "Unknown setting %q. Run 'stayawake %s' to see the list.\n", a, verb)
			return 2
		}
		updates[k] = value
	}
	if err := core.SetEnabled(updates); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	printStatus(core.GetStatus())
	return 0
}

func cmdJiggle(args []string) int {
	if len(args) == 0 {
		fmt.Println("Usage: stayawake jiggle on|off  [--distance N] [--interval N] [--resume N]")
		fmt.Println("Current jiggler:", jigglerStatus())
		return 0
	}
	switch strings.ToLower(args[0]) {
	case "on":
		cfg := core.LoadConfig().Jiggler
		cfg.Enabled = true
		for i := 1; i+1 < len(args); i += 2 {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				continue
			}
			switch args[i] {
			case "--distance":
				cfg.DistancePx = n
			case "--interval":
				cfg.IntervalSec = n
			case "--resume":
				cfg.ResumeAfterSec = n
			}
		}
		if err := core.SetJiggler(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := jiggler.Start(pidPath(), selfExe()); err != nil {
			fmt.Fprintln(os.Stderr, "could not start jiggler:", err)
			return 1
		}
		fmt.Println("Mouse jiggler: on")
	case "off":
		cfg := core.LoadConfig().Jiggler
		cfg.Enabled = false
		_ = core.SetJiggler(cfg)
		jiggler.Stop(pidPath())
		fmt.Println("Mouse jiggler: off")
	case "status":
		fmt.Println("Mouse jiggler:", jigglerStatus())
	default:
		fmt.Fprintf(os.Stderr, "Unknown: jiggle %s (use on|off)\n", args[0])
		return 2
	}
	return 0
}

func cmdStop() int {
	p, ok := serverRunning()
	if !ok {
		fmt.Println("Server is not running.")
		return 0
	}
	_, _ = http.Post(urlFor(p)+"api/quit", "application/json", bytes.NewReader([]byte("{}")))
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
	if p, ok := serverRunning(); ok {
		_, _ = http.Post(urlFor(p)+"api/quit", "application/json", bytes.NewReader([]byte("{}")))
		time.Sleep(500 * time.Millisecond)
	}
	jiggler.Stop(pidPath())

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
