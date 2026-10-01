// deckcap-linux: the Linux capture helper. Protocol: docs/helpers.md.
//
// X11 only (also under XWayland, for the windows that run there): windows come from the window
// manager's client list, the picture from the Composite extension's off-screen pixmap, which is
// right while the window is covered, and input from XTest. Sound is audio_linux.go. A native Wayland
// session needs the xdg-desktop-portal route and is not implemented: the helper says so. stdout
// carries only framed messages; logs go to stderr.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"github.com/asmsaifs/techo5-streamdeck/internal/helperkit"
)

const (
	evMotionNotify  = 6
	evButtonPress   = 4
	evButtonRelease = 5
)

type backend struct {
	mu     sync.Mutex
	c      *xgb.Conn
	root   xproto.Window
	atoms  map[string]xproto.Atom
	win    xproto.Window
	fw, fh int
	stop   chan struct{}
	done   chan struct{}
	snd    *sound
	out    *helperkit.Out
}

// connect opens the X connection on first use, so a missing display is reported as an answer to a
// command rather than as a helper that never became ready.
func (b *backend) connect() error {
	if b.c != nil {
		return nil
	}
	c, err := xgb.NewConn()
	if err != nil {
		hint := ""
		if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
			hint = " (a native Wayland session is not supported yet; X11 or XWayland is needed)"
		}
		return &helperkit.Error{Code: "unsupported", Msg: "cannot reach an X11 display: " + err.Error() + hint}
	}
	if err := composite.Init(c); err != nil {
		c.Close()
		return &helperkit.Error{Code: "unsupported", Msg: "the X server has no Composite extension"}
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return &helperkit.Error{Code: "unsupported", Msg: "the X server has no XTest extension, so touches cannot be sent"}
	}
	b.c = c
	b.root = xproto.Setup(c).DefaultScreen(c).Root
	b.atoms = map[string]xproto.Atom{}
	return nil
}

func (b *backend) atom(name string) xproto.Atom {
	if a, ok := b.atoms[name]; ok {
		return a
	}
	r, err := xproto.InternAtom(b.c, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	b.atoms[name] = r.Atom
	return r.Atom
}

func (b *backend) prop(w xproto.Window, atom xproto.Atom) []byte {
	r, err := xproto.GetProperty(b.c, false, w, atom, xproto.GetPropertyTypeAny, 0, 1<<14).Reply()
	if err != nil || r == nil {
		return nil
	}
	return r.Value
}

func (b *backend) title(w xproto.Window) string {
	if v := b.prop(w, b.atom("_NET_WM_NAME")); len(v) > 0 {
		return string(v)
	}
	return string(b.prop(w, xproto.AtomWmName))
}

func (b *backend) pid(w xproto.Window) int {
	v := b.prop(w, b.atom("_NET_WM_PID"))
	if len(v) < 4 {
		return 0
	}
	return int(binary.LittleEndian.Uint32(v))
}

// appName is the process's command name, or the WM_CLASS class when the window has no pid.
func (b *backend) appName(w xproto.Window, pid int) string {
	if pid > 0 {
		if v, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm"); err == nil {
			return strings.TrimSpace(string(v))
		}
	}
	parts := strings.Split(strings.TrimRight(string(b.prop(w, xproto.AtomWmClass)), "\x00"), "\x00")
	return parts[len(parts)-1]
}

func (b *backend) candidates() []xproto.Window {
	var ws []xproto.Window
	v := b.prop(b.root, b.atom("_NET_CLIENT_LIST"))
	for i := 0; i+4 <= len(v); i += 4 {
		ws = append(ws, xproto.Window(binary.LittleEndian.Uint32(v[i:])))
	}
	if len(ws) == 0 {
		// A window manager without EWMH: the root's children are the best there is.
		if t, err := xproto.QueryTree(b.c, b.root).Reply(); err == nil {
			ws = t.Children
		}
	}
	return ws
}

func (b *backend) List() ([]helperkit.Window, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.connect(); err != nil {
		return nil, err
	}
	self := os.Getpid()
	var list []helperkit.Window
	for _, w := range b.candidates() {
		at, err := xproto.GetWindowAttributes(b.c, w).Reply()
		if err != nil || at.MapState != xproto.MapStateViewable || at.OverrideRedirect {
			continue
		}
		g, err := xproto.GetGeometry(b.c, xproto.Drawable(w)).Reply()
		if err != nil || g.Width <= 100 || g.Height <= 80 {
			continue
		}
		pid := b.pid(w)
		if pid == self {
			continue
		}
		list = append(list, helperkit.Window{
			ID: strconv.FormatUint(uint64(w), 10), Title: b.title(w), App: b.appName(w, pid),
			W: int(g.Width), H: int(g.Height),
		})
	}
	return list, nil
}

func (b *backend) Start(out *helperkit.Out, a helperkit.StartArgs) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.connect(); err != nil {
		return err
	}
	n, err := strconv.ParseUint(a.Window, 10, 32)
	w := xproto.Window(n)
	if err != nil || n == 0 {
		return &helperkit.Error{Code: "no_window", Msg: "window " + a.Window + " not found"}
	}
	if _, err := xproto.GetGeometry(b.c, xproto.Drawable(w)).Reply(); err != nil {
		return &helperkit.Error{Code: "no_window", Msg: "window " + a.Window + " not found"}
	}
	// Automatic redirection keeps a running compositor working while the server also keeps the
	// window's pixels off-screen, which is what makes a covered window grabbable.
	if err := composite.RedirectWindowChecked(b.c, w, composite.RedirectAutomatic).Check(); err != nil {
		return fmt.Errorf("redirect window: %w", err)
	}
	b.win, b.fw, b.fh, b.out = w, 0, 0, out
	b.stop, b.done = make(chan struct{}), make(chan struct{})
	go b.captureLoop(out, w, a, b.stop, b.done)
	if a.Audio {
		s, err := startSound(out, b.pid(w))
		if err != nil {
			out.Event("error", "unsupported", "no sound: "+err.Error())
		} else {
			b.snd = s
		}
	}
	return nil
}

func (b *backend) Stop() {
	b.mu.Lock()
	stop, done, snd, w := b.stop, b.done, b.snd, b.win
	b.stop, b.done, b.snd, b.win = nil, nil, nil, 0
	b.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
		composite.UnredirectWindow(b.c, w, composite.RedirectAutomatic)
	}
	if snd != nil {
		snd.Stop()
	}
}

func (b *backend) captureLoop(out *helperkit.Out, w xproto.Window, a helperkit.StartArgs, stop, done chan struct{}) {
	defer close(done)
	var pix xproto.Pixmap
	pw, ph := 0, 0
	defer func() {
		if pix != 0 {
			xproto.FreePixmap(b.c, pix)
		}
	}()
	tick := time.NewTicker(time.Second / time.Duration(a.FPS))
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		g, err := xproto.GetGeometry(b.c, xproto.Drawable(w)).Reply()
		if err != nil {
			out.Event("error", "no_window", "the window was closed")
			return
		}
		gw, gh := int(g.Width), int(g.Height)
		if gw == 0 || gh == 0 {
			continue
		}
		// The named pixmap is a snapshot of the window's buffer at one size; a resize needs a new one.
		if pix == 0 || gw != pw || gh != ph {
			if pix != 0 {
				xproto.FreePixmap(b.c, pix)
				pix = 0
			}
			p, err := xproto.NewPixmapId(b.c)
			if err != nil {
				continue
			}
			if composite.NameWindowPixmapChecked(b.c, w, p).Check() != nil {
				continue // unmapped (minimized): keep the last picture
			}
			pix, pw, ph = p, gw, gh
		}
		img, err := xproto.GetImage(b.c, xproto.ImageFormatZPixmap, xproto.Drawable(pix), 0, 0, uint16(pw), uint16(ph), 0xffffffff).Reply()
		if err != nil || img.Depth < 24 || len(img.Data) < pw*ph*4 {
			// The pixmap went stale under us (the window was remapped): name it again next tick.
			xproto.FreePixmap(b.c, pix)
			pix = 0
			continue
		}
		px, fw, fh := helperkit.Scale(img.Data, pw, ph, pw*4, a.MaxW)
		if px == nil {
			continue
		}
		b.mu.Lock()
		b.fw, b.fh = fw, fh
		b.mu.Unlock()
		out.Frame(fw, fh, px)
	}
}

// topLevel is the ancestor of w that is a child of the root: the window manager's frame when there
// is one, which is what the server reports as being under the pointer.
func (b *backend) topLevel(w xproto.Window) xproto.Window {
	for range 16 {
		t, err := xproto.QueryTree(b.c, w).Reply()
		if err != nil || t.Parent == b.root || t.Parent == 0 {
			return w
		}
		w = t.Parent
	}
	return w
}

func (b *backend) fake(typ, detail byte, x, y int16) {
	xtest.FakeInput(b.c, typ, detail, 0, b.root, x, y, 0)
}

func (b *backend) click(detail byte, times int) {
	for range times {
		b.fake(evButtonPress, detail, 0, 0)
		b.fake(evButtonRelease, detail, 0, 0)
	}
}

func (b *backend) Input(kind string, x, y, dy int) {
	b.mu.Lock()
	w, fw, fh, out := b.win, b.fw, b.fh, b.out
	b.mu.Unlock()
	if w == 0 || fw == 0 || b.c == nil {
		return
	}
	g, err := xproto.GetGeometry(b.c, xproto.Drawable(w)).Reply()
	if err != nil {
		return
	}
	o, err := xproto.TranslateCoordinates(b.c, w, b.root, 0, 0).Reply()
	if err != nil {
		return
	}
	// Clipped to the window: nothing outside it can be clicked.
	mx, my := helperkit.MapPoint(x, y, fw, fh, int(g.Width), int(g.Height))
	px, py := int(o.DstX)+mx, int(o.DstY)+my

	if (kind == "down" || kind == "tap") && !b.bringToFront(w, px, py, out) {
		return
	}
	b.fake(evMotionNotify, 0, int16(px), int16(py))
	switch kind {
	case "tap":
		b.click(1, 1)
	case "down":
		b.fake(evButtonPress, 1, 0, 0)
	case "move":
		// The motion above is the move; a held button makes it a drag.
	case "up":
		b.fake(evButtonRelease, 1, 0, 0)
	case "wheel":
		// Buttons 4 and 5 are wheel up and down, one notch per click; about 40 pixels a notch.
		n := max(1, abs(dy)/40)
		if dy > 0 {
			b.click(5, n)
		} else {
			b.click(4, n)
		}
	default:
		out.Event("error", "unsupported", "input kind "+kind)
	}
	// Push the requests out now: a touch should not wait for the next one.
	xproto.GetInputFocus(b.c).Reply()
}

// bringToFront raises the window and checks that it is what is under the pointer: synthetic
// clicks go to whatever is on top, and a click on the wrong window would be worse than none.
func (b *backend) bringToFront(w xproto.Window, px, py int, out *helperkit.Out) bool {
	top := b.topLevel(w)
	xproto.ConfigureWindow(b.c, top, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	// Ask the window manager to focus it as a pager would (EWMH _NET_ACTIVE_WINDOW, source 2).
	ev := xproto.ClientMessageEvent{
		Format: 32, Window: w, Type: b.atom("_NET_ACTIVE_WINDOW"),
		Data: xproto.ClientMessageDataUnionData32New([]uint32{2, 0, 0, 0, 0}),
	}
	xproto.SendEvent(b.c, false, b.root, xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify, string(ev.Bytes()))
	for range 5 {
		r, err := xproto.TranslateCoordinates(b.c, b.root, b.root, int16(px), int16(py)).Reply()
		if err == nil && r.Child == top {
			return true
		}
		time.Sleep(30 * time.Millisecond)
	}
	out.Event("error", "internal", "the window is covered by another one; the touch was dropped")
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func main() {
	fmt.Fprintln(os.Stderr, "deckcap-linux starting")
	helperkit.Run(&backend{})
}
