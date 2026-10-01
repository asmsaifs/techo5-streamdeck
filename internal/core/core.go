// Package core runs the deck server for both front ends: the desktop app and cmd/decksrv. It owns
// the store, the config watcher and the listener, and lets the front end pause and resume serving.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/actions"
	"github.com/asmsaifs/techo5-streamdeck/internal/control"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/secrets"
	"github.com/asmsaifs/techo5-streamdeck/internal/server"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

// Options configure a Core.
type Options struct {
	// Dir is the config folder; empty means store.Dir().
	Dir string
	// Listen overrides the config's server.listen when not empty.
	Listen string
	// DryRun logs actions instead of performing them.
	DryRun bool
}

// Core is the running deck server plus its config.
type Core struct {
	Store  *store.Store
	Server *server.Server
	// Actions is the registry the server runs buttons with; the editor's Test button uses it too.
	Actions *actions.Registry
	// Secrets holds the Home Assistant token and the OBS password, in the OS keychain.
	Secrets secrets.Store
	Dir     string

	listen string

	mu     sync.Mutex
	cancel context.CancelFunc // non-nil while serving
	done   chan struct{}
	addr   net.Addr
	root   context.Context
	stop   context.CancelFunc

	onReload []func(*deck.Config)
}

// New opens the config and prepares the server; nothing listens until Start.
func New(o Options) (*Core, error) {
	dir := o.Dir
	if dir == "" {
		d, err := store.Dir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	var sys actions.System = actions.OS()
	if o.DryRun {
		sys = actions.DryRun(slog.Default())
	}
	c := &Core{Store: st, Dir: dir, listen: o.Listen}
	c.Actions = actions.New(sys, nil)
	c.Secrets = secrets.Keyring()
	c.Actions.Secrets = c.Secrets
	c.Actions.Integrations = func() *deck.Integrations { return c.Store.Config().Integrations }
	c.Server = &server.Server{
		Config:   st.Config,
		Runner:   c.Actions,
		Renderer: render.New(filepath.Join(dir, store.IconsDir)),
	}
	c.root, c.stop = context.WithCancel(context.Background())
	err = st.Watch(c.root, func(_ *deck.Config, err error) {
		if err != nil {
			slog.Warn("config.json is broken; keeping the last good one", "err", err)
			return
		}
		slog.Info("config.json reloaded")
		c.Reload()
	})
	if err != nil {
		c.stop()
		return nil, err
	}
	// The socket is a convenience: an app that cannot open it (a second instance, a folder path
	// too long for a socket) still serves the deck.
	if err := control.Listen(c.root, dir, c.Trigger); err != nil {
		slog.Warn("the trigger socket is not available", "err", err)
	}
	return c, nil
}

// OnReload calls fn with the config now and again each time it changes, whether the file was
// edited or the editor saved it. fn runs on the goroutine that noticed the change.
func (c *Core) OnReload(fn func(*deck.Config)) {
	c.mu.Lock()
	c.onReload = append(c.onReload, fn)
	c.mu.Unlock()
	fn(c.Store.Config())
}

// Reload tells everything that depends on the config that it changed: the decks on connected
// Shows are redrawn and the OnReload callbacks run.
func (c *Core) Reload() {
	c.Server.Reload()
	c.mu.Lock()
	fns := make([]func(*deck.Config), len(c.onReload))
	copy(fns, c.onReload)
	c.mu.Unlock()
	cfg := c.Store.Config()
	for _, fn := range fns {
		fn(cfg)
	}
}

// Press runs the action of one button as if it had been tapped on a Show, from this computer: a
// hotkey or the trigger command. Navigation is the Show's own screen and means nothing here.
func (c *Core) Press(ctx context.Context, profile, page, button string) error {
	cfg := c.Store.Config()
	p := cfg.Profiles[profile]
	if p == nil {
		return fmt.Errorf("there is no profile %q", profile)
	}
	pg := p.Pages[page]
	if pg == nil {
		return fmt.Errorf("profile %q has no page %q", profile, page)
	}
	cell, err := deck.ParseCell(button)
	if err != nil {
		return err
	}
	b := pg.Buttons[cell.String()]
	if b == nil || b.Action == nil {
		return fmt.Errorf("page %q has no button with an action at %s", page, cell)
	}
	switch b.Action.Type {
	case "page", "back":
		return fmt.Errorf("%s is a %s button, which moves around on a Show's screen", cell, b.Action.Type)
	}
	ctx, cancel := context.WithTimeout(actions.WithButton(ctx, actions.ButtonKey(profile, page, cell)), 10*time.Minute)
	defer cancel()
	err = c.Actions.Run(ctx, b.Action)
	// A toggle's ring on a Show follows the state, which this press may have changed.
	c.Server.Reload()
	return err
}

// Trigger presses the button an id names. The id is either a hotkey of the config, "Alt+1", or
// "profile/page/col,row". Only buttons the config has can be named: this is what a hotkey and the
// trigger command share, and neither can carry an action of its own.
func (c *Core) Trigger(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	cfg := c.Store.Config()
	if t, ok := cfg.Hotkeys[id]; ok {
		return c.Press(ctx, t.Profile, t.Page, t.Button)
	}
	for accel, t := range cfg.Hotkeys { // "cmdorctrl+alt+1" for "CmdOrCtrl+Alt+1"
		if strings.EqualFold(accel, id) {
			return c.Press(ctx, t.Profile, t.Page, t.Button)
		}
	}
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		return fmt.Errorf("%q is neither a hotkey of the config nor profile/page/col,row", id)
	}
	return c.Press(ctx, parts[0], parts[1], parts[2])
}

// Start listens and serves Shows. It returns once the listener is up, so a bind error is
// reported to the caller. Starting a running core is a no-op.
func (c *Core) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		return nil
	}
	if c.root.Err() != nil {
		return errors.New("core: closed")
	}
	addr := c.listen
	if addr == "" {
		addr = c.Store.Config().Server.Listen
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(c.root)
	done := make(chan struct{})
	c.cancel, c.done, c.addr = cancel, done, ln.Addr()
	go func() {
		defer close(done)
		if err := c.Server.Serve(ctx, ln); err != nil {
			slog.Error("deck server stopped", "err", err)
		}
	}()
	return nil
}

// Pause stops listening and drops every connected Show, and waits until that is done. The Shows
// reconnect by themselves once Start is called again.
func (c *Core) Pause() {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel, c.done, c.addr = nil, nil, nil
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Rebind moves the server to another listen address: it stops, listens on addr, and keeps addr
// as the one to use. If addr cannot be listened on the old address is back up and the error says
// why, so a typo in the editor never leaves the deck down. Connected Shows are dropped and
// reconnect by themselves.
func (c *Core) Rebind(addr string) error {
	c.mu.Lock()
	old, wasRunning := c.listen, c.cancel != nil
	c.mu.Unlock()
	c.Pause()
	c.mu.Lock()
	c.listen = addr
	c.mu.Unlock()
	err := c.Start()
	if err != nil {
		c.mu.Lock()
		c.listen = old
		c.mu.Unlock()
		if wasRunning {
			_ = c.Start()
		}
	}
	return err
}

// Running reports whether the server is listening.
func (c *Core) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancel != nil
}

// Addr is the address being listened on, or nil when paused.
func (c *Core) Addr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addr
}

// Close pauses the server and stops the config watcher.
func (c *Core) Close() {
	c.Pause()
	c.stop()
}

// LANIP is this computer's address on the LAN, as a guess for the instructions: the source
// address of a route out. Nothing is sent.
func LANIP() string {
	c, err := net.Dial("udp", "192.0.2.1:9") // TEST-NET-1: never reached, only routed
	if err != nil {
		return "<this computer's IP>"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
