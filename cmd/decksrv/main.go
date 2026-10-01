// Command decksrv is the deck server without the desktop app, for debugging. For now it is the
// hello-world of PLAN.md step 0.3: it accepts a Show, sends it one picture that greets it, logs
// every touch and marks where each tap landed.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

func main() {
	listen := flag.String("listen", ":9555", "address to listen on")
	key := flag.String("key", "", "the deck's key (default: the saved one, made on first run)")
	flag.Parse()

	k, err := loadKey(*key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decksrv:", err)
		os.Exit(1)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decksrv:", err)
		os.Exit(1)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	fmt.Println("On the Show: Dashboard server =", lanIP()+":"+port)
	fmt.Println("              key              =", k)
	fmt.Println("              Dashboard        = Streamed, then swipe in from the left edge")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("accept", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go serve(c, k)
	}
}

// loadKey is the key given, else the one saved by an earlier run, else a new one, saved. A saved key
// means the Show is set up once, not every time the server starts.
func loadKey(given string) (string, error) {
	if given != "" {
		return given, wire.CheckKey(given)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "techo5-streamdeck", "decksrv.key")
	if b, err := os.ReadFile(path); err == nil {
		k := strings.TrimSpace(string(b))
		return k, wire.CheckKey(k)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	k := wire.NewKey()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return k, os.WriteFile(path, []byte(k+"\n"), 0o600)
}

// lanIP is this computer's address on the LAN, as a guess for the instructions: the source address
// of a route out. Nothing is sent.
func lanIP() string {
	c, err := net.Dial("udp", "192.0.2.1:9") // TEST-NET-1: never reached, only routed
	if err != nil {
		return "<this computer's IP>"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func serve(raw net.Conn, key string) {
	defer raw.Close()
	from := raw.RemoteAddr()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := wire.ServerHandshake(raw, key)
	if err != nil {
		slog.Warn("handshake failed: a wrong key, or not a TECHO5 device", "from", from, "err", err)
		return
	}
	lines := wire.NewLines(c)
	h, err := lines.Hello()
	if err != nil {
		slog.Warn("no hello", "from", from, "err", err)
		return
	}
	// The device clears its deadline after the hello and never sets a read deadline again, so a
	// quiet server is fine: nothing needs to be sent to keep it.
	_ = raw.SetDeadline(time.Time{})
	slog.Info("connected", "from", from, "name", h.Name, "w", h.W, "h", h.H, "path", h.Path, "caps", h.Caps)

	out := wire.NewSender(c)
	bg := greeting(h)
	if err := out.Picture(image.Point{}, encode(bg)); err != nil {
		slog.Warn("send", "err", err)
		return
	}
	for {
		t, err := lines.Touch()
		if err != nil {
			slog.Info("disconnected", "name", h.Name, "err", err)
			return
		}
		slog.Info("touch", "name", h.Name, "t", t.T, "x", t.X, "y", t.Y)
		if t.T == "tap" || t.T == "down" {
			at, dot := mark(t, bg)
			if err := out.Picture(at, encode(dot)); err != nil {
				slog.Warn("send", "err", err)
				return
			}
		}
	}
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	return b.Bytes()
}

// greeting is the whole screen: a gradient and "Hello <name> w×h".
func greeting(h wire.Hello) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, h.W, h.H))
	for y := 0; y < h.H; y++ {
		for x := 0; x < h.W; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(20 + 60*x/h.W),
				G: uint8(30 + 50*y/h.H),
				B: uint8(90 + 120*(x+y)/(h.W+h.H)),
				A: 255,
			})
		}
	}
	name := h.Name
	if name == "" {
		name = "Show"
	}
	text(img, fmt.Sprintf("Hello %s", name), h.H/2-h.H/20, float64(h.H)/8)
	text(img, fmt.Sprintf("%d×%d · tap anywhere", h.W, h.H), h.H/2+h.H/8, float64(h.H)/16)
	return img
}

// text draws s centred across img with its baseline at y.
func text(img *image.RGBA, s string, y int, size float64) {
	f, _ := opentype.Parse(gobold.TTF)
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return
	}
	defer face.Close()
	d := &font.Drawer{Dst: img, Src: image.White, Face: face}
	w := d.MeasureString(s).Round()
	d.Dot = fixed.P((img.Rect.Dx()-w)/2, y)
	d.DrawString(s)
}

// mark is a small picture of a dot where t landed over bg, and where to draw it: a tap is
// accent-coloured, a press white. It is clipped to the screen, as the device would not draw past it.
func mark(t wire.Touch, bg *image.RGBA) (image.Point, image.Image) {
	const r = 10
	box := image.Rect(t.X-r, t.Y-r, t.X+r, t.Y+r).Intersect(bg.Rect)
	if box.Empty() {
		box = image.Rect(0, 0, 1, 1)
	}
	img := image.NewRGBA(box)
	// What is under the dot is the greeting; drawing it again keeps the dot's corners right.
	draw.Draw(img, box, bg, box.Min, draw.Src)
	c := color.RGBA{0x4f, 0x8c, 0xff, 255}
	if t.T == "down" {
		c = color.RGBA{255, 255, 255, 255}
	}
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			if dx, dy := x-t.X, y-t.Y; dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
	return box.Min, img
}
