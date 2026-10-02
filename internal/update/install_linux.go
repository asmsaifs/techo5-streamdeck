package update

import (
	"io"
	"os"
	"path/filepath"
)

// CanInstall reports whether Install can work here: only an AppImage, whose file the app can
// replace. A .deb belongs to the package manager and needs root.
func CanInstall() bool {
	t := os.Getenv("APPIMAGE")
	return t != "" && writable(filepath.Dir(t))
}

// Install replaces the AppImage this process runs from with file and starts it once we have
// exited. The running copy keeps its old inode, so renaming over it is safe on Linux. The caller
// quits when this returns nil.
func Install(file string, args []string) error {
	target := os.Getenv("APPIMAGE")
	if !CanInstall() {
		return ErrUnsupported
	}
	// Next to the target, so the final rename is atomic: a power cut leaves the old or the new
	// file, never half of one.
	tmp := target + ".new"
	if err := copyFile(file, tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return relaunchAfterExit(target, args...)
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
