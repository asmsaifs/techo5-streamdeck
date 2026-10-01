package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/draw"
	"image/jpeg"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

const key = "a-long-enough-test-key"

const profiles = `{"default":{"pages":{
	"home":{"buttons":{
		"0,0":{"label":"Say","icon":"lucide:play","action":{"type":"run","command":"say"}},
		"1,0":{"label":"Apps","icon":"lucide:folder","action":{"type":"page","page":"apps"}}}},
	"apps":{"buttons":{"0,0":{"label":"Safari","action":{"type":"open.app","app":"Safari"}}}}}}}`

func config(t *testing.T) *deck.Config {
	t.Helper()
	c, err := deck.Decode([]byte(`{"version":1,"server":{"key":"` + key + `"},"profiles":` + profiles +
		`,"devices":{"Gone":{"profile":"missing"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	c.Fill()
	return c
}

type runner struct {
	mu  sync.Mutex
	ran []string
	ch  chan string
}

func (r *runner) Run(_ context.Context, a *deck.Action) error {
	r.mu.Lock()
	r.ran = append(r.ran, a.Type)
	r.mu.Unlock()
	select {
	case r.ch <- a.Type:
	default:
	}
	return nil
}

// show is a fake device: a connection, and the screen it has drawn from what it was sent.
type show struct {
	t      *testing.T
	conn   net.Conn
	secure *wire.Conn
	screen *image.RGBA
	mu     sync.Mutex
	msgs   chan wire.Msg
}

func startServer(t *testing.T, cfg *deck.Config, run *runner) (*Server, string) {
	t.Helper()
	s := &Server{Config: func() *deck.Config { return cfg }, Runner: run, Renderer: render.New(t.TempDir())}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, ln.Addr().String()
}

func connect(t *testing.T, addr, key, name string) *show {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	sc, err := wire.ClientHandshake(c, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wire.Hello{Name: name, W: 960, H: 480})
	if _, err := sc.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	sh := &show{t: t, conn: c, secure: sc, screen: image.NewRGBA(image.Rect(0, 0, 960, 480)), msgs: make(chan wire.Msg, 64)}
	go func() {
		br := bufio.NewReader(sc)
		for {
			m, err := wire.ReadMsg(br)
			if err != nil {
				close(sh.msgs)
				return
			}
			sh.msgs <- m
		}
	}()
	return sh
}

func (s *show) tap(x, y int) {
	b, _ := json.Marshal(wire.Touch{T: "tap", X: x, Y: y})
	if _, err := s.secure.Write(append(b, '\n')); err != nil {
		s.t.Fatal(err)
	}
}

// draw applies one message to the screen, as the device does.
func (s *show) draw(m wire.Msg) {
	img, err := jpeg.Decode(bytes.NewReader(m.Data))
	if err != nil {
		s.t.Fatal(err)
	}
	if m.Kind == wire.KindHalf {
		b := img.Bounds()
		big := image.NewRGBA(image.Rect(0, 0, b.Dx()*2, b.Dy()*2))
		for y := 0; y < big.Rect.Dy(); y++ {
			for x := 0; x < big.Rect.Dx(); x++ {
				big.Set(x, y, img.At(b.Min.X+x/2, b.Min.Y+y/2))
			}
		}
		img = big
	}
	r := image.Rectangle{Min: m.At, Max: m.At.Add(img.Bounds().Size())}
	draw.Draw(s.screen, r, img, img.Bounds().Min, draw.Src)
}

// settle draws everything that arrives until it has been quiet for a while, and returns the screen.
func (s *show) settle() *image.RGBA {
	s.t.Helper()
	for {
		select {
		case m, ok := <-s.msgs:
			if !ok {
				s.t.Fatal("the connection closed")
			}
			if m.Kind == wire.KindProblem {
				s.t.Fatalf("a problem: %s", m.Data)
			}
			s.draw(m)
		case <-time.After(500 * time.Millisecond):
			return s.screen
		}
	}
}

// differs is the mean difference per channel between two screens: JPEG makes equal pictures
// differ a little, a different page by a lot.
func differs(a, b *image.RGBA) float64 {
	var sum int
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(a.Pix))
}

func TestDeckOnTheWire(t *testing.T) {
	cfg := config(t)
	run := &runner{ch: make(chan string, 4)}
	srv, addr := startServer(t, cfg, run)
	sh := connect(t, addr, key, "Kitchen")

	p := cfg.Profiles["default"]
	rd := render.New(t.TempDir())
	home := rd.Grid(p, p.Pages["home"], image.Pt(960, 480), nil)
	l := render.NewLayout(p.Grid, image.Pt(960, 480))
	centre := func(c deck.Cell) (int, int) { r := l.Rect(c); return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2 }

	if d := differs(sh.settle(), home); d > 1.5 {
		t.Fatalf("the Show's screen is not the home page: mean difference %.2f", d)
	}
	if got := srv.Sessions(); len(got) != 1 || got[0].Name != "Kitchen" || got[0].Profile != "default" || got[0].W != 960 {
		t.Errorf("sessions %+v", got)
	}

	// A tap on an action button runs it on the computer.
	sh.tap(centre(deck.Cell{}))
	select {
	case got := <-run.ch:
		if got != "run" {
			t.Errorf("ran %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the action did not run")
	}
	if d := differs(sh.settle(), home); d > 1.5 {
		t.Errorf("after the action the screen is not plain again: %.2f", d)
	}

	// A folder opens, and its Back button returns.
	sh.tap(centre(deck.Cell{Col: 1}))
	apps := sh.settle()
	if d := differs(apps, home); d < 2 {
		t.Fatalf("the folder did not open: mean difference %.2f", d)
	}
	sh.tap(centre(deck.Cell{Col: 0, Row: p.Grid.Rows - 1}))
	if d := differs(sh.settle(), home); d > 1.5 {
		t.Errorf("Back did not return to home: %.2f", d)
	}
	if got := len(run.ran); got != 1 {
		t.Errorf("%d actions ran, want 1", got)
	}
}

func TestWrongKeyGetsNothing(t *testing.T) {
	_, addr := startServer(t, config(t), &runner{})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	// The client's side of the handshake may or may not notice the wrong key; either way the server
	// must send no picture.
	_, _ = wire.ClientHandshake(c, "another-long-enough-key")
	buf := make([]byte, 1)
	if n, _ := c.Read(buf); n != 0 {
		t.Error("a Show with the wrong key was sent something")
	}
}

func TestUnknownProfileGetsAProblem(t *testing.T) {
	_, addr := startServer(t, config(t), &runner{})
	sh := connect(t, addr, key, "Gone")
	select {
	case m := <-sh.msgs:
		if m.Kind != wire.KindProblem {
			t.Errorf("kind %d, want a problem", m.Kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was sent")
	}
}

// A Show that reconnects replaces its old session, which may be half open.
func TestReconnectReplacesTheOldSession(t *testing.T) {
	srv, addr := startServer(t, config(t), &runner{})
	old := connect(t, addr, key, "Kitchen")
	old.settle()
	fresh := connect(t, addr, key, "Kitchen")
	fresh.settle()
	// The old connection ends.
	deadline := time.After(3 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-old.msgs:
		case <-deadline:
			t.Fatal("the old session stayed open")
		}
	}
	if got := srv.Sessions(); len(got) != 1 {
		t.Errorf("%d sessions, want 1", len(got))
	}
}

// Reload redraws what a connected Show is looking at.
func TestReloadRedraws(t *testing.T) {
	cfg := config(t)
	var mu sync.Mutex
	cur := cfg
	s := &Server{Config: func() *deck.Config { mu.Lock(); defer mu.Unlock(); return cur }, Renderer: render.New(t.TempDir())}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, ln); close(done) }()
	defer func() { cancel(); <-done }()

	sh := connect(t, ln.Addr().String(), key, "Kitchen")
	before := image.NewRGBA(sh.settle().Rect)
	copy(before.Pix, sh.screen.Pix)

	mu.Lock()
	cur = config(t)
	cur.Profiles["default"].Theme.BG = "#aa2222"
	mu.Unlock()
	s.Reload()
	if d := differs(sh.settle(), before); d < 10 {
		t.Errorf("the new theme was not drawn: %.2f", d)
	}
}
