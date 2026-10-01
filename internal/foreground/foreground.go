// Package foreground tells which application the user is working in, so the deck can switch to
// that application's profile (step 3.5). It only names the application: it reads no window
// contents and needs no permission on macOS or Windows.
package foreground

import (
	"errors"
	"strings"
)

// App is the application in front. Name is what the user knows it by ("Safari", "firefox"), ID
// what identifies it for sure (a macOS bundle id, a Windows exe name, an X11 WM_CLASS).
type App struct {
	Name string
	ID   string
}

// ErrUnsupported means this desktop cannot say (Wayland keeps windows from each other).
var ErrUnsupported = errors.New("this desktop does not tell which application is in front")

// Names are the strings a rule can match the application by.
func (a App) Names() []string {
	var n []string
	for _, s := range []string{a.Name, a.ID} {
		if s = clean(s); s != "" {
			n = append(n, s)
		}
	}
	return n
}

// clean drops what makes a name hard to compare: the invisible marks macOS puts around display
// names, and surrounding space.
func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch r {
		case 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0xfeff:
			return -1
		}
		return r
	}, s))
}

// Match reports whether a rule's app text names one of names: the same text without regard to
// case, and with or without ".exe".
func Match(rule string, names ...string) bool {
	r := key(rule)
	if r == "" {
		return false
	}
	for _, n := range names {
		if key(n) == r {
			return true
		}
	}
	return false
}

func key(s string) string {
	return strings.TrimSuffix(strings.ToLower(clean(s)), ".exe")
}
