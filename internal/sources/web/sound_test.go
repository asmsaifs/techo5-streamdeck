package web

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// tonePage plays a 440 Hz tone as soon as it loads, and has a link that loads another page, which
// plays a tone of its own: a full navigation must not end the capture.
func tonePage(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	tone := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="background:#123">`+r.URL.Path+`<script>
const c = new AudioContext(); const o = c.createOscillator(); o.frequency.value = 440;
const g = c.createGain(); g.gain.value = 0.5; o.connect(g); g.connect(c.destination); o.start(); c.resume();
</script>`)
	}
	mux.HandleFunc("/", tone)
	return httptest.NewServer(mux)
}

type pcmSink struct {
	mu    sync.Mutex
	bytes int
	peak  int
	odd   int
}

func (p *pcmSink) write(pcm []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bytes += len(pcm)
	if len(pcm)%4 != 0 {
		p.odd++
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		p.peak = max(p.peak, int(math.Abs(float64(v))))
	}
}

func (p *pcmSink) get() (bytes, peak, odd int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bytes, p.peak, p.odd
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 150; i++ {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("never saw %s", what)
}

// A page whose content security policy refuses a blob: script, as YouTube's does, cannot load an
// audio worklet: its sound is still captured.
func TestSoundSurvivesAStrictContentSecurityPolicy(t *testing.T) {
	m := browserOrSkip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "script-src 'self' 'unsafe-inline'")
		fmt.Fprint(w, `<!doctype html><body style="background:#123"><script>
const c = new AudioContext(); const o = c.createOscillator(); o.frequency.value = 440;
o.connect(c.destination); o.start(); c.resume();</script>`)
	}))
	defer srv.Close()
	s, err := m.New(Spec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var got pcmSink
	s.SetSound(got.write)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a second of sound", func() bool { b, _, _ := got.get(); return b >= 48000*4 })
	if _, peak, odd := got.get(); peak < 4000 || odd != 0 {
		t.Errorf("peak %d, %d odd blocks", peak, odd)
	}
}

func TestSourceSendsThePagesSound(t *testing.T) {
	m := browserOrSkip(t)
	srv := tonePage(t)
	defer srv.Close()

	s, err := m.New(Spec{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var got pcmSink
	s.SetSound(got.write)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a second of sound", func() bool { b, _, _ := got.get(); return b >= 48000*4 })
	b, peak, odd := got.get()
	if odd != 0 {
		t.Errorf("%d blocks were not whole frames", odd)
	}
	// A 0.5 tone is about 16000 on a full scale of 32767; silence would be 0.
	if peak < 4000 {
		t.Errorf("peak %d of %d bytes: the sound is silent", peak, b)
	}

	// Navigating the page to another document goes on capturing.
	before := b
	if err := s.navigate(srv.URL + "/second"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sound after a full navigation", func() bool { b, _, _ := got.get(); return b >= before+48000*4 })
}

func TestSoundIsNotPassedOnUnlessItIsForTheShow(t *testing.T) {
	m := browserOrSkip(t)
	srv := tonePage(t)
	defer srv.Close()
	for _, tt := range []struct {
		name string
		spec Spec
		set  bool // whether the source is given somewhere to send it
	}{
		{"nowhere to send it", Spec{URL: srv.URL}, false},
		{"off", Spec{URL: srv.URL, Sound: SoundOff}, true},
		{"desktop", Spec{URL: srv.URL, Sound: SoundDesktop}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, err := m.New(tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			var got pcmSink
			if tt.set {
				s.SetSound(got.write)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := s.Start(ctx, image.Pt(960, 480)); err != nil {
				t.Fatal(err)
			}
			settled(t, s, "the page", func(*image.RGBA) bool { return true })
			time.Sleep(2 * time.Second)
			if b, _, _ := got.get(); b != 0 {
				t.Errorf("%d bytes of sound were passed on", b)
			}
		})
	}
}
