//go:build !darwin && !linux && !windows

package update

// CanInstall is false: no release file is built for this OS.
func CanInstall() bool { return false }

// Install always fails with ErrUnsupported.
func Install(string, []string) error { return ErrUnsupported }
