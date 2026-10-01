// Package server accepts Shows, reads their hello and touches, and runs one source per connected device.
package server

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/screen"
	"github.com/asmsaifs/techo5-streamdeck/internal/sources"
	deckview "github.com/asmsaifs/techo5-streamdeck/internal/sources/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Server is the deck server: it shows each connected Show the deck of its profile.
type Server struct {
	// Config is the current config. It is read at each connection (for the key and the profile)
	// and by each deck as it draws, so an edit that is reloaded applies at once.
	Config func() *deck.Config
	// Runner runs the buttons' actions. May be nil.
	Runner   deckview.Runner
	Renderer *render.Renderer
	// Tiles reads the values of live tiles, shared by every Show. May be nil: tiles show nothing.
	Tiles deckview.Tiler
	Log   *slog.Logger

	mu       sync.Mutex
	sessions map[*session]struct{}
	front    []string // the names of the application in front on this computer, for auto-switch
}

// session is one connected Show.
type session struct {
	hello  wire.Hello
	from   net.Addr
	since  time.Time
	deck   *deckview.Source
	cancel context.CancelFunc
	// Counters for the devices panel, which turns two readings into a rate.
	bytes  *atomic.Uint64
	frames atomic.Uint64
}

// countConn counts the bytes written to a Show.
type countConn struct {
	net.Conn
	n *atomic.Uint64
}

func (c countConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.n.Add(uint64(n))
	return n, err
}

// Info describes a connected Show, for the devices panel.
type Info struct {
	Name    string
	Addr    string
	W, H    int
	Profile string
	Source  string // what it is showing now: "deck"
	Since   time.Time
	// Totals since it connected: bytes written to the Show, and pictures sent.
	Bytes, Frames uint64
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Serve accepts Shows on ln until ctx ends, then closes ln and every connection.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); ln.Close() }()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			s.log().Warn("accept", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.handle(ctx, c) }()
	}
}

// Reload redraws every connected deck: call it when the config was reloaded. The auto-switch
// rules may have changed too, so each Show's profile is worked out again.
func (s *Server) Reload() {
	s.applyProfiles()
	s.mu.Lock()
	var all []*deckview.Source
	for se := range s.sessions {
		all = append(all, se.deck)
	}
	s.mu.Unlock()
	for _, d := range all {
		d.Refresh()
	}
}

// SetForeground tells the server which application is in front on this computer (its names, as
// foreground.App.Names gives them; none when unknown or when auto-switch is off). Each Show whose
// profile an auto-switch rule changes is switched.
func (s *Server) SetForeground(names ...string) {
	s.mu.Lock()
	s.front = names
	s.mu.Unlock()
	s.applyProfiles()
}

// Front is the application in front as last told by SetForeground.
func (s *Server) Front() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.front...)
}

// effective is the profile the Show called device should show now.
func (s *Server) effective(cfg *deck.Config, device string) string {
	s.mu.Lock()
	front := s.front
	s.mu.Unlock()
	return cfg.EffectiveProfile(device, front...)
}

func (s *Server) applyProfiles() {
	cfg := s.Config()
	if cfg == nil {
		return
	}
	type change struct {
		d    *deckview.Source
		name string
	}
	var todo []change
	s.mu.Lock()
	for se := range s.sessions {
		want := cfg.EffectiveProfile(se.hello.Name, s.front...)
		if cfg.Profiles[want] != nil && want != se.deck.Profile() {
			todo = append(todo, change{se.deck, want})
		}
	}
	s.mu.Unlock()
	for _, c := range todo {
		s.log().Info("profile switched", "from", c.d.Profile(), "to", c.name)
		c.d.SetProfile(c.name)
	}
}

// Sessions lists the connected Shows.
func (s *Server) Sessions() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Info
	for se := range s.sessions {
		out = append(out, Info{Name: se.hello.Name, Addr: se.from.String(), W: se.hello.W, H: se.hello.H,
			Profile: se.deck.Profile(), Source: "deck", Since: se.since,
			Bytes: se.bytes.Load(), Frames: se.frames.Load()})
	}
	return out
}

func (s *Server) handle(ctx context.Context, raw net.Conn) {
	defer raw.Close()
	from := raw.RemoteAddr()
	if tc, ok := raw.(*net.TCPConn); ok {
		// A Show that is switched off or leaves the Wi-Fi sends no FIN, and the device sends no
		// keepalive of its own, so the connection would otherwise sit here for ever.
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	cfg := s.Config()
	var sent atomic.Uint64
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := wire.ServerHandshake(countConn{raw, &sent}, cfg.Server.Key)
	if err != nil {
		s.log().Warn("handshake failed: a wrong key, or not a TECHO5 device", "from", from, "err", err)
		return
	}
	lines := wire.NewLines(c)
	h, err := lines.Hello()
	if err != nil {
		s.log().Warn("no hello", "from", from, "err", err)
		return
	}
	// The device never sets a read deadline after the hello, so a quiet server is fine.
	_ = raw.SetDeadline(time.Time{})

	out := wire.NewSender(c)
	cfg = s.Config() // the config may have been reloaded while the handshake ran
	name := s.effective(cfg, h.Name)
	if cfg.Profiles[name] == nil {
		s.log().Warn("a Show's profile does not exist", "name", h.Name, "profile", name)
		_ = out.Problem("The deck has no profile \"" + name + "\" for this Show. Check the desktop app's settings.")
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var src sources.Source
	d := deckview.New(s.Config, name, s.Renderer, s.Runner, s.log().With("show", h.Name))
	d.Tiles = s.Tiles
	src = d
	if err := src.Start(ctx, image.Pt(h.W, h.H)); err != nil {
		s.log().Warn("source", "err", err)
		_ = out.Problem("The deck could not start: " + err.Error())
		return
	}
	defer src.Close()

	se := &session{hello: h, from: from, since: time.Now(), deck: d, cancel: cancel}
	se.bytes = &sent
	s.add(se)
	defer s.remove(se)
	s.log().Info("connected", "from", from, "name", h.Name, "w", h.W, "h", h.H, "profile", name, "caps", h.Caps)
	defer func() { s.log().Info("disconnected", "name", h.Name) }()

	enc := screen.NewEncoder(out, screen.Options{})
	defer enc.Close()
	go func() {
		defer cancel() // a picture that cannot be sent ends the session
		defer raw.Close()
		for img := range src.Frames() {
			se.frames.Add(1)
			if err := enc.Send(img); err != nil {
				s.log().Warn("send", "name", h.Name, "err", err)
				return
			}
		}
	}()
	go func() { <-ctx.Done(); raw.Close() }() // unblocks the touch read below

	for {
		t, err := lines.Touch()
		if err != nil {
			return
		}
		src.Touch(t)
	}
}

// add registers a session. A Show that reconnects replaces its old session: the old connection
// may only look alive, as a Show that dropped off the Wi-Fi leaves it half open.
func (s *Server) add(se *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[*session]struct{}{}
	}
	for old := range s.sessions {
		if se.hello.Name != "" && old.hello.Name == se.hello.Name {
			old.cancel()
		}
	}
	s.sessions[se] = struct{}{}
}

func (s *Server) remove(se *session) {
	s.mu.Lock()
	delete(s.sessions, se)
	s.mu.Unlock()
}
