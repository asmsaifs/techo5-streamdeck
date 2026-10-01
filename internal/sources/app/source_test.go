package app

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/capture"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// The tests run this binary again as the capture helper (FAKE_HELPER set).
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_HELPER") != "" {
		fakeHelper()
		return
	}
	os.Exit(m.Run())
}

func msg(kind byte, p []byte) {
	h := make([]byte, 5)
	binary.LittleEndian.PutUint32(h, uint32(len(p)))
	h[4] = kind
	os.Stdout.Write(append(h, p...))
}

func event(e capture.Event) { b, _ := json.Marshal(e); msg(4, b) }

func fakeHelper() {
	event(capture.Event{Event: "ready"})
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c capture.Command
		json.Unmarshal(sc.Bytes(), &c)
		switch c.Cmd {
		case "list":
			b, _ := json.Marshal([]capture.Window{
				{ID: "1", App: "Notes", Title: "Todo", W: 300, H: 300},
				{ID: "2", App: "Notes", Title: "Big list", W: 400, H: 400},
				{ID: "3", App: "Mail", Title: "Inbox", W: 900, H: 900},
				{ID: "9", App: "Gone", Title: "Closing", W: 400, H: 400},
			})
			msg(1, b)
		case "start":
			if c.Window == "9" {
				event(capture.Event{Event: "error", Code: "no_window", Msg: "gone"})
				continue
			}
			// A 400x400 red picture (BGRA) and a bit of audio.
			f := make([]byte, 4+400*400*4)
			binary.LittleEndian.PutUint16(f, 400)
			binary.LittleEndian.PutUint16(f[2:], 400)
			for i := 4; i < len(f); i += 4 {
				f[i], f[i+1], f[i+2], f[i+3] = 0, 0, 255, 255
			}
			msg(3, append([]byte{0x80, 0xbb, 0, 0, 2}, make([]byte, 8)...))
			event(capture.Event{Event: "started", Msg: fmt.Sprintf("%s maxw=%d", c.Window, c.MaxW)})
			msg(2, f)
		case "input":
			event(capture.Event{Event: "input", Msg: fmt.Sprintf("%s %d %d %d", c.Kind, c.X, c.Y, c.DY)})
		}
	}
}

var evs = make(chan capture.Event, 64)

func start(t *testing.T, spec Spec) *Source {
	t.Helper()
	t.Setenv("FAKE_HELPER", "1")
	for len(evs) > 0 {
		<-evs
	}
	exe, _ := os.Executable()
	s, err := (&Manager{Helper: exe}).New(spec)
	if err != nil {
		t.Fatal(err)
	}
	s.seen = func(e capture.Event) {
		select {
		case evs <- e:
		default:
		}
	}
	if err := s.Start(context.Background(), image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func frame(t *testing.T, s *Source) *image.RGBA {
	t.Helper()
	select {
	case f, ok := <-s.Frames():
		if !ok {
			t.Fatalf("source ended: %v", s.Err())
		}
		return f
	case <-time.After(10 * time.Second):
		t.Fatal("no frame")
	}
	return nil
}

// helperEvent waits for the fake helper's event of that name.
func helperEvent(t *testing.T, s *Source, name string) string {
	t.Helper()
	for {
		select {
		case e := <-evs:
			if e.Event == name {
				return e.Msg
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no %s event", name)
		}
	}
}

func TestShowsLargestMatchLetterboxed(t *testing.T) {
	s := start(t, Spec{App: "notes"})
	img := frame(t, s)
	if img.Bounds().Size() != image.Pt(960, 480) {
		t.Fatalf("size %v", img.Bounds())
	}
	// 400x400 fits the screen, so it is not scaled: centred, red from x=280 to 680, y=40 to 440.
	red := img.RGBAAt(480, 240)
	if red.R != 255 || red.G != 0 || red.B != 0 {
		t.Errorf("centre = %v", red)
	}
	if bar := img.RGBAAt(10, 10); bar.R != 0 || bar.A != 255 {
		t.Errorf("bar = %v", bar)
	}
	if img.RGBAAt(270, 240).R != 0 || img.RGBAAt(690, 240).R != 0 || img.RGBAAt(480, 30).R != 0 {
		t.Error("the picture spills over its box")
	}
}

func TestTouchMapping(t *testing.T) {
	s := start(t, Spec{App: "notes"})
	frame(t, s)
	// The 400x400 frame sits at x 280..680, y 40..440.
	s.Touch(wire.Touch{T: "tap", X: 480, Y: 240})
	if m := helperEvent(t, s, "input"); m != "tap 200 200 0" {
		t.Errorf("centre tap = %q", m)
	}
	s.Touch(wire.Touch{T: "tap", X: 100, Y: 100}) // on the bar: nothing
	s.Touch(wire.Touch{T: "tap", X: 280, Y: 40})
	if m := helperEvent(t, s, "input"); m != "tap 0 0 0" {
		t.Errorf("corner tap = %q (the bar's tap must not have been sent)", m)
	}
}

func TestDragLeavingThePictureStillLifts(t *testing.T) {
	s := start(t, Spec{App: "notes"})
	frame(t, s)
	s.Touch(wire.Touch{T: "down", X: 480, Y: 240})
	helperEvent(t, s, "input")
	s.Touch(wire.Touch{T: "up", X: 5, Y: 5})
	if m := helperEvent(t, s, "input"); m != "up 200 200 0" {
		t.Errorf("lift = %q", m)
	}
}

func TestScrollMode(t *testing.T) {
	s := start(t, Spec{App: "notes", Scroll: true})
	frame(t, s)
	s.Touch(wire.Touch{T: "down", X: 480, Y: 300})
	s.Touch(wire.Touch{T: "move", X: 480, Y: 252}) // finger up 48 px: scrolls down 48
	if m := helperEvent(t, s, "input"); m != "wheel 200 212 48" {
		t.Errorf("wheel = %q", m)
	}
	s.Touch(wire.Touch{T: "up", X: 480, Y: 252})
}

func TestPick(t *testing.T) {
	list := []capture.Window{{ID: "1", App: "Notes", Title: "Todo", W: 300, H: 300}, {ID: "2", App: "Notes", Title: "Big list", W: 400, H: 400}, {ID: "3", App: "Mail", Title: "Inbox", W: 900, H: 900}}
	for _, tc := range []struct {
		spec Spec
		want string
	}{
		{Spec{App: "notes"}, "2"},
		{Spec{App: "NOTES", Title: "todo"}, "1"},
		{Spec{Title: "^inbox$"}, "3"},
		{Spec{App: "calc"}, ""},
	} {
		s, err := (&Manager{}).New(tc.spec)
		if err != nil {
			t.Fatal(err)
		}
		w, ok := s.Pick(list)
		if (tc.want != "") != ok || w.ID != tc.want {
			t.Errorf("%+v picked %q, %v", tc.spec, w.ID, ok)
		}
	}
	if _, err := (&Manager{}).New(Spec{}); err == nil {
		t.Error("an empty spec was accepted")
	}
	if _, err := (&Manager{}).New(Spec{App: "x", Title: "("}); err == nil {
		t.Error("a bad regexp was accepted")
	}
}

func TestNoWindowEndsWithError(t *testing.T) {
	s := start(t, Spec{App: "calc"})
	select {
	case _, ok := <-s.Frames():
		if ok {
			t.Fatal("got a frame")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never ended")
	}
	if err := s.Err(); err == nil || err.Error() != "there is no window of calc" {
		t.Errorf("Err = %v", err)
	}
}

func TestClosedWindowEnds(t *testing.T) {
	s := start(t, Spec{App: "gone"})
	select {
	case _, ok := <-s.Frames():
		if ok {
			t.Fatal("got a frame")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never ended")
	}
	if err := s.Err(); err == nil || err.Error() != "the window was closed" {
		t.Errorf("Err = %v", err)
	}
}
