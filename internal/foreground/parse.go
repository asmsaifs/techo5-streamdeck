package foreground

import (
	"regexp"
	"strings"
)

var quoted = regexp.MustCompile(`"([^"]*)"\s*$`)

// parseLsappinfo reads the output of `lsappinfo info -only name -only bundleid <asn>`:
//
//	"LSDisplayName"="Safari"
//	"CFBundleIdentifier"="com.apple.Safari"
func parseLsappinfo(out string) (App, bool) {
	var a App
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m := quoted.FindStringSubmatch(v)
		if m == nil {
			continue
		}
		switch strings.Trim(strings.TrimSpace(k), `"`) {
		case "LSDisplayName":
			a.Name = clean(m[1])
		case "CFBundleIdentifier":
			a.ID = clean(m[1])
		}
	}
	return a, a.Name != "" || a.ID != ""
}

var windowID = regexp.MustCompile(`window id # (0x[0-9a-fA-F]+)`)

// parseActiveWindow reads `xprop -root _NET_ACTIVE_WINDOW`: "_NET_ACTIVE_WINDOW(WINDOW): window id # 0x4a00007".
// A desktop with nothing focused says 0x0.
func parseActiveWindow(out string) (string, bool) {
	m := windowID.FindStringSubmatch(out)
	if m == nil || m[1] == "0x0" {
		return "", false
	}
	return m[1], true
}

var wmClass = regexp.MustCompile(`WM_CLASS\(STRING\)\s*=\s*"([^"]*)"(?:,\s*"([^"]*)")?`)

// parseWMClass reads `xprop -id <w> WM_CLASS`: WM_CLASS(STRING) = "navigator", "firefox". The
// first is the instance, the second the class; the class is the application's name.
func parseWMClass(out string) (App, bool) {
	m := wmClass.FindStringSubmatch(out)
	if m == nil {
		return App{}, false
	}
	if m[2] == "" {
		return App{Name: m[1], ID: m[1]}, true
	}
	return App{Name: m[2], ID: m[1]}, true
}
