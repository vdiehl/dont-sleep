// Package core holds the app logic shared by the web server and the CLI:
// which settings are enabled, the defaults/previous snapshots, and the
// prevent/restore actions. State lives in %APPDATA%\stayawake.
package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"stayawake/internal/powercfg"
)

// StateDir is where config/snapshots are stored (survives app reinstalls).
func StateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir, _ = os.UserHomeDir()
	}
	return filepath.Join(dir, "stayawake")
}

func defaultsFile() string { return filepath.Join(StateDir(), "defaults.json") }
func previousFile() string { return filepath.Join(StateDir(), "previous.json") }
func configFile() string   { return filepath.Join(StateDir(), "config.json") }

// ---- config: which settings "Prevent" touches -----------------------------

// JigglerConfig controls the mouse-jiggler keep-awake mode.
type JigglerConfig struct {
	Enabled        bool `json:"enabled"`
	DistancePx     int  `json:"distancePx"`         // size of the back-and-forth nudge
	IntervalSec    int  `json:"intervalSeconds"`    // how often to nudge while active
	ResumeAfterSec int  `json:"resumeAfterSeconds"` // idle time before resuming; 0 = never
}

type Config struct {
	Enabled map[string]bool `json:"enabled"`
	Jiggler JigglerConfig   `json:"jiggler"`
}

func defaultConfig() Config {
	en := map[string]bool{}
	for _, s := range powercfg.Settings {
		en[s.Key] = s.Category == "core"
	}
	return Config{
		Enabled: en,
		Jiggler: JigglerConfig{Enabled: false, DistancePx: 2, IntervalSec: 30, ResumeAfterSec: 60},
	}
}

func clampJiggler(j *JigglerConfig) {
	if j.DistancePx <= 0 {
		j.DistancePx = 2
	}
	if j.IntervalSec <= 0 {
		j.IntervalSec = 30
	}
	if j.ResumeAfterSec < 0 { // 0 is valid ("never"); negative is not
		j.ResumeAfterSec = 60
	}
}

func LoadConfig() Config {
	// Start from defaults, then let the saved file override present fields only,
	// so missing keys (e.g. a config from an older version) keep their defaults.
	cfg := defaultConfig()
	if b, err := os.ReadFile(configFile()); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	for _, s := range powercfg.Settings {
		if _, ok := cfg.Enabled[s.Key]; !ok {
			cfg.Enabled[s.Key] = s.Category == "core"
		}
	}
	clampJiggler(&cfg.Jiggler)
	return cfg
}

func saveConfig(cfg Config) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configFile(), b, 0o644)
}

// SetEnabled merges updates into the enabled config and persists it.
func SetEnabled(updates map[string]bool) error {
	cfg := LoadConfig()
	for k, v := range updates {
		if _, ok := cfg.Enabled[k]; ok {
			cfg.Enabled[k] = v
		}
	}
	return saveConfig(cfg)
}

// SetJiggler replaces the jiggler config and persists it.
func SetJiggler(jc JigglerConfig) error {
	cfg := LoadConfig()
	cfg.Jiggler = jc
	clampJiggler(&cfg.Jiggler)
	return saveConfig(cfg)
}

func enabledKeys() []string {
	cfg := LoadConfig()
	var keys []string
	for _, s := range powercfg.Settings { // stable order
		if cfg.Enabled[s.Key] {
			keys = append(keys, s.Key)
		}
	}
	return keys
}

// ---- snapshots ------------------------------------------------------------

type acdc struct {
	AC int `json:"ac"`
	DC int `json:"dc"`
}

// Snapshot is a saved set of values (only settings that were available).
type Snapshot struct {
	CapturedAt string          `json:"capturedAt"`
	Settings   map[string]acdc `json:"settings"`
}

func snapshotFromRaw(raw map[string]powercfg.Value) map[string]acdc {
	out := map[string]acdc{}
	for k, v := range raw {
		if v.Available {
			out[k] = acdc{AC: v.AC, DC: v.DC}
		}
	}
	return out
}

func saveSnapshot(path string, raw map[string]powercfg.Value) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	snap := Snapshot{CapturedAt: time.Now().Format("2006-01-02T15:04:05"), Settings: snapshotFromRaw(raw)}
	b, _ := json.MarshalIndent(snap, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func loadSnapshot(path string) *Snapshot {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s Snapshot
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// ---- readings & state -----------------------------------------------------

func readAll() map[string]powercfg.Value {
	out := map[string]powercfg.Value{}
	for _, s := range powercfg.Settings {
		out[s.Key] = powercfg.QuerySetting(s)
	}
	return out
}

// isPreventedRaw: every enabled+available setting sits at its prevent value.
func isPreventedRaw(raw map[string]powercfg.Value, keys []string) bool {
	considered := 0
	for _, key := range keys {
		s, ok := powercfg.ByKey(key)
		v, hasV := raw[key]
		if !ok || !hasV || !v.Available {
			continue
		}
		considered++
		if v.AC != s.Prevent || v.DC != s.Prevent {
			return false
		}
	}
	return considered > 0
}

func captureDefaultsIfNeeded(raw map[string]powercfg.Value) {
	existing := loadSnapshot(defaultsFile())
	if existing == nil {
		if !isPreventedRaw(raw, enabledKeys()) {
			_ = saveSnapshot(defaultsFile(), raw)
		}
		return
	}
	// Backfill settings that only became visible later (e.g. unhidden).
	changed := false
	for k, v := range snapshotFromRaw(raw) {
		if _, ok := existing.Settings[k]; !ok {
			existing.Settings[k] = v
			changed = true
		}
	}
	if changed {
		b, _ := json.MarshalIndent(existing, "", "  ")
		_ = os.WriteFile(defaultsFile(), b, 0o644)
	}
}

// ---- status payload (shape consumed by the web UI) ------------------------

type StatusSetting struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Unit       string `json:"unit"`
	Note       string `json:"note"`
	PreventVal int    `json:"preventValue"`
	Hidden     bool   `json:"hidden"`
	Enabled    bool   `json:"enabled"`
	Available  bool   `json:"available"`
	NeedsAdmin bool   `json:"needsAdmin"`
	AC         *int   `json:"ac"`
	DC         *int   `json:"dc"`
}

type Status struct {
	Settings     []StatusSetting `json:"settings"`
	Prevented    bool            `json:"prevented"`
	IsAdmin      bool            `json:"isAdmin"`
	Defaults     *Snapshot       `json:"defaults"`
	Previous     *Snapshot       `json:"previous"`
	HasDefaults  bool            `json:"hasDefaults"`
	HasPrevious  bool            `json:"hasPrevious"`
	Jiggler      JigglerConfig   `json:"jiggler"`
	JigglerState string          `json:"jigglerState"` // off|active|paused (server fills live state)
}

func intPtr(v int) *int { return &v }

func GetStatus() Status {
	raw := readAll()
	captureDefaultsIfNeeded(raw)
	cfg := LoadConfig()
	admin := powercfg.IsAdmin()

	var settings []StatusSetting
	for _, s := range powercfg.Settings {
		v := raw[s.Key]
		ss := StatusSetting{
			Key: s.Key, Name: s.Name, Category: s.Category, Unit: string(s.Unit),
			Note: s.Note, PreventVal: s.Prevent, Hidden: s.Hidden,
			Enabled: cfg.Enabled[s.Key], Available: v.Available,
			NeedsAdmin: s.Hidden && !v.Available && !admin,
		}
		if v.Available {
			ss.AC = intPtr(v.AC)
			ss.DC = intPtr(v.DC)
		}
		settings = append(settings, ss)
	}

	defaults := loadSnapshot(defaultsFile())
	previous := loadSnapshot(previousFile())
	jigState := "off"
	if cfg.Jiggler.Enabled {
		jigState = "enabled" // the server overrides this with active/paused
	}
	return Status{
		Settings:     settings,
		Prevented:    isPreventedRaw(raw, enabledKeys()),
		IsAdmin:      admin,
		Defaults:     defaults,
		Previous:     previous,
		HasDefaults:  defaults != nil,
		HasPrevious:  previous != nil,
		Jiggler:      cfg.Jiggler,
		JigglerState: jigState,
	}
}

// ---- actions --------------------------------------------------------------

// Prevent sets every enabled setting to its keep-awake value, saving prior state.
func Prevent() (Status, error) {
	keys := enabledKeys()

	// Unhide any enabled hidden settings so we can read + write them (admin).
	for _, key := range keys {
		s, _ := powercfg.ByKey(key)
		if s.Hidden {
			v := powercfg.QuerySetting(s)
			if !v.Available {
				if err := powercfg.Unhide(s); err != nil {
					return Status{}, err
				}
			}
		}
	}

	raw := readAll()
	captureDefaultsIfNeeded(raw)
	if !isPreventedRaw(raw, keys) {
		_ = saveSnapshot(previousFile(), raw)
	}

	applied := false
	for _, key := range keys {
		s, _ := powercfg.ByKey(key)
		if !raw[key].Available {
			continue
		}
		if err := powercfg.SetSetting(s, s.Prevent, s.Prevent); err != nil {
			return Status{}, err
		}
		applied = true
	}
	if applied {
		if err := powercfg.ApplyActive(); err != nil {
			return Status{}, err
		}
	}
	return GetStatus(), nil
}

// Restore re-applies a saved snapshot. which is "previous" or "defaults".
func Restore(which string) (Status, error) {
	path := previousFile()
	if which == "defaults" {
		path = defaultsFile()
	}
	snap := loadSnapshot(path)
	if snap == nil {
		return Status{}, fmt.Errorf("no saved %s snapshot to restore", which)
	}
	applied := false
	for key, v := range snap.Settings {
		s, ok := powercfg.ByKey(key)
		if !ok {
			continue
		}
		if err := powercfg.SetSetting(s, v.AC, v.DC); err != nil {
			return Status{}, err
		}
		applied = true
	}
	if applied {
		if err := powercfg.ApplyActive(); err != nil {
			return Status{}, err
		}
	}
	return GetStatus(), nil
}
