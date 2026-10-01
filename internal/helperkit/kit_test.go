package helperkit

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestScale(t *testing.T) {
	// 4x2 picture, left half black, right half white, alpha 0 as window grabs leave it.
	src := make([]byte, 4*2*4)
	for i := 0; i < 8; i++ {
		v := byte(0)
		if i%4 >= 2 {
			v = 200
		}
		src[i*4], src[i*4+1], src[i*4+2] = v, v, v
	}
	for _, tc := range []struct {
		name      string
		maxw      int
		w, h      int
		first, lt byte
	}{
		{"copy when it fits", 960, 4, 2, 0, 200},
		{"halved averages pairs", 2, 2, 1, 0, 200},
		{"one pixel wide mixes", 1, 1, 0, 100, 100},
	} {
		got, w, h := Scale(src, 4, 2, 16, tc.maxw)
		if tc.name == "one pixel wide mixes" {
			if w != 1 || h != 1 || got[0] != 100 || got[3] != 255 {
				t.Errorf("%s: %dx%d %v", tc.name, w, h, got)
			}
			continue
		}
		if w != tc.w || h != tc.h || got[0] != tc.first || got[(w-1)*4] != tc.lt || got[3] != 255 {
			t.Errorf("%s: %dx%d %v", tc.name, w, h, got)
		}
	}
	if d, _, _ := Scale(src[:10], 4, 2, 16, 4); d != nil {
		t.Error("short buffer was accepted")
	}
}

func TestMapPoint(t *testing.T) {
	for _, tc := range []struct{ x, y, wx, wy int }{
		{0, 0, 0, 0},
		{480, 240, 960, 480}, // the frame is half the window
		{-5, 9999, 0, 958},   // clipped to the frame, then mapped
		{959, 479, 1918, 958},
	} {
		if x, y := MapPoint(tc.x, tc.y, 960, 480, 1920, 960); x != tc.wx || y != tc.wy {
			t.Errorf("%d,%d mapped to %d,%d, want %d,%d", tc.x, tc.y, x, y, tc.wx, tc.wy)
		}
	}
}

type fake struct {
	mu      sync.Mutex
	calls   []string
	started StartArgs
}

func (f *fake) rec(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fake) List() ([]Window, error) {
	return []Window{{ID: "7", Title: "t", App: "a", W: 300, H: 200}}, nil
}
func (f *fake) Start(out *Out, a StartArgs) error {
	f.rec("start")
	f.started = a
	if a.Window == "gone" {
		return &Error{"no_window", "gone"}
	}
	out.Frame(1, 1, []byte{1, 2, 3, 255})
	out.Frame(1, 1, []byte{1, 2, 3, 255}) // identical: dropped
	return nil
}
func (f *fake) Input(k string, x, y, dy int) { f.rec("input " + k) }
func (f *fake) Stop()                        { f.rec("stop") }

func TestServe(t *testing.T) {
	var out bytes.Buffer
	f := &fake{}
	in := strings.Join([]string{
		`{"cmd":"list"}`,
		`{"cmd":"start","window":"7"}`,
		`{"cmd":"input","kind":"tap","x":1,"y":2}`,
		`{"cmd":"start","window":"gone"}`,
		`{"cmd":"nope"}`,
		`garbage`,
		`{"cmd":"stop"}`,
	}, "\n")
	serve(f, strings.NewReader(in), NewOut(&out))

	var kinds []byte
	var events []string
	r := out.Bytes()
	for len(r) > 0 {
		n := binary.LittleEndian.Uint32(r)
		k, p := r[4], r[5:5+n]
		r = r[5+n:]
		kinds = append(kinds, k)
		if k == kindEvent {
			var e map[string]string
			json.Unmarshal(p, &e)
			events = append(events, e["event"]+":"+e["code"])
		}
	}
	// ready, list, started, frame (one: the duplicate is dropped), error no_window, two unsupported, stopped
	want := []byte{kindEvent, kindWindows, kindFrame, kindEvent, kindEvent, kindEvent, kindEvent, kindEvent}
	if !bytes.Equal(kinds, want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	wantEv := "ready: started: error:no_window error:unsupported error:unsupported stopped:"
	if got := strings.Join(events, " "); got != wantEv {
		t.Errorf("events %q, want %q", got, wantEv)
	}
	if f.started.FPS != 25 || f.started.MaxW != 960 {
		t.Errorf("defaults not applied: %+v", f.started)
	}
}
