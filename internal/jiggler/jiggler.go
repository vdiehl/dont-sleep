// Package jiggler implements the "keep the mouse moving" keep-awake mode.
//
// Behaviour (as specified): while active it performs a small NET-ZERO nudge
// every IntervalSec — it moves the cursor by DistancePx and immediately moves it
// back to the exact original spot. Net-zero means there's no drift and the
// cursor always ends where it was, so it's consistent across monitors and DPI
// scales (a fixed physical-pixel hop that's instantly undone). When the user
// moves the mouse it pauses; after the mouse is idle for ResumeAfterSec it
// resumes (ResumeAfterSec == 0 means never auto-resume).
//
// User movement is detected by comparing the live cursor position to where we
// last placed it — our own nudge returns to that spot, so only a real user move
// makes them differ.
package jiggler

import (
	"context"
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

func ensureDPIAware() {
	dpiOnce.Do(func() { procSetProcessDPIAware.Call() })
}

type point struct{ X, Y int32 }

func getCursor() (point, bool) {
	var p point
	r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p, r != 0
}

func setCursor(x, y int32) {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
}

// Config holds the tunable jiggler parameters (enable is tracked separately).
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

// decide computes the next state for one tick. Pure: no Windows calls.
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

// ---- manager --------------------------------------------------------------

// Jiggler runs the jiggle loop and can be reconfigured live.
type Jiggler struct {
	mu      sync.Mutex
	cfg     Config
	running bool
	cancel  context.CancelFunc
	st      state
}

func New() *Jiggler { return &Jiggler{} }

// Apply updates the config and starts/stops the loop to match `enabled`.
func (j *Jiggler) Apply(enabled bool, cfg Config) {
	j.mu.Lock()
	j.cfg = cfg
	running := j.running
	j.mu.Unlock()

	switch {
	case enabled && !running:
		j.start()
	case !enabled && running:
		j.stop()
	}
}

func (j *Jiggler) start() {
	ensureDPIAware()
	ctx, cancel := context.WithCancel(context.Background())
	j.mu.Lock()
	j.running = true
	j.cancel = cancel
	j.st = stActive
	j.mu.Unlock()
	go j.loop(ctx)
}

func (j *Jiggler) stop() {
	j.mu.Lock()
	c := j.cancel
	j.running = false
	j.st = stActive
	j.mu.Unlock()
	if c != nil {
		c()
	}
}

func (j *Jiggler) snapshot() Config {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cfg
}

func (j *Jiggler) setState(s state) {
	j.mu.Lock()
	j.st = s
	j.mu.Unlock()
}

// State reports "off", "active", or "paused" for the UI.
func (j *Jiggler) State() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.running {
		return "off"
	}
	if j.st == stPaused {
		return "paused"
	}
	return "active"
}

func (j *Jiggler) loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()

	var lastSet point
	haveLast := false
	st := stActive
	pausedSince := time.Now()
	var lastNudge time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cur, ok := getCursor()
			if !ok {
				continue
			}
			if !haveLast {
				lastSet = cur
				haveLast = true
				continue
			}
			cfg := j.snapshot()
			res := decide(tickInput{
				Now: now, Cur: cur, LastSet: lastSet, St: st,
				PausedSince: pausedSince, LastNudge: lastNudge, Cfg: cfg,
			})
			st, lastSet, pausedSince, lastNudge = res.St, res.LastSet, res.PausedSince, res.LastNudge
			j.setState(st)
			if res.Nudge {
				origin := cur
				d := int32(cfg.DistancePx)
				setCursor(origin.X+d, origin.Y)
				time.Sleep(40 * time.Millisecond)
				setCursor(origin.X, origin.Y) // net-zero: back to exact origin
				lastSet = origin
			}
		}
	}
}
