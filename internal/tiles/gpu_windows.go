package tiles

import (
	"context"
	"os/exec"
)

// osGPULoad reads the 3D engine's performance counters, the ones Task Manager shows as "3D", which
// every graphics card on Windows 10 and later has. typeperf takes a second to measure them. The
// counter names are English: on a Windows in another language they have other names and this
// fails.
func osGPULoad(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "typeperf", `\GPU Engine(*engtype_3D)\Utilization Percentage`, "-sc", "1").Output()
	if err != nil {
		return 0, err
	}
	return parseTypeperf(string(out))
}
