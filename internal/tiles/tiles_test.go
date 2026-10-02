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

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

func env() Env {
	return Env{
		Now:    func() time.Time { return time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC) },
		CPU:    func(context.Context) (float64, error) { return 36.6, nil },
		RAM:    func(context.Context) (float64, error) { return 62.2, nil },
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
	for typ, want := range map[string]time.Duration{"clock": time.Second, "cpu": 2 * time.Second, "ram": 5 * time.Second, "script": 10 * time.Second, "ha_state": 5 * time.Second} {
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
