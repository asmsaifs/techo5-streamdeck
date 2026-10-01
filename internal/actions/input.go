package actions

import "github.com/go-vgo/robotgo"

// tapKeys presses a combo. On macOS this needs the Accessibility permission, which the system asks
// for the first time and which docs/permissions.md describes.
func tapKeys(c Combo) error {
	mods := make([]any, len(c.Mods))
	for i, m := range c.Mods {
		mods[i] = m
	}
	return robotgo.KeyTap(c.Key, mods...)
}

func typeText(s string) error {
	robotgo.TypeStr(s)
	return nil
}
