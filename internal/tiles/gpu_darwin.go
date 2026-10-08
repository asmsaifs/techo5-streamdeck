package tiles

import (
	"context"
	"os/exec"
)

// osGPULoad reads the graphics processor's own statistics, which macOS keeps in the I/O registry
// for every user to read: no root, unlike powermetrics.
func osGPULoad(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator").Output()
	if err != nil {
		return 0, err
	}
	return parseIoreg(string(out))
}
