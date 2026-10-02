package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// bundleOf is the .app that holds the program at exe (".../X.app/Contents/MacOS/prog").
func bundleOf(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" && strings.HasSuffix(app, ".app") {
		return app, true
	}
	return "", false
}

func currentBundle() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", false
	}
	return bundleOf(exe)
}

// CanInstall reports whether Install can work here: the program runs from an .app in a folder the
// user can write to (/Applications is, for an administrator).
func CanInstall() bool {
	b, ok := currentBundle()
	return ok && writable(filepath.Dir(b))
}

// Install opens the DMG, copies the .app out of it over the running one and starts the new app
// once we have exited. The caller quits when this returns nil. A failure before the swap leaves
// the old app untouched; the swap itself is two renames that are undone if the second fails.
func Install(file string, args []string) error {
	bundle, ok := currentBundle()
	if !ok || !writable(filepath.Dir(bundle)) {
		return ErrUnsupported
	}
	mnt, err := os.MkdirTemp("", "techo5-mount")
	if err != nil {
		return err
	}
	// -noverify: the file's sha256 was checked against the signed checksums already.
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noverify", "-mountpoint", mnt, file).CombinedOutput(); err != nil {
		os.Remove(mnt)
		return fmt.Errorf("update: open the disk image: %v: %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		exec.Command("hdiutil", "detach", "-force", mnt).Run()
		os.Remove(mnt)
	}()
	apps, _ := filepath.Glob(filepath.Join(mnt, "*.app"))
	if len(apps) != 1 {
		return fmt.Errorf("update: the disk image holds %d apps, want 1", len(apps))
	}

	// Staged beside the target, on its volume, so the swap is a rename.
	staged, old := bundle+".new", bundle+".old"
	os.RemoveAll(staged)
	if out, err := exec.Command("ditto", apps[0], staged).CombinedOutput(); err != nil {
		os.RemoveAll(staged)
		return fmt.Errorf("update: copy the app: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("codesign", "--verify", "--strict", staged).CombinedOutput(); err != nil {
		os.RemoveAll(staged)
		return fmt.Errorf("update: the new app's signature is broken: %v: %s", err, strings.TrimSpace(string(out)))
	}
	os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		os.RemoveAll(staged)
		return err
	}
	if err := os.Rename(staged, bundle); err != nil {
		os.Rename(old, bundle)
		os.RemoveAll(staged)
		return err
	}
	os.RemoveAll(old)
	return relaunchAfterExit("/usr/bin/open", append([]string{"-n", bundle, "--args"}, args...)...)
}
