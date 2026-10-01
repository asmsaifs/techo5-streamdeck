package foreground

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Current asks Launch Services, which knows the front application without any permission: AppleScript
// would ask the user to allow "System Events".
func Current(ctx context.Context) (App, error) {
	asn, err := exec.CommandContext(ctx, "lsappinfo", "front").Output()
	if err != nil {
		return App{}, err
	}
	front := strings.TrimSpace(string(asn))
	if front == "" {
		return App{}, errors.New("no application is in front")
	}
	out, err := exec.CommandContext(ctx, "lsappinfo", "info", "-only", "name", "-only", "bundleid", front).Output()
	if err != nil {
		return App{}, err
	}
	a, ok := parseLsappinfo(string(out))
	if !ok {
		return App{}, errors.New("could not read the front application")
	}
	return a, nil
}
