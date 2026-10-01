// Package server accepts Shows, reads their hello and touches, and runs one source per connected device.
package server

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/asmsaifs/techo5-streamdeck/internal/sources/web"
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
	// Web runs the browsers of website tiles. May be nil: those buttons then say they cannot.
	Web *web.Manager
	Log *slog.Logger

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
	enc    *screen.Encoder

	// cur is what the Show is looking at: the deck, or a tile that was opened from it. kind says
	// which, for the devices panel. cmu guards both and opening, and is held only briefly.
	cmu     sync.Mutex
	cur     sources.Source
	kind    string
	opening bool
	// sendMu makes "is this source current" and "send its frame" one step, so that a frame of the
	// source that was left cannot arrive after the first of the one that took over.
	sendMu sync.Mutex
	chip   chipState // guarded by sendMu
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
	Source  string // what it is showing now: "deck", "web"
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
			Profile: se.deck.Profile(), Source: se.sourceKind(), Since: se.since,
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
	out.SetCaps(h) // kinds beyond the first three go only to a device that said it takes them
	cfg = s.Config() // the config may have been reloaded while the handshake ran
	name := s.effective(cfg, h.Name)
	if cfg.Profiles[name] == nil {
		s.log().Warn("a Show's profile does not exist", "name", h.Name, "profile", name)
		_ = out.Problem("The deck has no profile \"" + name + "\" for this Show. Check the desktop app's settings.")
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := deckview.New(s.Config, name, s.Renderer, s.Runner, s.log().With("show", h.Name))
	d.Tiles = s.Tiles
	if err := d.Start(ctx, image.Pt(h.W, h.H)); err != nil {
		s.log().Warn("source", "err", err)
		_ = out.Problem("The deck could not start: " + err.Error())
		return
	}
	defer d.Close()

	se := &session{hello: h, from: from, since: time.Now(), deck: d, cancel: cancel, cur: d, kind: "deck"}
	se.bytes = &sent
	se.enc = screen.NewEncoder(out, screen.Options{})
	defer se.enc.Close()
	d.Stream = func(a *deck.Action) error { return s.stream(ctx, se, out, a) }
	s.add(se)
	defer s.remove(se)
	s.log().Info("connected", "from", from, "name", h.Name, "w", h.W, "h", h.H, "profile", name, "caps", h.Caps)
	defer func() { s.log().Info("disconnected", "name", h.Name) }()

	go func() {
		defer cancel() // a picture that cannot be sent ends the session
		defer raw.Close()
		s.pump(se, d, d.Frames(), raw)
	}()
	go func() { <-ctx.Done(); raw.Close() }() // unblocks the touch read below

	for {
		t, err := lines.Touch()
		if err != nil {
			return
		}
		s.touch(se, t)
	}
}

// current is the source the Show is looking at.
func (se *session) current() sources.Source {
	se.cmu.Lock()
	defer se.cmu.Unlock()
	return se.cur
}

func (se *session) sourceKind() string {
	se.cmu.Lock()
	defer se.cmu.Unlock()
	return se.kind
}

// pump sends the frames of src to the Show for as long as src is the one it is looking at. The
// frames of a source that is not are dropped: the deck's, while a website is shown, and it draws
// itself again when it comes back. It returns when src stops, or when a picture cannot be sent,
// which closes the connection.
func (s *Server) pump(se *session, src sources.Source, frames <-chan *image.RGBA, raw net.Conn) {
	for img := range frames {
		se.sendMu.Lock()
		if se.current() != src {
			se.sendMu.Unlock()
			continue
		}
		se.frames.Add(1)
		if src != sources.Source(se.deck) {
			img = s.framed(se, img)
		}
		started := time.Now()
		err := se.enc.Send(img)
		wait := se.enc.MinInterval() - time.Since(started)
		se.sendMu.Unlock()
		// The next frame is the newest one: a source keeps only the latest, so waiting drops the
		// ones in between, which is the frame rate cap.
		if wait > 0 && err == nil {
			time.Sleep(wait)
		}
		if err != nil {
			s.log().Warn("send", "name", se.hello.Name, "err", err)
			se.cancel()
			if raw != nil {
				raw.Close()
			}
			return
		}
	}
}

// openTimeout is how long a page may take to show its first picture. Chrome starting cold and a
// slow site can take a while; a Show waiting longer than this is told it failed.
const openTimeout = 45 * time.Second

// stream is a button that shows something other than the deck: it opens it and, once it has
// something to show, the Show is switched to it. It returns when that has happened, or why it
// could not; the deck's button shows the press meanwhile.
func (s *Server) stream(ctx context.Context, se *session, out *wire.Sender, a *deck.Action) error {
	if a.Type != "stream.web" {
		return fmt.Errorf("%s is not available yet", a.Type)
	}
	var p struct {
		URL     string   `json:"url"`
		Allow   []string `json:"allow"`
		Profile string   `json:"profile"`
		Video   bool     `json:"video"`
	}
	if err := a.Params(&p); err != nil {
		return fmt.Errorf("bad parameters: %w", err)
	}
	if s.Web == nil {
		return errors.New("websites are not available in this build")
	}
	se.cmu.Lock()
	if se.opening || se.cur != sources.Source(se.deck) {
		se.cmu.Unlock()
		return errors.New("something is already being shown")
	}
	se.opening = true
	se.cmu.Unlock()
	defer func() { se.cmu.Lock(); se.opening = false; se.cmu.Unlock() }()

	src, err := s.Web.New(web.Spec{URL: p.URL, Allow: p.Allow, Profile: p.Profile})
	if err != nil {
		return err
	}
	// The source lives until the Show goes back to the deck or the session ends.
	sctx, cancel := context.WithCancel(ctx)
	if err := src.Start(sctx, image.Pt(se.hello.W, se.hello.H)); err != nil {
		cancel()
		return err
	}
	var first *image.RGBA
	select {
	case img, ok := <-src.Frames():
		if !ok {
			cancel()
			return src.Err()
		}
		first = img
	case <-time.After(openTimeout):
		cancel()
		return errors.New("the page took too long to open")
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}

	// The Show switches to the page: the first picture is sent whole.
	se.sendMu.Lock()
	se.cmu.Lock()
	se.cur, se.kind = src, "web"
	se.cmu.Unlock()
	se.enc.Invalidate()
	se.enc.SetVideo(p.Video)
	s.raiseChip(se)
	err = se.enc.Send(s.framed(se, first))
	se.sendMu.Unlock()
	if err != nil {
		cancel()
		return err
	}
	s.log().Info("showing a website", "name", se.hello.Name, "profile", src.Profile())

	go func() {
		defer cancel()
		s.pump(se, src, src.Frames(), nil)
		// The page stopped by itself (the browser died, the tab closed), or the session is over.
		if ctx.Err() != nil || se.current() != src {
			return
		}
		text := "The page stopped."
		if err := src.Err(); err != nil {
			text = "The page stopped: " + err.Error()
		}
		s.log().Warn("website ended", "name", se.hello.Name, "err", src.Err())
		_ = out.Problem(text)
		select {
		case <-time.After(4 * time.Second): // long enough to read
		case <-ctx.Done():
			return
		}
		s.showDeck(se, src)
	}()
	return nil
}

// showDeck takes the Show back from src to the deck, if src is still what it is looking at.
func (s *Server) showDeck(se *session, src sources.Source) {
	se.sendMu.Lock()
	se.cmu.Lock()
	if se.cur != src {
		se.cmu.Unlock()
		se.sendMu.Unlock()
		return
	}
	se.cur, se.kind = se.deck, "deck"
	se.cmu.Unlock()
	if se.chip.timer != nil {
		se.chip.timer.Stop()
	}
	se.chip = chipState{}
	se.enc.SetVideo(false)
	se.enc.Invalidate()
	se.sendMu.Unlock()
	_ = src.Close()
	se.deck.Refresh() // its frame goes out through the deck's pump
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
