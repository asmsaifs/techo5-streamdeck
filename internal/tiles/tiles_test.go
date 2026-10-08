package tiles

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

func env() Env {
	return Env{
		Now: func() time.Time { return time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC) },
		CPU: func(context.Context) (float64, error) { return 36.6, nil },
		RAM: func(context.Context) (float64, error) { return 62.2, nil },
		Temp: func(_ context.Context, part string) (float64, error) {
			if part == "gpu" {
				return 0, errors.New("no sensor")
			}
			return 51.6, nil
		},
		GPU: func(context.Context) (float64, error) { return 7.4, nil },
		Rate: func(_ context.Context, kind string) (float64, error) {
			return map[string]float64{"net_down": 1_234_567, "net_up": 850_000, "disk_read": 0, "disk_write": 4_500_000_000}[kind], nil
		},
		HA:     func(_ context.Context, e string) (string, error) { return "21.5 °C", nil },
		Output: func(context.Context, string, []string, bool) (string, error) { return "", nil },
	}
}

func TestRead(t *testing.T) {
	exit := exec.Command("false").Run() // a real *exec.ExitError
	tests := []struct {
		name string
		tile deck.Tile
		out  string
		err  error
		text string
		on   string // "", "on", "off"
		bad  bool
	}{
		{"clock 24h", deck.Tile{Type: "clock"}, "", nil, "15:04", "", false},
		{"clock 12h", deck.Tile{Type: "clock", Format: "12h"}, "", nil, "3:04 PM", "", false},
		{"clock seconds", deck.Tile{Type: "clock", Format: "24h-seconds"}, "", nil, "15:04:05", "", false},
		{"clock date", deck.Tile{Type: "clock", Format: "date"}, "", nil, "Fri 2 Jan", "", false},
		{"cpu rounds", deck.Tile{Type: "cpu"}, "", nil, "37%", "", false},
		{"ram", deck.Tile{Type: "ram"}, "", nil, "62%", "", false},
		{"gpu", deck.Tile{Type: "gpu"}, "", nil, "7%", "", false},
		{"cpu temperature", deck.Tile{Type: "cpu_temp"}, "", nil, "52°C", "", false},
		{"cpu temperature fahrenheit", deck.Tile{Type: "cpu_temp", Format: "f"}, "", nil, "125°F", "", false},
		{"gpu temperature missing", deck.Tile{Type: "gpu_temp"}, "", nil, "", "", true},
		{"download", deck.Tile{Type: "net_down"}, "", nil, "1.2 MB/s", "", false},
		{"download in bits", deck.Tile{Type: "net_down", Format: "bits"}, "", nil, "9.9 Mbit/s", "", false},
		{"upload", deck.Tile{Type: "net_up"}, "", nil, "850 kB/s", "", false},
		{"disk read idle", deck.Tile{Type: "disk_read"}, "", nil, "0 B/s", "", false},
		{"disk write", deck.Tile{Type: "disk_write"}, "", nil, "4.5 GB/s", "", false},
		{"home assistant", deck.Tile{Type: "ha_state", Entity: "sensor.t"}, "", nil, "21.5 °C", "", false},
		{"script first line", deck.Tile{Type: "script", Command: "x"}, "  12 unread \nsecond line\n", nil, "12 unread", "", false},
		{"script long is cut", deck.Tile{Type: "script", Command: "x"}, strings.Repeat("é", 100), nil, strings.Repeat("é", 59) + "…", "", false},
		{"script fails", deck.Tile{Type: "script", Command: "x"}, "", errors.New("nope"), "", "", true},
		{"state: output on", deck.Tile{Type: "state", Command: "x"}, "yes\n", nil, "", "on", false},
		{"state: output off", deck.Tile{Type: "state", Command: "x"}, " Off \n", nil, "", "off", false},
		{"state: output 0", deck.Tile{Type: "state", Command: "x"}, "0", nil, "", "off", false},
		{"state: exit 0, no output", deck.Tile{Type: "state", Command: "x"}, "", nil, "", "on", false},
		{"state: exit 1", deck.Tile{Type: "state", Command: "x"}, "", exit, "", "off", false},
		{"state: cannot run", deck.Tile{Type: "state", Command: "x"}, "", errors.New("not found"), "", "", true},
		{"unknown", deck.Tile{Type: "weather"}, "", nil, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := env()
			e.Output = func(context.Context, string, []string, bool) (string, error) { return tt.out, tt.err }
			v := Read(context.Background(), e, &tt.tile)
			if (v.Err != nil) != tt.bad {
				t.Fatalf("error = %v, want error: %v", v.Err, tt.bad)
			}
			if v.Text != tt.text {
				t.Errorf("text = %q, want %q", v.Text, tt.text)
			}
			switch {
			case tt.on == "" && v.On != nil:
				t.Errorf("On = %v, want none", *v.On)
			case tt.on == "on" && (v.On == nil || !*v.On), tt.on == "off" && (v.On == nil || *v.On):
				t.Errorf("On = %v, want %s", v.On, tt.on)
			}
		})
	}
}

func TestEveryDefaults(t *testing.T) {
	for typ, want := range map[string]time.Duration{"clock": time.Second, "cpu": 2 * time.Second, "ram": 5 * time.Second, "gpu": 2 * time.Second, "net_up": 2 * time.Second, "disk_read": 2 * time.Second, "cpu_temp": 5 * time.Second, "script": 10 * time.Second, "ha_state": 5 * time.Second} {
		if got := Every(&deck.Tile{Type: typ}); got != want {
			t.Errorf("%s: %v, want %v", typ, got, want)
		}
	}
	if got := Every(&deck.Tile{Type: "clock", Every: 30}); got != 30*time.Second {
		t.Errorf("own interval: %v", got)
	}
}

func TestCacheReadsOncePerIntervalForAllAskers(t *testing.T) {
	var n atomic.Int32
	e := env()
	e.Output = func(context.Context, string, []string, bool) (string, error) {
		n.Add(1)
		time.Sleep(30 * time.Millisecond)
		return "v", nil
	}
	c := NewCache(e)
	tile := &deck.Tile{Type: "script", Command: "x", Every: 60}

	// Two Shows ask at once: one reading.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Get(context.Background(), tile) }()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Errorf("%d readings for 5 askers", n.Load())
	}
	if v := c.Get(context.Background(), tile); v.Text != "v" || n.Load() != 1 {
		t.Errorf("a fresh reading was read again: %d", n.Load())
	}
	// Another tile is another reading.
	c.Get(context.Background(), &deck.Tile{Type: "script", Command: "y", Every: 60})
	if n.Load() != 2 {
		t.Errorf("a different command shared a reading: %d", n.Load())
	}
	// An interval that has passed reads again.
	short := &deck.Tile{Type: "script", Command: "z", Every: 1}
	c.Get(context.Background(), short)
	time.Sleep(1100 * time.Millisecond)
	c.Get(context.Background(), short)
	if n.Load() != 4 {
		t.Errorf("an expired reading was reused: %d", n.Load())
	}
}

// The real command runner, on a unix shell.
func TestOSOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if runtime.GOOS == "windows" {
		t.Skip("a Windows runner's sh is not the shell the tile runs commands with")
	}
	e := OSEnv(nil)
	out, err := e.Output(context.Background(), "echo hi; echo err >&2", nil, true)
	if err != nil || strings.TrimSpace(out) != "hi" {
		t.Errorf("out %q err %v: stderr must not be the value", out, err)
	}
	v := Read(context.Background(), e, &deck.Tile{Type: "state", Command: "exit 3", Shell: true})
	if v.Err != nil || v.On == nil || *v.On {
		t.Errorf("exit 3 = %+v, want off", v)
	}
	v = Read(context.Background(), e, &deck.Tile{Type: "ha_state", Entity: "sensor.x"})
	if v.Err == nil {
		t.Error("an unset Home Assistant should say so")
	}
}

func TestRealCPUAndRAM(t *testing.T) {
	e := OSEnv(nil)
	for _, typ := range []string{"cpu", "ram"} {
		v := Read(context.Background(), e, &deck.Tile{Type: typ})
		if v.Err != nil || !strings.HasSuffix(v.Text, "%") {
			t.Errorf("%s = %+v", typ, v)
		}
	}
}

func TestRealIO(t *testing.T) {
	e := OSEnv(nil)
	for _, typ := range []string{"net_down", "net_up", "disk_read", "disk_write"} {
		v := Read(context.Background(), e, &deck.Tile{Type: typ})
		if v.Err != nil || !strings.HasSuffix(v.Text, "B/s") {
			t.Errorf("%s = %+v", typ, v)
		}
	}
}

func TestSpeed(t *testing.T) {
	for _, tt := range []struct {
		v    float64
		want string
	}{
		{0, "0 B/s"}, {999, "999 B/s"}, {999.6, "1.0 kB/s"}, {1500, "1.5 kB/s"}, {12_345, "12 kB/s"},
		{999_499, "999 kB/s"}, {9_960_000, "10 MB/s"}, {2.5e12, "2.5 TB/s"}, {5e15, "5000 TB/s"},
	} {
		if got := speed(tt.v, "B/s"); got != tt.want {
			t.Errorf("speed(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestPickTemp(t *testing.T) {
	s := func(kv ...any) []sensors.TemperatureStat {
		var out []sensors.TemperatureStat
		for i := 0; i < len(kv); i += 2 {
			out = append(out, sensors.TemperatureStat{SensorKey: kv[i].(string), Temperature: kv[i+1].(float64)})
		}
		return out
	}
	tests := []struct {
		name  string
		stats []sensors.TemperatureStat
		part  string
		want  float64 // 0: none
	}{
		{"linux intel: the package, not the hotter core", s("coretemp_core_0", 70.0, "coretemp_package_id_0", 60.0, "nvme_composite", 40.0), "cpu", 60},
		{"linux amd", s("k10temp_tctl", 55.0, "amdgpu_edge", 48.0, "amdgpu_junction", 52.0), "cpu", 55},
		{"linux amd graphics", s("k10temp_tctl", 55.0, "amdgpu_edge", 48.0, "amdgpu_junction", 52.0), "gpu", 48},
		{"intel mac", s("TC0P", 50.0, "TG0P", 45.0, "TA0P", 30.0), "gpu", 45},
		{"apple silicon: the hottest die sensor", s("PMU tdie1", 50.4, "PMU tdie6", 51.0, "NAND CH0 temp", 42.0), "cpu", 51},
		{"apple silicon graphics: the die", s("PMU tdie1", 50.4, "NAND CH0 temp", 42.0), "gpu", 50.4},
		{"windows thermal zone", s(`ACPI\ThermalZone\TZ00_0`, 41.0), "cpu", 41},
		{"unplugged sensors are skipped", s("coretemp_package_id_0", 0.0, "coretemp_core_0", 255.0, "coretemp_core_1", 58.0), "cpu", 58},
		{"nothing to go by", s("nvme_composite", 40.0), "cpu", 0},
		{"no graphics sensor", s("coretemp_package_id_0", 60.0), "gpu", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickTemp(tt.stats, tt.part)
			if ok != (tt.want != 0) || got != tt.want {
				t.Errorf("= %v, %v; want %v", got, ok, tt.want)
			}
		})
	}
}

func TestParseGPULoad(t *testing.T) {
	ioreg := `+-o AGXAcceleratorG16X  <class AGXAcceleratorG16X>
    "PerformanceStatistics" = {"Tiler Utilization %"=12,"Renderer Utilization %"=11,"Device Utilization %"=23,"In use system memory"=366919680}`
	typeperf := "\r\n\"(PDH-CSV 4.0)\",\"\\\\PC\\GPU Engine(pid_1_engtype_3D)\\Utilization Percentage\",\"\\\\PC\\GPU Engine(pid_2_engtype_3D)\\Utilization Percentage\"\r\n" +
		"\"10/08/2026 13:00:00.000\",\"12.500000\",\"3.250000\"\r\nExiting, please wait...\r\nThe command completed successfully.\r\n"
	tests := []struct {
		name  string
		parse func(string) (float64, error)
		in    string
		want  float64
		bad   bool
	}{
		{"ioreg", parseIoreg, ioreg, 23, false},
		{"ioreg without the statistic", parseIoreg, "+-o IOAccelerator", 0, true},
		{"typeperf sums the programs", parseTypeperf, typeperf, 15.75, false},
		{"typeperf with no counters", parseTypeperf, "Error: No valid counters.\r\n", 0, true},
		{"nvidia-smi: the busiest card", parseNvidia, "12\n47\n", 47, false},
		{"nvidia-smi cannot say", parseNvidia, "[N/A]\n", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.parse(tt.in)
			if (err != nil) != tt.bad || got != tt.want {
				t.Errorf("= %v, %v; want %v, error: %v", got, err, tt.want, tt.bad)
			}
		})
	}
}

func TestCountNIC(t *testing.T) {
	for name, want := range map[string]bool{
		"en0": true, "eth0": true, "wlp3s0": true, "Wi-Fi": true, "Ethernet": true, "Local Area Connection* 1": true,
		"lo": false, "lo0": false, "Loopback Pseudo-Interface 1": false, "utun3": false, "docker0": false,
		"veth12ab": false, "vEthernet (WSL)": false, "tailscale0": false, "bridge0": false,
	} {
		if got := countNIC(name); got != want {
			t.Errorf("countNIC(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCacheReadingSurvivesTheAskerLeaving(t *testing.T) {
	e := env()
	e.CPU = func(ctx context.Context) (float64, error) {
		time.Sleep(50 * time.Millisecond)
		return 10, ctx.Err()
	}
	c := NewCache(e)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	c.Get(ctx, &deck.Tile{Type: "cpu"}) // the Show goes away while it is read
	if v := c.Get(context.Background(), &deck.Tile{Type: "cpu"}); v.Err != nil || v.Text != "10%" {
		t.Errorf("the next Show got %+v", v)
	}
}
