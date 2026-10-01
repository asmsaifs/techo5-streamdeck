// Package server accepts Shows, reads their hello and touches, and runs one source per connected device.
package server

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"net"
	"sync"
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
	Log      *slog.Logger

	mu       sync.Mutex
	sessions map[*session]struct{}
}

// session is one connected Show.
type session struct {
	hello  wire.Hello
	from   net.Addr
	since  time.Time
	deck   *deckview.Source
	cancel context.CancelFunc
}

// Info describes a connected Show, for the devices panel.
type Info struct {
	Name    string
	Addr    string
	W, H    int
	Profile string
	Since   time.Time
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

// Reload redraws every connected deck: call it when the config was reloaded.
func (s *Server) Reload() {
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

// Sessions lists the connected Shows.
func (s *Server) Sessions() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Info
	for se := range s.sessions {
		out = append(out, Info{Name: se.hello.Name, Addr: se.from.String(), W: se.hello.W, H: se.hello.H,
			Profile: s.Config().ProfileFor(se.hello.Name), Since: se.since})
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
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := wire.ServerHandshake(raw, cfg.Server.Key)
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
	name := cfg.ProfileFor(h.Name)
	if cfg.Profiles[name] == nil {
		s.log().Warn("a Show's profile does not exist", "name", h.Name, "profile", name)
		_ = out.Problem("The deck has no profile \"" + name + "\" for this Show. Check the desktop app's settings.")
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var src sources.Source
	d := deckview.New(s.Config, name, s.Renderer, s.Runner, s.log().With("show", h.Name))
	src = d
	if err := src.Start(ctx, image.Pt(h.W, h.H)); err != nil {
		s.log().Warn("source", "err", err)
		_ = out.Problem("The deck could not start: " + err.Error())
		return
	}
	defer src.Close()

	se := &session{hello: h, from: from, since: time.Now(), deck: d, cancel: cancel}
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
