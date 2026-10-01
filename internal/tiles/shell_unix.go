//go:build !windows

package tiles

import (
	"context"
	"os/exec"
)

func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", line)
}
