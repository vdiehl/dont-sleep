// Package jiggler implements the "keep the mouse moving" keep-awake mode as a
// standalone, detached background process (a daemon) — independent of the web
// server, with no open port. It is controlled via a PID file:
//
//	Start  -> spawns `<exe> __jiggle` detached & windowless (if not already running)
//	Stop   -> terminates the daemon (taskkill) and clears the PID file
//	RunDaemon -> the daemon entry point: the jiggle loop itself
//
// Behaviour: while active it performs a small NET-ZERO nudge every IntervalSec
// (move by DistancePx, then back to the exact origin) — no drift, consistent
// across monitors/DPI. It pauses when the user moves the mouse (detected by
// comparing the cursor to where we last placed it) and resumes after the mouse
// is idle for ResumeAfterSec (0 = never auto-resume). It re-reads config every
// tick, so live changes apply within a second and disabling it makes the daemon
// exit on its own.
package jiggler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procSetCursorPos       = user32.NewProc("SetCursorPos")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	dpiOnce                sync.Once
)

func ensureDPIAware() { dpiOnce.Do(func() { procSetProcessDPIAware.Call() }) }

type point struct{ X, Y int32 }

func getCursor() (point, bool) {
	var p point
	r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p, r != 0
}

func setCursor(x, y int32) { procSetCursorPos.Call(uintptr(x), uintptr(y)) }

// Config holds the tunable jiggler parameters.
type Config struct {
	DistancePx     int
	IntervalSec    int
	ResumeAfterSec int // 0 = never auto-resume
}

type state int

const (
	stActive state = iota
	stPaused
)

// ---- pure decision step (unit-tested) -------------------------------------

type tickInput struct {
	Now         time.Time
	Cur         point
	LastSet     point
	St          state
	PausedSince time.Time
	LastNudge   time.Time
	Cfg         Config
}

type tickResult struct {
	St          state
	LastSet     point
	PausedSince time.Time
	LastNudge   time.Time
	Nudge       bool
}

func secs(n int) time.Duration { return time.Duration(n) * time.Second }

func decide(in tickInput) tickResult {
	r := tickResult{St: in.St, LastSet: in.LastSet, PausedSince: in.PausedSince, LastNudge: in.LastNudge}
	if in.Cur != in.LastSet { // the user moved the mouse
		r.St = stPaused
		r.PausedSince = in.Now
		r.LastSet = in.Cur
		return r
	}
	switch in.St {
	case stActive:
		if in.LastNudge.IsZero() || in.Now.Sub(in.LastNudge) >= secs(in.Cfg.IntervalSec) {
			r.Nudge = true
			r.LastNudge = in.Now
		}
	case stPaused:
		if in.Cfg.ResumeAfterSec > 0 && in.Now.Sub(in.PausedSince) >= secs(in.Cfg.ResumeAfterSec) {
			r.St = stActive
			r.Nudge = true
			r.LastNudge = in.Now
		}
	}
	return r
}

// ---- daemon lifecycle (PID-file managed) ----------------------------------

func writePID(path string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func readPID(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	const queryLimitedInfo = 0x1000
	h, err := syscall.OpenProcess(queryLimitedInfo, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(h)
	return true
}

// IsRunning reports whether the jiggler daemon is currently running.
func IsRunning(pidPath string) bool {
	pid, ok := readPID(pidPath)
	return ok && processAlive(pid)
}

// Start spawns the daemon detached & windowless if it isn't already running.
func Start(pidPath, exe string) error {
	if IsRunning(pidPath) {
		return nil
	}
	c := exec.Command(exe, "__jiggle")
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return c.Start()
}

// Stop terminates the daemon and clears the PID file.
func Stop(pidPath string) {
	if pid, ok := readPID(pidPath); ok {
		c := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = c.Run()
	}
	_ = os.Remove(pidPath)
}

func writeState(path string, s state) {
	v := "active"
	if s == stPaused {
		v = "paused"
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(v), 0o644)
}

// ReadState returns "active" or "paused" (defaults to "active" if unknown).
func ReadState(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "active"
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}
	return "active"
}

// RunDaemon is the daemon entry point. It writes the PID file, then loops once a
// second, re-reading (enabled, cfg) via load. It exits when load reports
// disabled. Blocks until then.
func RunDaemon(pidPath, statePath string, load func() (bool, Config)) {
	writePID(pidPath)
	defer os.Remove(pidPath)
	defer os.Remove(statePath)
	ensureDPIAware()

	t := time.NewTicker(time.Second)
	defer t.Stop()

	var lastSet point
	haveLast := false
	st := stActive
	pausedSince := time.Now()
	var lastNudge time.Time
	writeState(statePath, stActive)
	prevWritten := stActive

	for range t.C {
		enabled, cfg := load()
		if !enabled {
			return
		}
		cur, ok := getCursor()
		if !ok {
			continue
		}
		if !haveLast {
			lastSet = cur
			haveLast = true
			continue
		}
		res := decide(tickInput{
			Now: time.Now(), Cur: cur, LastSet: lastSet, St: st,
			PausedSince: pausedSince, LastNudge: lastNudge, Cfg: cfg,
		})
		st, lastSet, pausedSince, lastNudge = res.St, res.LastSet, res.PausedSince, res.LastNudge
		if st != prevWritten {
			writeState(statePath, st)
			prevWritten = st
		}
		if res.Nudge {
			origin := cur
			d := int32(cfg.DistancePx)
			setCursor(origin.X+d, origin.Y)
			time.Sleep(40 * time.Millisecond)
			setCursor(origin.X, origin.Y) // net-zero: back to origin
			// Re-read the ACTUAL resting position: on scaled displays SetCursorPos
			// can land a pixel off due to DPI rounding, and tracking the real value
			// avoids mistaking that rounding for a user move on the next tick.
			if p, ok := getCursor(); ok {
				lastSet = p
			} else {
				lastSet = origin
			}
		}
	}
}
