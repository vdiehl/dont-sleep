package jiggler

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	cfg := Config{DistancePx: 2, IntervalSec: 30, ResumeAfterSec: 60}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return base.Add(d) }
	p := func(x, y int32) point { return point{x, y} }

	t.Run("active nudges when interval elapsed", func(t *testing.T) {
		r := decide(tickInput{Now: at(30 * time.Second), Cur: p(100, 100), LastSet: p(100, 100),
			St: stActive, LastNudge: base, Cfg: cfg})
		if !r.Nudge || r.St != stActive {
			t.Fatalf("expected nudge while active, got nudge=%v st=%v", r.Nudge, r.St)
		}
	})

	t.Run("active does not nudge before interval", func(t *testing.T) {
		r := decide(tickInput{Now: at(5 * time.Second), Cur: p(100, 100), LastSet: p(100, 100),
			St: stActive, LastNudge: base, Cfg: cfg})
		if r.Nudge {
			t.Fatal("should not nudge before interval elapsed")
		}
	})

	t.Run("user movement pauses", func(t *testing.T) {
		r := decide(tickInput{Now: at(40 * time.Second), Cur: p(500, 400), LastSet: p(100, 100),
			St: stActive, LastNudge: base, Cfg: cfg})
		if r.St != stPaused || r.Nudge {
			t.Fatalf("expected pause on user move, got st=%v nudge=%v", r.St, r.Nudge)
		}
		if r.PausedSince != at(40*time.Second) || r.LastSet != p(500, 400) {
			t.Fatalf("pause bookkeeping wrong: %+v", r)
		}
	})

	t.Run("paused resumes after idle timeout", func(t *testing.T) {
		r := decide(tickInput{Now: at(60 * time.Second), Cur: p(500, 400), LastSet: p(500, 400),
			St: stPaused, PausedSince: base, Cfg: cfg})
		if r.St != stActive || !r.Nudge {
			t.Fatalf("expected resume+nudge after idle, got st=%v nudge=%v", r.St, r.Nudge)
		}
	})

	t.Run("paused stays paused before idle timeout", func(t *testing.T) {
		r := decide(tickInput{Now: at(10 * time.Second), Cur: p(500, 400), LastSet: p(500, 400),
			St: stPaused, PausedSince: base, Cfg: cfg})
		if r.St != stPaused || r.Nudge {
			t.Fatalf("expected still paused, got st=%v nudge=%v", r.St, r.Nudge)
		}
	})

	t.Run("never auto-resume when ResumeAfterSec is 0", func(t *testing.T) {
		never := cfg
		never.ResumeAfterSec = 0
		r := decide(tickInput{Now: at(99 * time.Hour), Cur: p(500, 400), LastSet: p(500, 400),
			St: stPaused, PausedSince: base, Cfg: never})
		if r.St != stPaused || r.Nudge {
			t.Fatalf("expected never-resume to stay paused, got st=%v nudge=%v", r.St, r.Nudge)
		}
	})
}
