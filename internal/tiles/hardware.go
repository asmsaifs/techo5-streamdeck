package tiles

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Sensor names differ by computer and by system, so a temperature is looked for by the names it
// goes by, best first: the first name any sensor matches wins, and the hottest sensor of that name
// is the value (a processor reports each core; the hottest is what a fan reacts to).
var tempNames = map[string][]string{
	"cpu": {
		"coretemp_package", "x86_pkg_temp", "k10temp_tctl", "k10temp_tdie", "zenpower_tdie", // Linux
		"cpu_thermal", "cpu-thermal", // Raspberry Pi and other ARM boards
		"tc0d", "tc0p", "tc0h", // Intel Macs
		"pmu tdie", // Apple silicon: the chip's die
		"coretemp", "k10temp", "zenpower", "cpu",
		"thermalzone", "acpitz", // Windows and Linux ACPI zones, which are mostly the processor's
	},
	"gpu": {
		"amdgpu_edge", "amdgpu", "radeon", "nouveau", "gpu",
		"tg0d", "tg0p", "tg0h", // Intel Macs
		// Apple silicon has no sensor of the graphics' own: they are on the same die as the
		// processor, so the die is the closest there is.
		"pmu tdie",
	},
}

// pickTemp is the temperature for part ("cpu" or "gpu") among what the sensors read.
func pickTemp(stats []sensors.TemperatureStat, part string) (float64, bool) {
	for _, name := range tempNames[part] {
		best, found := 0.0, false
		for _, s := range stats {
			// A sensor that is not connected reads 0, or a nonsense value like 255 or -127.
			if s.Temperature <= 0 || s.Temperature >= 150 || !strings.Contains(strings.ToLower(s.SensorKey), name) {
				continue
			}
			if !found || s.Temperature > best {
				best, found = s.Temperature, true
			}
		}
		if found {
			return best, true
		}
	}
	return 0, false
}

func temperature(ctx context.Context, part string) (float64, error) {
	// A graphics card from NVIDIA does not show up among the sensors; its own tool reads it.
	if part == "gpu" {
		if c, err := nvidia(ctx, "temperature.gpu"); err == nil {
			return c, nil
		}
	}
	// The sensors can fail in part (one unreadable zone) and still return the rest.
	stats, err := sensors.TemperaturesWithContext(ctx)
	if c, ok := pickTemp(stats, part); ok {
		return c, nil
	}
	what := map[string]string{"cpu": "processor", "gpu": "graphics card"}[part]
	if err != nil {
		return 0, fmt.Errorf("cannot read the %s temperature: %v", what, err)
	}
	return 0, fmt.Errorf("this computer does not report its %s temperature", what)
}

func gpuLoad(ctx context.Context) (float64, error) {
	if v, err := nvidia(ctx, "utilization.gpu"); err == nil {
		return v, nil
	}
	return osGPULoad(ctx)
}

// nvidia reads one number about the NVIDIA graphics cards with nvidia-smi: the highest, when there
// are several.
func nvidia(ctx context.Context, field string) (float64, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return 0, err
	}
	out, err := exec.CommandContext(ctx, path, "--query-gpu="+field, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, err
	}
	return parseNvidia(string(out))
}

func parseNvidia(out string) (float64, error) {
	best, found := 0.0, false
	for _, line := range strings.Split(out, "\n") {
		v, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
		if err != nil {
			continue // a blank line, or "[N/A]" from a card that cannot say
		}
		if !found || v > best {
			best, found = v, true
		}
	}
	if !found {
		return 0, errors.New("nvidia-smi gave no value")
	}
	return best, nil
}

var ioregUtilization = regexp.MustCompile(`"Device Utilization %"=(\d+)`)

// parseIoreg is the busiest graphics processor's load in what `ioreg -c IOAccelerator` prints.
func parseIoreg(out string) (float64, error) {
	best, found := 0.0, false
	for _, m := range ioregUtilization.FindAllStringSubmatch(out, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		if !found || v > best {
			best, found = v, true
		}
	}
	if !found {
		return 0, errors.New("the graphics processor does not report its load")
	}
	return best, nil
}

// parseTypeperf adds up the values in what `typeperf -sc 1` prints: a header line of counter
// names, then one line of a time and a value per counter. Each running program's use of the
// graphics engine is its own counter, so the sum is the engine's load.
func parseTypeperf(out string) (float64, error) {
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	sum, rows := 0.0, 0
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) < 2 || strings.HasPrefix(rec[0], "(PDH-CSV") {
			continue // the header, or a line like "Exiting, please wait..."
		}
		rows++
		for _, f := range rec[1:] {
			if v, err := strconv.ParseFloat(strings.TrimSpace(f), 64); err == nil {
				sum += v
			}
		}
	}
	if rows == 0 {
		return 0, errors.New("the graphics processor does not report its load")
	}
	return min(sum, 100), nil
}

// rate counts the bytes a kind of tile is about twice, a second apart.
func rate(ctx context.Context, kind string) (float64, error) {
	a, err := ioBytes(ctx, kind)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(time.Second):
	}
	b, err := ioBytes(ctx, kind)
	if err != nil {
		return 0, err
	}
	if b < a {
		return 0, nil // a disk or a network went away in between
	}
	return float64(b-a) / time.Since(start).Seconds(), nil
}

// ioBytes is how many bytes the computer has received, sent, read or written since it started.
func ioBytes(ctx context.Context, kind string) (uint64, error) {
	var total uint64
	switch kind {
	case deck.TileNetDown, deck.TileNetUp:
		nics, err := net.IOCountersWithContext(ctx, true)
		if err != nil {
			return 0, err
		}
		for _, n := range nics {
			if !countNIC(n.Name) {
				continue
			}
			if kind == deck.TileNetDown {
				total += n.BytesRecv
			} else {
				total += n.BytesSent
			}
		}
	case deck.TileDiskRead, deck.TileDiskWrite:
		disks, err := disk.IOCountersWithContext(ctx)
		if err != nil {
			return 0, err
		}
		for name, d := range disks {
			if !countDisk(name) {
				continue
			}
			if kind == deck.TileDiskRead {
				total += d.ReadBytes
			} else {
				total += d.WriteBytes
			}
		}
	default:
		return 0, fmt.Errorf("there is no rate %q", kind)
	}
	return total, nil
}

// Interfaces whose traffic is counted again elsewhere are left out: the loopback never leaves the
// computer, and what goes through a VPN tunnel, a container's or a virtual machine's network also
// goes through the real network card.
var skippedNICs = []string{"utun", "tun", "tap", "wg", "tailscale", "docker", "veth", "br-", "virbr", "vethernet", "bridge", "awdl", "llw", "anpi", "gif", "stf"}

func countNIC(name string) bool {
	name = strings.ToLower(name)
	// Windows calls its loopback "Loopback Pseudo-Interface 1"; "lo" alone would also catch its
	// "Local Area Connection".
	if name == "lo" || name == "lo0" || strings.Contains(name, "loopback") {
		return false
	}
	for _, p := range skippedNICs {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}
