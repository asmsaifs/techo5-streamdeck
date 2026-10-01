// Package tiles reads the values live buttons show: the time, processor and memory load, a Home
// Assistant entity, what a command prints. Readings are cached by what is read, so a script that
// two Shows both display runs once per interval, not once per Show.
package tiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Value is one reading.
type Value struct {
	Text string // what the tile shows; empty for a state tile
	// On is set by a state tile: whether the thing it watches is on.
	On  *bool
	Err error
}

// Env is what readings need of the computer; tests replace it.
type Env struct {
	Now func() time.Time
	CPU func(ctx context.Context) (float64, error) // percent
	RAM func(ctx context.Context) (float64, error) // percent
	// HA is a Home Assistant entity's state, with its unit when it has one.
	HA func(ctx context.Context, entity string) (string, error)
	// Output runs a program, or a shell line, and returns what it printed. A *exec.ExitError says
	// it ran and exited with a failure.
	Output func(ctx context.Context, command string, args []string, shell bool) (string, error)
}

// OSEnv is the real computer. ha reads Home Assistant (core wires the actions' client in); nil
// makes ha_state tiles say it is not set up.
func OSEnv(ha func(ctx context.Context, entity string) (string, error)) Env {
	if ha == nil {
		ha = func(context.Context, string) (string, error) { return "", errors.New("Home Assistant is not set up") }
	}
	return Env{
		Now: time.Now,
		CPU: func(ctx context.Context) (float64, error) {
			// Over the second just passed: the first reading of a process has nothing to compare to.
			p, err := cpu.PercentWithContext(ctx, time.Second, false)
			if err != nil || len(p) == 0 {
				return 0, fmt.Errorf("cpu: %v", err)
			}
			return p[0], nil
		},
		RAM: func(ctx context.Context) (float64, error) {
			v, err := mem.VirtualMemoryWithContext(ctx)
			if err != nil {
				return 0, err
			}
			return v.UsedPercent, nil
		},
		HA:     ha,
		Output: osOutput,
	}
}

func osOutput(ctx context.Context, command string, args []string, shell bool) (string, error) {
	var cmd *exec.Cmd
	if shell {
		cmd = shellCommand(ctx, command)
	} else {
		cmd = exec.CommandContext(ctx, command, args...)
	}
	out, err := cmd.Output() // stderr is dropped: a warning is not the value
	return string(out[:min(len(out), 4096)]), err
}

// Every is how often a tile is read, in seconds: its own setting, or its type's default.
func Every(t *deck.Tile) time.Duration {
	if t.Every >= 1 {
		return time.Duration(t.Every * float64(time.Second))
	}
	switch t.Type {
	case deck.TileClock:
		return time.Second
	case deck.TileCPU:
		return 2 * time.Second
	case deck.TileScript:
		return 10 * time.Second
	}
	return 5 * time.Second
}

const readTimeout = 10 * time.Second

var clockLayouts = map[string]string{"": "15:04", "24h": "15:04", "12h": "3:04 PM", "24h-seconds": "15:04:05", "date": "Mon 2 Jan"}

// Read reads a tile now, without the cache.
func Read(ctx context.Context, e Env, t *deck.Tile) Value {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	switch t.Type {
	case deck.TileClock:
		return Value{Text: e.Now().Format(clockLayouts[t.Format])}
	case deck.TileCPU:
		return percent(e.CPU(ctx))
	case deck.TileRAM:
		return percent(e.RAM(ctx))
	case deck.TileHA:
		s, err := e.HA(ctx, t.Entity)
		return Value{Text: oneLine(s), Err: err}
	case deck.TileScript:
		out, err := e.Output(ctx, t.Command, t.Args, t.Shell)
		if err != nil {
			return Value{Err: failure(err)}
		}
		return Value{Text: oneLine(out)}
	case deck.TileState:
		out, err := e.Output(ctx, t.Command, t.Args, t.Shell)
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			return Value{On: ptr(false)} // it ran and said no
		case err != nil:
			return Value{Err: failure(err)}
		}
		switch strings.ToLower(strings.TrimSpace(out)) {
		case "0", "false", "off", "no":
			return Value{On: ptr(false)}
		}
		return Value{On: ptr(true)}
	}
	return Value{Err: fmt.Errorf("there is no tile type %q", t.Type)}
}

func percent(v float64, err error) Value {
	if err != nil {
		return Value{Err: err}
	}
	return Value{Text: fmt.Sprintf("%.0f%%", v)}
}

func ptr(b bool) *bool { return &b }

func failure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("took longer than %s", readTimeout)
	}
	return err
}

// oneLine is the first line of s, trimmed and cut to what a button can show.
func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 60 {
		s = string([]rune(s)[:59]) + "…"
	}
	return s
}

// Cache reads tiles, and reuses a reading until the tile's interval has passed.
type Cache struct {
	Env Env

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	mu   sync.Mutex // held while reading, so concurrent askers wait for one reading
	at   time.Time
	val  Value
	used time.Time
}

// NewCache is a Cache reading from env.
func NewCache(env Env) *Cache { return &Cache{Env: env, entries: map[string]*entry{}} }

func key(t *deck.Tile) string {
	b, _ := json.Marshal(struct {
		T, F, E, C string
		A          []string
		S          bool
	}{t.Type, t.Format, t.Entity, t.Command, t.Args, t.Shell})
	return string(b)
}

// Get is the tile's value, read again if the last reading is older than the tile's interval.
func (c *Cache) Get(ctx context.Context, t *deck.Tile) Value {
	k := key(t)
	now := time.Now()
	c.mu.Lock()
	e := c.entries[k]
	if e == nil {
		// Readings nobody has asked for in a while are dropped: a tile that was edited away
		// must not be kept for ever.
		for old, oe := range c.entries {
			if now.Sub(oe.used) > 10*time.Minute {
				delete(c.entries, old)
			}
		}
		e = &entry{}
		c.entries[k] = e
	}
	e.used = now
	c.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// A little early counts as due: two Shows ticking a hair apart should not make a second reading.
	if !e.at.IsZero() && now.Sub(e.at) < Every(t)-50*time.Millisecond {
		return e.val
	}
	// The reading is shared, so it must not die with the one Show that happened to ask: a Show
	// that disconnects mid-reading would hand the next one its "cancelled" as the value.
	e.val = Read(context.WithoutCancel(ctx), c.Env, t)
	e.at = time.Now()
	return e.val
}
