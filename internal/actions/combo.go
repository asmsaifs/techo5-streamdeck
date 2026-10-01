package actions

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

// Combo is a key press with modifiers held: "Cmd+Shift+4" is Key "4" with Mods cmd and shift. The
// names are the ones robotgo takes.
type Combo struct {
	Key  string
	Mods []string
}

var modNames = map[string]string{
	"cmd": "cmd", "command": "cmd", "meta": "cmd", "super": "cmd", "win": "cmd", "windows": "cmd",
	"ctrl": "ctrl", "control": "ctrl",
	"alt": "alt", "option": "alt", "opt": "alt",
	"shift": "shift",
}

// keyNames maps the ways a key is written to robotgo's name for it.
var keyNames = map[string]string{
	"space": "space", "enter": "enter", "return": "enter", "tab": "tab",
	"esc": "escape", "escape": "escape", "backspace": "backspace", "delete": "delete", "del": "delete",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end", "pageup": "pageup", "pagedown": "pagedown",
	"plus": "+", "minus": "-", "comma": ",", "period": ".",
}

var fkey = regexp.MustCompile(`^f([1-9]|1[0-9]|2[0-4])$`)

// ParseCombo reads "Cmd+Space", "CmdOrCtrl+Shift+4", "Ctrl+Alt+Delete" or "F5". CmdOrCtrl is the
// Command key on a Mac and Ctrl elsewhere, so one deck works on both.
func ParseCombo(s string) (Combo, error) { return parseCombo(s, runtime.GOOS) }

func parseCombo(s, goos string) (Combo, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Combo{}, fmt.Errorf("no keys given")
	}
	// A "+" as the key itself: "Cmd++".
	parts := strings.Split(s, "+")
	if strings.HasSuffix(s, "++") {
		parts = append(strings.Split(strings.TrimSuffix(s, "++"), "+"), "plus")
	}
	var c Combo
	for i, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			return Combo{}, fmt.Errorf("%q has an empty part", s)
		}
		if i < len(parts)-1 {
			mod := modNames[p]
			if p == "cmdorctrl" || p == "commandorcontrol" {
				mod = "ctrl"
				if goos == "darwin" {
					mod = "cmd"
				}
			}
			if mod == "" {
				return Combo{}, fmt.Errorf("%q is not a modifier in %q (use Cmd, Ctrl, Alt, Shift or CmdOrCtrl)", p, s)
			}
			if !contains(c.Mods, mod) {
				c.Mods = append(c.Mods, mod)
			}
			continue
		}
		switch {
		case keyNames[p] != "":
			c.Key = keyNames[p]
		case fkey.MatchString(p):
			c.Key = p
		case len([]rune(p)) == 1:
			c.Key = p
		default:
			return Combo{}, fmt.Errorf("%q is not a key in %q", p, s)
		}
	}
	return c, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
