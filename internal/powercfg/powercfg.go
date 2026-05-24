// Package powercfg is a thin, locale-independent wrapper around the Windows
// `powercfg` CLI plus the catalog of settings StayAwake can manage.
//
// Locale note: earlier versions decided AC vs DC by matching the English
// substrings "AC"/"DC" in powercfg's output. That breaks on localized Windows
// (Portuguese prints "CA"/"CC", etc.), making every value read as unavailable.
// We now parse by position instead: `powercfg /query` for a single setting
// always ends with two hex values, Current AC then Current DC, regardless of
// language. See parseCurrentValues.
package powercfg

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Subgroup GUIDs.
const (
	subSleep     = "238c9fa8-0aad-41ed-83f4-97be242c8f20"
	subVideo     = "7516b95f-f776-4464-8c53-06167f40cc99"
	subDisk      = "0012ee47-9041-4b5d-9b77-535fba8b1442"
	subProcessor = "54533251-82be-4824-96c1-47b60b740d00"
)

// Unit describes how a setting's integer value should be interpreted.
type Unit string

const (
	UnitSeconds Unit = "seconds" // idle timeout; 0 = Never; prevent target = 0
	UnitPercent Unit = "percent" // processor state 0-100; prevent target = 100
	UnitBool    Unit = "bool"    // 0/1 flag
)

// Setting is one manageable power setting.
type Setting struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Category string `json:"category"` // "core" | "advanced"
	Sub      string `json:"-"`
	GUID     string `json:"-"`
	Unit     Unit   `json:"unit"`
	Prevent  int    `json:"preventValue"`
	Hidden   bool   `json:"hidden"`
	Note     string `json:"note"`
}

// Settings is the full catalog, core first then advanced (opt-in).
var Settings = []Setting{
	{Key: "standby", Name: "Sleep after", Category: "core", Sub: subSleep, GUID: "29f6c1db-86da-48c5-9fdb-f2b67b1f44da", Unit: UnitSeconds, Prevent: 0, Hidden: false, Note: "Lets the system enter sleep (S3/modern standby) when idle."},
	{Key: "hibernate", Name: "Hibernate after", Category: "core", Sub: subSleep, GUID: "9d7815a6-7ee4-497e-8888-515a05f02364", Unit: UnitSeconds, Prevent: 0, Hidden: false, Note: "Lets the system hibernate when idle."},
	{Key: "monitor", Name: "Turn off display after", Category: "core", Sub: subVideo, GUID: "3c0bc021-c8a8-4e07-a973-6b14cbcb2b7e", Unit: UnitSeconds, Prevent: 0, Hidden: false, Note: "Powers the display down when idle."},
	{Key: "disk", Name: "Turn off hard disk after", Category: "core", Sub: subDisk, GUID: "6738e2c4-e8a5-4a42-b16a-e040e769756e", Unit: UnitSeconds, Prevent: 0, Hidden: false, Note: "Spins the disk down when idle."},

	{Key: "proc_min", Name: "Minimum processor state", Category: "advanced", Sub: subProcessor, GUID: "893dee8e-2bef-41e0-89c6-b55d0929964c", Unit: UnitPercent, Prevent: 100, Hidden: false, Note: "Pin to 100% so the CPU never down-clocks. Higher power/heat."},
	{Key: "proc_max", Name: "Maximum processor state", Category: "advanced", Sub: subProcessor, GUID: "bc5038f7-23e0-4960-96da-33abaf5935ec", Unit: UnitPercent, Prevent: 100, Hidden: false, Note: "Allow the CPU to reach 100% (removes any frequency cap)."},
	{Key: "unattended", Name: "Unattended sleep timeout", Category: "advanced", Sub: subSleep, GUID: "7bc4a2f9-d8fc-4469-b07b-33eb785aaca0", Unit: UnitSeconds, Prevent: 0, Hidden: true, Note: "Sleep after the system wakes unattended. Hidden; needs admin to enable."},
	{Key: "idle_disable", Name: "Processor idle disable (C-states)", Category: "advanced", Sub: subProcessor, GUID: "5d76a2ca-e8c0-402f-a133-2158492d58ad", Unit: UnitBool, Prevent: 1, Hidden: true, Note: "Stops CPU cores entering low-power idle states. Aggressive: more power/heat. Hidden; needs admin."},
}

// ByKey returns the setting with the given key, if any.
func ByKey(key string) (Setting, bool) {
	for _, s := range Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// Value is the current AC/DC reading for a setting.
type Value struct {
	AC        int  `json:"ac"`
	DC        int  `json:"dc"`
	Available bool `json:"available"`
}

var hexRe = regexp.MustCompile(`0x([0-9a-fA-F]+)`)

// parseCurrentValues extracts (AC, DC) from a single-setting `powercfg /query`.
// It takes the LAST two hex values in the output (Current AC then Current DC),
// which is language-independent. Returns ok=false if fewer than two are present
// (e.g. a hidden/absent setting that prints nothing).
func parseCurrentValues(out string) (ac, dc int, ok bool) {
	m := hexRe.FindAllStringSubmatch(out, -1)
	if len(m) < 2 {
		return 0, 0, false
	}
	a, err1 := strconv.ParseUint(m[len(m)-2][1], 16, 64)
	d, err2 := strconv.ParseUint(m[len(m)-1][1], 16, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int(a), int(d), true
}

func run(args ...string) (string, error) {
	cmd := exec.Command("powercfg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("powercfg %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// QuerySetting reads a setting's current AC/DC values on the active scheme.
func QuerySetting(s Setting) Value {
	out, err := run("/query", "SCHEME_CURRENT", s.Sub, s.GUID)
	if err != nil {
		return Value{Available: false}
	}
	ac, dc, ok := parseCurrentValues(out)
	if !ok {
		return Value{Available: false}
	}
	return Value{AC: ac, DC: dc, Available: true}
}

// SetSetting writes a setting's AC and DC value on the active scheme (no activate).
func SetSetting(s Setting, ac, dc int) error {
	if _, err := run("/setacvalueindex", "SCHEME_CURRENT", s.Sub, s.GUID, strconv.Itoa(ac)); err != nil {
		return err
	}
	if _, err := run("/setdcvalueindex", "SCHEME_CURRENT", s.Sub, s.GUID, strconv.Itoa(dc)); err != nil {
		return err
	}
	return nil
}

// ApplyActive re-activates the current scheme so pending changes take effect.
func ApplyActive() error {
	_, err := run("/setactive", "SCHEME_CURRENT")
	return err
}

// Unhide clears the HIDE attribute on a hidden setting so it can be read/written.
// Requires administrator rights.
func Unhide(s Setting) error {
	if !s.Hidden {
		return nil
	}
	if !IsAdmin() {
		return fmt.Errorf("%q is a hidden Windows setting and needs administrator rights to enable", s.Name)
	}
	_, err := run("-attributes", s.Sub, s.GUID, "-ATTRIB_HIDE")
	return err
}

// IsAdmin reports whether the process is elevated, via shell32!IsUserAnAdmin
// (the same call the previous Python version used).
func IsAdmin() bool {
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin")
	ret, _, _ := proc.Call()
	return ret != 0
}
