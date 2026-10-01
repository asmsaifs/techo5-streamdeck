package foreground

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// Current reads the X11 active window with xprop. Under Wayland there is nothing to ask (a
// window cannot see another), so it says so; XWayland applications do answer, which is why a
// Wayland session with no X application in front looks like "no window".
func Current(ctx context.Context) (App, error) {
	if os.Getenv("DISPLAY") == "" {
		return App{}, ErrUnsupported
	}
	if _, err := exec.LookPath("xprop"); err != nil {
		return App{}, errors.New("xprop is not installed; it is how the front application is found on Linux")
	}
	root, err := exec.CommandContext(ctx, "xprop", "-root", "_NET_ACTIVE_WINDOW").Output()
	if err != nil {
		return App{}, err
	}
	id, ok := parseActiveWindow(string(root))
	if !ok {
		return App{}, errors.New("no window is in front")
	}
	out, err := exec.CommandContext(ctx, "xprop", "-id", id, "WM_CLASS").Output()
	if err != nil {
		return App{}, err
	}
	a, ok := parseWMClass(string(out))
	if !ok {
		return App{}, errors.New("the front window has no class")
	}
	return a, nil
}
