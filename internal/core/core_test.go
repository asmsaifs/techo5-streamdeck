package core

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/actions"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/foreground"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

func TestPauseAndResume(t *testing.T) {
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if c.Running() {
		t.Fatal("running before Start")
	}
	for round := 0; round < 2; round++ {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		if err := c.Start(); err != nil { // no-op
			t.Fatal(err)
		}
		if !c.Running() {
			t.Fatal("not running after Start")
		}
		conn, err := net.Dial("tcp", c.Addr().String())
		if err != nil {
			t.Fatalf("round %d: dial: %v", round, err)
		}
		conn.Close()
		c.Pause()
		if c.Running() || c.Addr() != nil {
			t.Fatal("still running after Pause")
		}
	}
	c.Close()
	if err := c.Start(); err == nil {
		t.Fatal("Start after Close should fail")
	}
}

func TestRebind(t *testing.T) {
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	// An address already in use is refused, and the deck is still up where it was.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := c.Rebind(busy.Addr().String()); err == nil {
		t.Fatal("rebind onto a busy port worked")
	}
	if !c.Running() {
		t.Fatal("deck is down after a failed rebind")
	}
	// A free one is taken.
	if err := c.Rebind("127.0.0.1:0"); err != nil || !c.Running() {
		t.Fatalf("rebind: %v running=%v", err, c.Running())
	}
}

// recSys records what the actions did to the computer.
type recSys struct{ opened []string }

func (r *recSys) Open(_ context.Context, _ actions.OpenKind, target string) error {
	r.opened = append(r.opened, target)
	return nil
}
func (*recSys) Keys(context.Context, actions.Combo) error      { return nil }
func (*recSys) Control(context.Context, actions.Control) error { return nil }
func (*recSys) Type(context.Context, string) error             { return nil }
func (*recSys) Exec(context.Context, actions.ExecSpec) error   { return nil }

func TestTriggerPressesConfiguredButtonsOnly(t *testing.T) {
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sys := &recSys{}
	c.Actions = actions.New(sys, nil)

	cfg := c.Store.Config()
	home := cfg.Profiles["default"].Pages["home"]
	home.Buttons["0,0"] = &deck.Button{Action: &deck.Action{Type: "open.url", Raw: []byte(`{"type":"open.url","url":"https://example.com"}`)}}
	home.Buttons["1,0"] = &deck.Button{Action: &deck.Action{Type: "page", Raw: []byte(`{"type":"page","page":"home"}`)}}
	home.Buttons["2,0"] = &deck.Button{Label: "no action"}
	cfg.Hotkeys = map[string]deck.HotkeyTarget{"CmdOrCtrl+Alt+1": {Profile: "default", Page: "home", Button: "0,0"}}

	tests := []struct {
		id      string
		wantErr string // a part of the error, or "" for success
	}{
		{"CmdOrCtrl+Alt+1", ""},
		{"cmdorctrl+alt+1", ""},
		{"default/home/0,0", ""},
		{"  default/home/0,0 \n", ""},
		{"default/home/1,0", "moves around"},
		{"default/home/2,0", "no button with an action"},
		{"default/home/9,9", "no button with an action"},
		{"default/gone/0,0", `no page "gone"`},
		{"nobody/home/0,0", `no profile "nobody"`},
		{"Alt+7", "neither a hotkey"},
		{"default/home/x", "not a cell"},
	}
	for _, tt := range tests {
		before := len(sys.opened)
		err := c.Trigger(context.Background(), tt.id)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("Trigger(%q) = %v", tt.id, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("Trigger(%q) = %v, want an error with %q", tt.id, err, tt.wantErr)
		}
		if ran := len(sys.opened) - before; (tt.wantErr == "") != (ran == 1) {
			t.Errorf("Trigger(%q) ran the action %d times", tt.id, ran)
		}
	}
}

func TestOnReloadFiresNowAndOnReload(t *testing.T) {
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	n := 0
	c.OnReload(func(*deck.Config) { n++ })
	if n != 1 {
		t.Fatalf("OnReload called fn %d times at once, want 1", n)
	}
	c.Reload()
	if n != 2 {
		t.Errorf("fn ran %d times after a Reload, want 2", n)
	}
}

func TestWatchForegroundSwitchesOnlyForRealApps(t *testing.T) {
	var mu sync.Mutex
	front := foreground.App{Name: "Steam", ID: "com.valvesoftware.steam"}
	var asks int
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true, ForegroundEvery: 20 * time.Millisecond,
		Foreground: func(context.Context) (foreground.App, error) {
			mu.Lock()
			defer mu.Unlock()
			asks++
			return front, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	set := func(a foreground.App) { mu.Lock(); front = a; mu.Unlock() }
	got := func() []string { c.Server.Config(); return c.Server.Front() }
	waitFor := func(want string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for strings.Join(got(), ",") != want {
			select {
			case <-deadline:
				t.Fatalf("front = %v, want %q", got(), want)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	// No rules: nothing is asked.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if asks != 0 {
		t.Errorf("asked %d times with no rules", asks)
	}
	mu.Unlock()

	// A save replaces the config; the watcher reads whichever is current.
	setRules := func(r []deck.AutoRule) {
		b, _ := store.Encode(c.Store.Config())
		next, err := store.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		next.AutoSwitch = r
		if err := c.Store.Save(next); err != nil {
			t.Fatal(err)
		}
		c.Reload()
	}
	setRules([]deck.AutoRule{{App: "steam", Profile: "default"}})
	waitFor("Steam,com.valvesoftware.steam")

	set(foreground.App{Name: "techo5-streamdeck", ID: "techo5-streamdeck"}) // this app: the deck stays
	time.Sleep(100 * time.Millisecond)
	waitFor("Steam,com.valvesoftware.steam")

	set(foreground.App{Name: "Notes", ID: "com.apple.Notes"})
	waitFor("Notes,com.apple.Notes")

	setRules(nil) // the rules are removed: every Show goes back to its own profile
	waitFor("")
}
