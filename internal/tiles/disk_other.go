//go:build !linux

package tiles

// countDisk keeps every disk: macOS lists whole disks only, and Windows one entry per drive letter.
func countDisk(string) bool { return true }
