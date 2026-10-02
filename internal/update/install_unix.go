//go:build darwin || linux

package update

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// relaunchAfterExit starts cmd once this process is gone. It must wait for the exit, not just
// start: the new app would find the deck's port still taken and open paused. The shell is in its
// own session so it outlives us.
func relaunchAfterExit(cmd string, args ...string) error {
	const script = `pid=$1; shift; while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done; exec "$@"`
	c := exec.Command("/bin/sh", append([]string{"-c", script, "sh", strconv.Itoa(os.Getpid()), cmd}, args...)...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return c.Start()
}
