package capture

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestMain doubles as the fake helper: the tests run this binary again with FAKE_HELPER set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_HELPER"); mode != "" {
		fakeHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeHelper(mode string) {
	out := os.Stdout
	ev := func(e Event) { b, _ := json.Marshal(e); writeMsg(out, KindEvent, b) }
	switch mode {
	case "garbage":
		out.Write([]byte{0xff, 0xff, 0xff, 0xff, 9})
		return
	case "silent":
		return // exits without ready
	}
	ev(Event{Event: "ready"})
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c Command
		json.Unmarshal(sc.Bytes(), &c)
		switch c.Cmd {
		case "list":
			b, _ := json.Marshal([]Window{{ID: "7", Title: "Notes", App: "notes", W: 800, H: 600}})
			writeMsg(out, KindWindows, b)
		case "start":
			ev(Event{Event: "started", Msg: c.Window})
			f := make([]byte, 4+2*2*4)
			binary.LittleEndian.PutUint16(f, 2)
			binary.LittleEndian.PutUint16(f[2:], 2)
			f[4] = 0xAB
			writeMsg(out, KindFrame, f)
			a := make([]byte, 5+8)
			binary.LittleEndian.PutUint32(a, 44100)
			a[4] = 2
			writeMsg(out, KindAudio, a)
		case "input":
			ev(Event{Event: "error", Code: "input:" + c.Kind, Msg: c.Window})
		case "crash":
			os.Exit(3)
		}
	}
}

func newFake(t *testing.T, mode string) (*Client, context.CancelFunc, chan error) {
	t.Helper()
	t.Setenv("FAKE_HELPER", mode)
	exe, _ := os.Executable()
	c := New(exe)
	c.Backoff = func(int) time.Duration { return 10 * time.Millisecond }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(cancel)
	return c, cancel, done
}

func waitEvent(t *testing.T, c *Client, name string) Event {
	t.Helper()
	for {
		select {
		case e := <-c.Events():
			if e.Event == name {
				return e
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q event", name)
		}
	}
}

func TestListStartAndStreams(t *testing.T) {
	c, _, _ := newFake(t, "ok")
	waitEvent(t, c, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := c.List(ctx)
	if err != nil || len(w) != 1 || w[0].Title != "Notes" || w[0].W != 800 {
		t.Fatalf("List = %+v, %v", w, err)
	}
	if err := c.Start("7", 25, 960, true); err != nil {
		t.Fatal(err)
	}
	if e := waitEvent(t, c, "started"); e.Msg != "7" {
		t.Errorf("started for %q", e.Msg)
	}
	select {
	case f := <-c.Frames():
		if f.W != 2 || f.H != 2 || f.BGRA[0] != 0xAB {
			t.Errorf("frame = %+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}
	select {
	case a := <-c.Audio():
		if a.Rate != 44100 || a.Channels != 2 || len(a.PCM) != 8 {
			t.Errorf("audio = %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no audio")
	}
	if err := c.Input("tap", 1, 2); err != nil {
		t.Fatal(err)
	}
	if e := waitEvent(t, c, "error"); e.Code != "input:tap" {
		t.Errorf("input event = %+v", e)
	}
}

func TestRestartReplaysStart(t *testing.T) {
	c, _, _ := newFake(t, "ok")
	waitEvent(t, c, "ready")
	if err := c.Start("9", 25, 960, false); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, c, "started")
	c.send(Command{Cmd: "crash"})
	waitEvent(t, c, "restart")
	// The new helper must be told to capture again without the caller doing anything.
	if e := waitEvent(t, c, "started"); e.Msg != "9" {
		t.Errorf("replayed start for %q", e.Msg)
	}
}

func TestStopForgetsCapture(t *testing.T) {
	c, _, _ := newFake(t, "ok")
	waitEvent(t, c, "ready")
	c.Start("9", 25, 960, false)
	waitEvent(t, c, "started")
	c.Stop()
	c.send(Command{Cmd: "crash"})
	waitEvent(t, c, "restart")
	waitEvent(t, c, "ready")
	select {
	case e := <-c.Events():
		if e.Event == "started" {
			t.Error("a stopped capture was started again")
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestGivesUp(t *testing.T) {
	for _, mode := range []string{"garbage", "silent"} {
		_, _, done := newFake(t, mode)
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: Run returned nil", mode)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: never gave up", mode)
		}
	}
}

func TestDecode(t *testing.T) {
	for name, tc := range map[string]struct {
		p   []byte
		bad bool
		f   func([]byte) error
	}{
		"frame ok":      {[]byte{1, 0, 1, 0, 1, 2, 3, 4}, false, func(p []byte) error { _, e := decodeFrame(p); return e }},
		"frame short":   {[]byte{1, 0, 1, 0, 1, 2, 3}, true, func(p []byte) error { _, e := decodeFrame(p); return e }},
		"frame zero":    {[]byte{0, 0, 1, 0}, true, func(p []byte) error { _, e := decodeFrame(p); return e }},
		"audio ok":      {[]byte{0x80, 0xbb, 0, 0, 2, 1, 2, 3, 4}, false, func(p []byte) error { _, e := decodeAudio(p); return e }},
		"audio partial": {[]byte{0x80, 0xbb, 0, 0, 2, 1, 2, 3}, true, func(p []byte) error { _, e := decodeAudio(p); return e }},
		"audio no ch":   {[]byte{0x80, 0xbb, 0, 0, 0}, true, func(p []byte) error { _, e := decodeAudio(p); return e }},
	} {
		if err := tc.f(tc.p); (err != nil) != tc.bad {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
