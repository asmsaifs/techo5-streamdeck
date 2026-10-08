package tiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// osGPULoad reads the load AMD's driver keeps in sysfs; NVIDIA cards were already tried with
// nvidia-smi. Intel's driver has no such file.
func osGPULoad(context.Context) (float64, error) {
	files, _ := filepath.Glob("/sys/class/drm/card*/device/gpu_busy_percent")
	best, found := 0.0, false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			continue
		}
		if !found || v > best {
			best, found = v, true
		}
	}
	if !found {
		return 0, errors.New("this computer's graphics card does not report its load (AMD and NVIDIA ones do)")
	}
	return best, nil
}
