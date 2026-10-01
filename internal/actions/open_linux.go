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
	if kind == OpenApp {
		// gtk-launch takes a desktop file's name ("firefox"); a program that has none is started by name.
		if _, err := exec.LookPath("gtk-launch"); err == nil {
			if run(exec.CommandContext(ctx, "gtk-launch", target)) == nil {
				return nil
			}
		}
		cmd := exec.Command(target)
		cmd.SysProcAttr = detached()
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
	return run(exec.CommandContext(ctx, "xdg-open", target))
}
