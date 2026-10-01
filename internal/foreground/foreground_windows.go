package foreground

import (
	"context"
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
)

// Current is the executable of the window in front: GetForegroundWindow, then its process.
func Current(context.Context) (App, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return App{}, errors.New("no window is in front")
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return App{}, errors.New("the front window has no process")
	}
	// Limited rights are enough to read the path, and work for elevated processes too.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return App{}, err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH*2)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return App{}, err
	}
	exe := filepath.Base(windows.UTF16ToString(buf[:n]))
	return App{Name: exe, ID: exe}, nil
}
