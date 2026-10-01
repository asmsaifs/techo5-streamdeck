package actions

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

const shellCmd = "cmd"

var shellArgs = []string{"/C"}

func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 /* DETACHED_PROCESS */}
}

func openTarget(ctx context.Context, kind OpenKind, target string) error {
	if kind == OpenApp {
		// cmd's start finds programs on the PATH and in App Paths, as the Run box does. cmd reads
		// & | < > ^ and % as its own, so a name with one is refused rather than quoted.
		if strings.ContainsAny(target, "&|<>^%\"\r\n") {
			return fmt.Errorf("%q cannot be started by name: it has a character cmd would read as its own", target)
		}
		return run(exec.CommandContext(ctx, "cmd", "/C", "start", "", target))
	}
	// The handler Explorer uses for a double click, with no shell reading the target.
	return run(exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", target))
}
