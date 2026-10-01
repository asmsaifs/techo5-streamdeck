package actions

import (
	"context"
	"os/exec"
	"syscall"
)

const shellCmd = "/bin/sh"

var shellArgs = []string{"-c"}

func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func openTarget(ctx context.Context, kind OpenKind, target string) error {
	args := []string{"--", target}
	if kind == OpenApp {
		args = []string{"-a", target}
	}
	// "open" returns once it has handed the target over.
	return run(exec.CommandContext(ctx, "open", args...))
}
