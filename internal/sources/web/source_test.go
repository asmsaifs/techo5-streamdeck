package web

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// These tests run a real browser, and are skipped where there is none.
func browserOrSkip(t *testing.T) *Manager {
	t.Helper()
	if _, err := FindChrome(""); err != nil {
		t.Skip(err)
	}
	// Not t.TempDir: the browser's processes may still be writing to its profile when the manager
	// has closed, and a failed cleanup would fail a test that passed. Retry, then give up quietly.
	dir, err := os.MkdirTemp("", "web-test")
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	t.Cleanup(func() {
		m.Close()
		for i := 0; i < 20; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
	return m
}

// site serves a red page with a button that turns it blue, and a link to another host.
func site(t *testing.T, elsewhere string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><body style="margin:0;background:#f00">
<button id=b style="position:absolute;left:0;top:0;width:200px;height:200px;border:0;background:#0f0"
  onclick="document.body.style.background='#00f';this.remove()"></button>
<a href="%[1]s" style="position:absolute;left:400px;top:0;width:200px;height:200px;background:#ff0;display:block"></a>
<div onclick="location.href='%[1]s'" style="position:absolute;left:600px;top:0;width:200px;height:200px;background:#0ff"></div>
</body>`, elsewhere)
	})
	mux.HandleFunc("/there", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<body style=background:#000>there") })
	return httptest.NewServer(mux)
}

// settled reads frames until one satisfies ok, or fails.
func settled(t *testing.T, s *Source, what string, ok func(*image.RGBA) bool) *image.RGBA {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case img, open := <-s.Frames():
			if !open {
				t.Fatalf("source stopped waiting for %s: %v", what, s.Err())
			}
			if ok(img) {
				return img
			}
		case <-deadline:
			t.Fatalf("never saw %s", what)
		}
	}
}

func near(img *image.RGBA, x, y int, r, g, b uint8) bool {
	c := img.RGBAAt(x, y)
	d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
	return d(c.R, r) < 8 && d(c.G, g) < 8 && d(c.B, b) < 8
}

func TestSourceShowsAPageAndTakesTouches(t *testing.T) {
	m := browserOrSkip(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	srv := site(t, other.URL)
	defer srv.Close()

	s, err := m.New(Spec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first := settled(t, s, "the page", func(img *image.RGBA) bool { return near(img, 100, 100, 0, 255, 0) })
	if first.Rect.Dx() != 960 || first.Rect.Dy() != 480 {
		t.Errorf("frame is %v, want 960x480", first.Rect)
	}
	if !near(first, 800, 400, 255, 0, 0) {
		t.Errorf("the page's background is not red: %v", first.RGBAAt(800, 400))
	}

	// A tap on the button turns the page blue.
	s.Touch(wire.Touch{T: "tap", X: 100, Y: 100})
	settled(t, s, "the page turned blue", func(img *image.RGBA) bool { return near(img, 800, 400, 0, 0, 255) })
}

func TestSourceKeepsThePageOnItsSites(t *testing.T) {
	m := browserOrSkip(t)
	// "Elsewhere" is another host name for the same machine: localhost against 127.0.0.1.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<body style=background:#000>elsewhere")
	}))
	defer other.Close()
	srv := site(t, strings.Replace(other.URL, "127.0.0.1", "localhost", 1))
	defer srv.Close()

	for name, at := range map[string]image.Point{
		"a link":          image.Pt(500, 100), // the page's own script stops it
		"a script's jump": image.Pt(700, 100), // the browser's check stops it
	} {
		t.Run(name, func(t *testing.T) {
			s, err := m.New(Spec{URL: srv.URL}) // allowed: 127.0.0.1 only
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			settled(t, s, "the page", func(img *image.RGBA) bool { return near(img, 100, 100, 0, 255, 0) })

			s.Touch(wire.Touch{T: "tap", X: at.X, Y: at.Y})
			time.Sleep(time.Second)
			// The page must still be the old one, and answer: the green button turns it blue.
			s.Touch(wire.Touch{T: "tap", X: 100, Y: 100})
			img := settled(t, s, "the page turned blue", func(img *image.RGBA) bool { return near(img, 800, 400, 0, 0, 255) })
			if near(img, 800, 400, 0, 0, 0) {
				t.Error("the page left its allowed sites")
			}
		})
	}
}

func TestSourceReportsAPageThatWillNotOpen(t *testing.T) {
	m := browserOrSkip(t)
	gone := httptest.NewServer(http.NotFoundHandler())
	addr := gone.URL
	gone.Close() // nothing listens there now
	s, err := m.New(Spec{URL: addr})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case _, ok := <-s.Frames():
		if ok {
			t.Fatal("a frame of a page that is not there")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the source neither shows the page nor stops")
	}
	if err := s.Err(); err == nil || !strings.Contains(err.Error(), "would not open") {
		t.Errorf("Err = %v, want the page would not open", err)
	}
}

func TestNewRefuses(t *testing.T) {
	m := NewManager(t.TempDir())
	defer m.Close()
	for name, sp := range map[string]Spec{
		"no address":      {},
		"not http":        {URL: "file:///etc/passwd"},
		"path as profile": {URL: "https://example.com", Profile: "../x"},
		"upper profile":   {URL: "https://example.com", Profile: "Work"},
		"bad extra":       {URL: "https://example.com", Allow: []string{"com"}},
	} {
		if _, err := m.New(sp); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s, err := m.New(Spec{URL: "https://www.youtube.com/tv"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Profile() != "youtube-com" {
		t.Errorf("default profile = %q, want youtube-com", s.Profile())
	}
}

func TestFindChromeNamed(t *testing.T) {
	if _, err := FindChrome("/no/such/browser"); err == nil {
		t.Error("a browser that is not there was accepted")
	}
}

// A tile that is left and opened again within the warm time gets its page back without loading it.
func TestParkedPageComesBackWarm(t *testing.T) {
	m := browserOrSkip(t)
	var loads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { // not the favicon
			loads.Add(1)
		}
		fmt.Fprint(w, `<body style="margin:0;background:#f00"><div style="width:200px;height:200px;background:#0f0"></div>`)
	}))
	defer srv.Close()

	open := func() *Source {
		s, err := m.New(Spec{URL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(context.Background(), image.Pt(960, 480)); err != nil {
			t.Fatal(err)
		}
		settled(t, s, "the page", func(img *image.RGBA) bool { return near(img, 100, 100, 0, 255, 0) })
		return s
	}
	first := open()
	first.Close()
	// Wait for it to be parked.
	deadline := time.Now().Add(10 * time.Second)
	for len(m.Stats()) == 0 || m.Stats()[0].Parked == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("never parked: %+v", m.Stats())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st := m.Stats()[0]; st.Tabs != 1 || st.Memory == 0 {
		t.Errorf("stats %+v: want one tab and a memory reading", st)
	}
	second := open()
	defer second.Close()
	if n := loads.Load(); n != 1 {
		t.Errorf("the page was loaded %d times, want once: the second tile should have been warm", n)
	}
	if st := m.Stats()[0]; st.Parked != 0 {
		t.Errorf("the tab is still counted as parked while in use: %+v", st)
	}
}

// Only WarmCount pages are kept; the oldest is closed to make room, and a parked page that
// nobody comes back for closes by itself.
func TestParkedPagesAreLimitedAndExpire(t *testing.T) {
	m := browserOrSkip(t)
	m.WarmCount = 1
	m.WarmFor = 2 * time.Second
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<body style="background:#0f0">`+r.URL.Path)
	}))
	defer srv.Close()
	for _, path := range []string{"/a", "/b"} {
		s, err := m.New(Spec{URL: srv.URL + path})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(context.Background(), image.Pt(960, 480)); err != nil {
			t.Fatal(err)
		}
		settled(t, s, "the page", func(img *image.RGBA) bool { return near(img, 800, 400, 0, 255, 0) })
		s.Close()
		deadline := time.Now().Add(10 * time.Second)
		for {
			st := m.Stats()
			if len(st) == 1 && st[0].Parked == 1 && st[0].Tabs == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("after %s: %+v, want one parked tab", path, st)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// The second is the one kept; after warmFor it goes, and the browser with it.
	deadline := time.Now().Add(15 * time.Second)
	for len(m.Stats()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the parked page never expired: %+v", m.Stats())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestPullDownRefreshesThePage(t *testing.T) {
	m := browserOrSkip(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	srv := site(t, other.URL)
	defer srv.Close()

	s, err := m.New(Spec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	settled(t, s, "the page", func(img *image.RGBA) bool { return near(img, 100, 100, 0, 255, 0) })
	s.Touch(wire.Touch{T: "tap", X: 100, Y: 100})
	settled(t, s, "the page turned blue", func(img *image.RGBA) bool { return near(img, 800, 400, 0, 0, 255) })

	drag := func(from, to int) {
		s.Touch(wire.Touch{T: "down", X: 500, Y: from})
		for y := from; y <= to; y += 20 {
			s.Touch(wire.Touch{T: "move", X: 500, Y: y})
			time.Sleep(20 * time.Millisecond) // a finger is not instant, and a busy page drops moves
		}
		s.Touch(wire.Touch{T: "up", X: 500, Y: to})
	}
	// A short pull is not a refresh: the page stays blue (it has the button's click undone only
	// by a reload).
	drag(100, 200)
	// A page that stands still sends no frames, so what is checked is that no red one comes.
	for quiet := time.After(3 * time.Second); ; {
		select {
		case img, open := <-s.Frames():
			if !open {
				t.Fatalf("source stopped: %v", s.Err())
			}
			if near(img, 800, 400, 255, 0, 0) {
				t.Fatal("a short pull loaded the page again")
			}
			continue
		case <-quiet:
		}
		break
	}

	drag(50, 300)
	settled(t, s, "the page loaded again", func(img *image.RGBA) bool { return near(img, 800, 400, 255, 0, 0) })
}

func TestBackTabGoesBackAndIsNotThePagesTouch(t *testing.T) {
	m := browserOrSkip(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	srv := site(t, other.URL)
	defer srv.Close()

	s, err := m.New(Spec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first := settled(t, s, "the page", func(img *image.RGBA) bool { return near(img, 100, 100, 0, 255, 0) })
	if near(first, 24, 240, 255, 0, 0) {
		t.Error("the back tab is not drawn")
	}
	if err := s.navigate(srv.URL + "/there"); err != nil {
		t.Fatal(err)
	}
	settled(t, s, "the second page", func(img *image.RGBA) bool { return near(img, 800, 400, 0, 0, 0) })
	s.Touch(wire.Touch{T: "tap", X: 24, Y: 240})
	settled(t, s, "the first page again", func(img *image.RGBA) bool { return near(img, 100, 100, 0, 255, 0) })

	// On the first page there is nothing to go back to: the page must stay, not go blank.
	s.Touch(wire.Touch{T: "tap", X: 24, Y: 240})
	time.Sleep(2 * time.Second)
	s.Touch(wire.Touch{T: "tap", X: 100, Y: 100})
	settled(t, s, "the first page still there", func(img *image.RGBA) bool { return near(img, 800, 400, 0, 0, 255) })
}
