package web

import "os/exec"

// chromeCandidates are where a Chromium-based browser is installed on this OS, best first.
func chromeCandidates() []string {
	var out []string
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	return out
}
