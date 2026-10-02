package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// CanInstall is always true: the installer is per user and needs no administrator.
func CanInstall() bool { return true }

// Install runs the NSIS installer silently over the folder this app lives in, then starts the app
// again. A Windows program cannot be overwritten while it runs, so the work is a cmd that waits a
// few seconds, outside this process; the caller quits when this returns nil.
func Install(file string, args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// /D= must come last and takes the path unquoted, as NSIS defines it.
	setup := syscall.EscapeArg(file) + " /S /D=" + filepath.Dir(exe)
	relaunch := `start "" ` + syscall.EscapeArg(exe)
	for _, a := range args {
		relaunch += " " + syscall.EscapeArg(a)
	}
	// ping is the portable sleep; the 3 s let the editor and the deck server end first.
	script := "ping -n 4 127.0.0.1 >nul & " + setup + " & " + relaunch
	c := exec.Command("cmd.exe")
	// cmd /S /C "..." strips the outer quotes and keeps the ones inside as they are.
	c.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /D /S /C "` + script + `"`,
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return c.Start()
}
