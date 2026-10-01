// Package core runs the deck server for both front ends: the desktop app and cmd/decksrv. It owns
// the store, the config watcher and the listener, and lets the front end pause and resume serving.
package core

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"sync"

	"github.com/asmsaifs/techo5-streamdeck/internal/actions"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
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
	Dir     string

	listen string

	mu     sync.Mutex
	cancel context.CancelFunc // non-nil while serving
	done   chan struct{}
	addr   net.Addr
	root   context.Context
	stop   context.CancelFunc
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
		c.Server.Reload()
	})
	if err != nil {
		c.stop()
		return nil, err
	}
	return c, nil
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
