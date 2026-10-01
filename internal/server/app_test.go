package server

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"image"
	"os"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/capture"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	appsrc "github.com/asmsaifs/techo5-streamdeck/internal/sources/app"
)

// TestMain lets this test binary stand in for the capture helper: a window of one solid blue
// 400x400 picture, which is all the test needs from it.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_HELPER") != "" {
		fakeHelper()
		return
	}
	os.Exit(m.Run())
}

func helperMsg(kind byte, p []byte) {
	h := make([]byte, 5)
	binary.LittleEndian.PutUint32(h, uint32(len(p)))
	h[4] = kind
	os.Stdout.Write(append(h, p...))
}

func helperEvent(e capture.Event) { b, _ := json.Marshal(e); helperMsg(4, b) }

func fakeHelper() {
	helperEvent(capture.Event{Event: "ready"})
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c capture.Command
		json.Unmarshal(sc.Bytes(), &c)
		switch c.Cmd {
		case "list":
			b, _ := json.Marshal([]capture.Window{{ID: "7", App: "Notes", Title: "Todo", W: 400, H: 400}})
			helperMsg(1, b)
		case "start":
			f := make([]byte, 4+400*400*4)
			binary.LittleEndian.PutUint16(f, 400)
			binary.LittleEndian.PutUint16(f[2:], 400)
			for i := 4; i < len(f); i += 4 {
				f[i], f[i+1], f[i+2], f[i+3] = 255, 0, 0, 255 // BGRA blue
			}
			helperMsg(2, f)
		}
	}
}

// A button that shows an app's window takes the Show to the helper's picture, centred on black.
func TestAppWindowOnTheShow(t *testing.T) {
	t.Setenv("FAKE_HELPER", "1")
	exe, _ := os.Executable()
	cfg, err := deck.Decode([]byte(`{"version":1,"server":{"key":"` + key + `"},"profiles":{"default":{"pages":{
		"home":{"buttons":{"0,0":{"label":"Notes","action":{"type":"stream.app","app":"notes"}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Fill()
	srv, addr := startServer(t, cfg, &runner{})
	srv.App = &appsrc.Manager{Helper: exe}
	sh := connect(t, addr, key, "Kitchen")
	sh.settle()

	p := cfg.Profiles["default"]
	r := render.NewLayout(p.Grid, image.Pt(960, 480)).Rect(deck.Cell{})
	sh.tap((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)

	deadline := time.Now().Add(20 * time.Second)
	for {
		scr := sh.settle()
		if closeTo(colourAt(scr, 480, 240), [3]int{0, 0, 255}) {
			if !closeTo(colourAt(scr, 100, 240), [3]int{0, 0, 0}) {
				t.Errorf("left bar is %v, want black", colourAt(scr, 100, 240))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window never reached the Show: %v at the centre", colourAt(scr, 480, 240))
		}
	}
	if got := srv.Sessions(); len(got) != 1 || got[0].Source != "app" {
		t.Errorf("sessions %+v, want one showing the app", got)
	}
}
