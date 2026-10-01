package web

import (
	"os"
	"path/filepath"
)

// chromeCandidates are where a Chromium-based browser is installed on this OS, best first. Edge
// comes with Windows, so there is nearly always one.
func chromeCandidates() []string {
	var out []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		out = append(out,
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
	}
	return out
}
