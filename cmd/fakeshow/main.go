// Command fakeshow is a simulated TECHO5 Show: a window that connects to a deck server as a Show's
// dashboard stream does, draws what it is sent and sends the mouse as touches, by the Show's rules
// (gesture.go). It is for developing without a Show in reach.
//
// Esc is the Show's swipe in from the left edge: it leaves the stream. Enter, or a click, opens it
// again.
//
// With -shot it opens no window: it connects, waits for a picture, sends the -tap touches, and writes
// what is on the screen to a PNG. That is for scripts and for checking a server by hand.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/asmsaifs/techo5-streamdeck/internal/store"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

func main() {
	server := flag.String("server", "127.0.0.1:9555", "the deck server, host:port")
	key := flag.String("key", "", "the deck's key (default: the one in decksrv's config.json)")
	name := flag.String("name", "Fake Show", "the device name sent in the hello")
	size := flag.String("size", "960x480", "the screen, WxH: 960x480 is a Show 5, 1280x800 a Show 8")
	caps := flag.String("caps", "", "comma-separated capabilities to advertise (none today)")
	shot := flag.String("shot", "", "no window: write the screen to this PNG and exit")
	taps := flag.String("tap", "", "with -shot: taps to send first, as x,y;x,y")
	wait := flag.Duration("wait", 5*time.Second, "with -shot: how long to wait for each picture")
	flag.Parse()

	w, h, err := parseSize(*size)
	if err == nil && *key == "" {
		*key, err = savedKey()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeshow:", err)
		os.Exit(2)
	}
	hello := wire.Hello{Name: *name, W: w, H: h}
	if *caps != "" {
		hello.Caps = strings.Split(*caps, ",")
	}
	s := newShow(*server, *key, hello)

	if *shot != "" {
		if err := headless(s, *shot, *taps, *wait); err != nil {
			fmt.Fprintln(os.Stderr, "fakeshow:", err)
			os.Exit(1)
		}
		return
	}

	g := &game{s: s, g: gestures{w: w, h: h}, frame: image.NewRGBA(image.Rect(0, 0, w, h))}
	g.open()
	ebiten.SetWindowSize(w, h)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowTitle("fakeshow")
	ebiten.SetScreenClearedEveryFrame(false)
	if err := ebiten.RunGame(g); err != nil && !errors.Is(err, ebiten.Termination) {
		fmt.Fprintln(os.Stderr, "fakeshow:", err)
		os.Exit(1)
	}
}

func parseSize(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(s, "x")
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if !ok || err1 != nil || err2 != nil || w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return 0, 0, fmt.Errorf("-size %q: want WxH, like 960x480", s)
	}
	return w, h, nil
}

// savedKey is the key in the config cmd/decksrv serves from: config.json in the user config folder.
func savedKey() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	c, err := store.Load(filepath.Join(dir, store.FileName))
	if err != nil {
		return "", fmt.Errorf("no -key given and no readable config: run decksrv once, or pass -key (%w)", err)
	}
	return c.Server.Key, nil
}

// headless connects, waits for a picture, sends the taps (waiting for a picture after each), and
// writes the screen to path.
func headless(s *show, path, taps string, wait time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)
	next := func(what string) error {
		select {
		case <-s.painted:
			// More of the picture may follow straight after: let it land.
			time.Sleep(100 * time.Millisecond)
			return nil
		case <-time.After(wait):
			_, problem, _, _ := s.snapshot(nil, 0)
			if problem != "" {
				return fmt.Errorf("no picture %s: %s", what, problem)
			}
			return fmt.Errorf("no picture %s within %v", what, wait)
		}
	}
	if err := next("from the server"); err != nil {
		return err
	}
	for _, t := range strings.Split(taps, ";") {
		if t == "" {
			continue
		}
		xs, ys, _ := strings.Cut(t, ",")
		x, err1 := strconv.Atoi(strings.TrimSpace(xs))
		y, err2 := strconv.Atoi(strings.TrimSpace(ys))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("-tap %q: want x,y", t)
		}
		s.touch(wire.Touch{T: "tap", X: x, Y: y})
		if err := next(fmt.Sprintf("after the tap at %d,%d", x, y)); err != nil {
			return err
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, s.hello.W, s.hello.H))
	s.snapshot(img, ^uint64(0))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// game is the window.
type game struct {
	s       *show
	g       gestures
	stop    context.CancelFunc // ends the stream; nil while it is left
	frame   *image.RGBA
	img     *ebiten.Image
	version uint64
}

func (g *game) open() {
	ctx, cancel := context.WithCancel(context.Background())
	g.stop = cancel
	go g.s.run(ctx)
}

func (g *game) Update() error {
	if g.stop == nil {
		if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
			g.open()
		}
		return nil
	}
	leave := inpututil.IsKeyJustPressed(ebiten.KeyEscape)
	x, y := ebiten.CursorPosition()
	x, y = clamp(x, 0, g.g.w-1), clamp(y, 0, g.g.h-1)
	switch {
	case inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft):
		g.g.press(x, y)
	case inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft):
		out, l := g.g.release(x, y)
		g.send(out)
		leave = leave || l
	case ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft):
		g.send(g.g.move(x, y))
	}
	if leave {
		slog.Info("left the stream")
		g.stop()
		g.stop = nil
		g.g.down = false
	}
	return nil
}

func (g *game) send(ts []wire.Touch) {
	for _, t := range ts {
		slog.Info("touch", "t", t.T, "x", t.X, "y", t.Y)
		g.s.touch(t)
	}
}

func (g *game) Draw(screen *ebiten.Image) {
	if g.img == nil {
		g.img = ebiten.NewImage(g.g.w, g.g.h)
	}
	v, problem, connected, st := g.s.snapshot(g.frame, g.version)
	if v != g.version {
		g.img.WritePixels(g.frame.Pix)
		g.version = v
	}
	screen.DrawImage(g.img, nil)
	switch {
	case g.stop == nil:
		ebitenutil.DebugPrintAt(screen, "Left the stream. Enter or click to open it again.", 8, 8)
	case problem != "":
		ebitenutil.DebugPrintAt(screen, problem, 8, 8)
	case v == 0:
		ebitenutil.DebugPrintAt(screen, "Connecting to "+g.s.server+"...", 8, 8)
	}
	state := "offline"
	if connected {
		state = "connected"
	}
	ebiten.SetWindowTitle(fmt.Sprintf("fakeshow %dx%d - %s - %d messages, %d KB", g.g.w, g.g.h, state, st.messages, st.bytes>>10))
}

func (g *game) Layout(int, int) (int, int) { return g.g.w, g.g.h }

func clamp(n, lo, hi int) int { return max(lo, min(n, hi)) }
