package sendspin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	"github.com/gorilla/websocket"
	"github.com/mewkiz/flac"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
)

// fakeShow is a Sendspin player built from the same client the Show's echod uses
// (sendspin-go's protocol.Client), so a handshake it accepts is one the Show accepts.
type fakeShow struct {
	t       *testing.T
	srv     *httptest.Server
	formats []protocol.AudioFormat
	busy    atomic.Bool
	dials   atomic.Int32

	starts chan protocol.StreamStart
	chunks chan protocol.AudioChunk
	cmds   chan protocol.PlayerCommand
	ends   chan struct{}
}

var (
	flacFormat = protocol.AudioFormat{Codec: "flac", Channels: 2, SampleRate: 48000, BitDepth: 16}
	pcmFormat  = protocol.AudioFormat{Codec: "pcm", Channels: 2, SampleRate: 48000, BitDepth: 16}
	opusFormat = protocol.AudioFormat{Codec: "opus", Channels: 2, SampleRate: 48000, BitDepth: 16}
)

func newFakeShow(t *testing.T, formats ...protocol.AudioFormat) *fakeShow {
	f := &fakeShow{
		t: t, formats: formats,
		starts: make(chan protocol.StreamStart, 8),
		chunks: make(chan protocol.AudioChunk, 1000),
		cmds:   make(chan protocol.PlayerCommand, 16),
		ends:   make(chan struct{}, 8),
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.dials.Add(1)
		if f.busy.Load() {
			http.Error(w, "already connected", http.StatusConflict)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		c := protocol.NewClientFromConn(protocol.Config{
			Name: "Kitchen Show", Version: 1, ClientID: "aa:bb",
			SupportedRoles:  []string{"player@v1"},
			PlayerV1Support: protocol.PlayerV1Support{SupportedFormats: f.formats, SupportedCommands: []string{"volume", "mute"}},
		}, conn)
		if err := c.Start(); err != nil {
			return
		}
		defer c.Close()
		// The burst the Show runs as soon as it is connected.
		go func() {
			for range 8 {
				if c.SendTimeSync(time.Now().UnixMicro()) != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-c.Done():
				return
			case <-c.TimeSyncResp:
			case s := <-c.StreamStart:
				f.starts <- s
			case ch := <-c.AudioChunks:
				f.chunks <- ch
			case cmd := <-c.ControlMsgs:
				f.cmds <- cmd
			case <-c.StreamEnd:
				f.ends <- struct{}{}
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeShow) url() string { return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/sendspin" }

func (f *fakeShow) group(o Options) *Group {
	o.Name, o.ID = "Office Mac", "test"
	o.Resolve = func(context.Context, string) (string, error) { return f.url(), nil }
	g := NewGroup([]string{"Kitchen Show"}, o)
	f.t.Cleanup(g.Close)
	return g
}

// tone is ms of a 440 Hz sine at about -12 dBFS, as wire PCM.
func tone(ms int) []byte {
	n := audio.Rate * ms / 1000
	b := make([]byte, n*4)
	for i := range n {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/audio.Rate))
		binary.LittleEndian.PutUint16(b[i*4:], uint16(v))
		binary.LittleEndian.PutUint16(b[i*4+2:], uint16(v))
	}
	return b
}

func silence(ms int) []byte { return make([]byte, audio.Rate*ms/1000*4) }

// feed writes pcm 20 ms at a time, at the pace a sound device delivers it.
func feed(g *Group, pcm []byte) {
	for len(pcm) > 0 {
		n := min(len(pcm), chunkBytes)
		g.Write(audio.Wire, pcm[:n])
		pcm = pcm[n:]
		time.Sleep(chunkLen)
	}
}

func within[T any](t *testing.T, ch <-chan T, d time.Duration, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		t.Fatalf("no %s within %v", what, d)
	}
	var zero T
	return zero
}

func TestDialPicksCodec(t *testing.T) {
	tests := []struct {
		name    string
		formats []protocol.AudioFormat
		codec   string
		err     error
	}{
		{"flac first", []protocol.AudioFormat{flacFormat, opusFormat, pcmFormat}, "flac", nil},
		{"pcm when there is no flac", []protocol.AudioFormat{opusFormat, pcmFormat}, "pcm", nil},
		{"opus only", []protocol.AudioFormat{opusFormat}, "", errNoFormat},
		{"flac at another rate", []protocol.AudioFormat{{Codec: "flac", Channels: 2, SampleRate: 44100, BitDepth: 16}}, "", errNoFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeShow(t, tt.formats...)
			c, err := Dial(context.Background(), f.url(), DialOptions{Name: "Office Mac", Clock: func() int64 { return time.Now().UnixMicro() }})
			if !errors.Is(err, tt.err) {
				t.Fatalf("Dial: %v, want %v", err, tt.err)
			}
			if err != nil {
				return
			}
			defer c.Close()
			if c.Codec() != tt.codec || c.Name() != "Kitchen Show" {
				t.Errorf("codec %q name %q, want %q Kitchen Show", c.Codec(), c.Name(), tt.codec)
			}
			s := within(t, f.starts, time.Second, "stream/start")
			if s.Player == nil || s.Player.Codec != tt.codec || s.Player.SampleRate != 48000 || s.Player.Channels != 2 || s.Player.BitDepth != 16 {
				t.Errorf("stream/start %+v", s.Player)
			}
			if (s.Player.CodecHeader != "") != (tt.codec == "flac") {
				t.Errorf("codec header %q for %s", s.Player.CodecHeader, tt.codec)
			}
		})
	}
}

// What the Show decodes is what was sent, sample for sample, in either codec.
func TestChunksDecode(t *testing.T) {
	for _, format := range []protocol.AudioFormat{flacFormat, pcmFormat} {
		t.Run(format.Codec, func(t *testing.T) {
			f := newFakeShow(t, format)
			c, err := Dial(context.Background(), f.url(), DialOptions{Clock: func() int64 { return 0 }})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			s := within(t, f.starts, time.Second, "stream/start")
			sent := tone(100)
			for i := 0; i < len(sent); i += chunkBytes {
				c.Write(int64(i), sent[i:i+chunkBytes])
			}
			var got []byte
			for range 5 {
				got = append(got, within(t, f.chunks, time.Second, "chunk").Data...)
			}
			if format.Codec == "flac" {
				got = decodeFLAC(t, s.Player.CodecHeader, got)
			}
			if !bytes.Equal(got, sent) {
				t.Errorf("decoded %d bytes that differ from the %d sent", len(got), len(sent))
			}
		})
	}
}

func decodeFLAC(t *testing.T, header string, frames []byte) []byte {
	t.Helper()
	h, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatal(err)
	}
	st, err := flac.New(io.MultiReader(bytes.NewReader(h), bytes.NewReader(frames)))
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for {
		fr, err := st.ParseNext()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		for i := range int(fr.BlockSize) {
			for c := range 2 {
				out = binary.LittleEndian.AppendUint16(out, uint16(int16(fr.Subframes[c].Samples[i])))
			}
		}
	}
}

// The chunks follow on 20 ms apart and are stamped a lead ahead of the Group's clock.
func TestStampsRise(t *testing.T) {
	f := newFakeShow(t, flacFormat)
	g := f.group(Options{Lead: 200 * time.Millisecond})
	go feed(g, tone(1000))
	var prev int64
	for i := range 20 {
		c := within(t, f.chunks, 2*time.Second, "chunk")
		if ahead := time.Duration(c.Timestamp-g.Clock()) * time.Microsecond; ahead < 50*time.Millisecond || ahead > 250*time.Millisecond {
			t.Errorf("chunk %d arrived %v ahead of its time, want about the 200 ms lead", i, ahead)
		}
		if i > 0 && c.Timestamp-prev != 20000 {
			t.Errorf("chunk %d is %d µs after the one before, want 20000", i, c.Timestamp-prev)
		}
		prev = c.Timestamp
	}
}

func TestGate(t *testing.T) {
	tests := []struct {
		name  string
		sound []byte
		dials bool
	}{
		{"silence", silence(300), false},
		{"a click", append(tone(60), silence(240)...), false},
		{"sound", tone(300), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeShow(t, flacFormat)
			g := f.group(Options{Idle: 400 * time.Millisecond})
			feed(g, tt.sound)
			if !tt.dials {
				time.Sleep(100 * time.Millisecond)
				if n := f.dials.Load(); n != 0 {
					t.Fatalf("dialed %d times", n)
				}
				if g.State().Open {
					t.Error("the gate is open")
				}
				return
			}
			within(t, f.starts, time.Second, "stream/start")
			if st := g.State(); !st.Open || len(st.Playing) != 1 || st.Playing[0] != "Kitchen Show" {
				t.Errorf("while playing: %+v", st)
			}
			// Then silence: the Show is let go after Idle, with stream/end.
			go feed(g, silence(1000))
			within(t, f.ends, 2*time.Second, "stream/end")
			time.Sleep(50 * time.Millisecond)
			if st := g.State(); st.Open || len(st.Playing) != 0 {
				t.Errorf("after the silence: %+v", st)
			}
		})
	}
}

// A busy Show is reported, and asked again only when the sound next starts.
func TestBusyOnce(t *testing.T) {
	f := newFakeShow(t, flacFormat)
	f.busy.Store(true)
	var changes atomic.Int32
	g := f.group(Options{Idle: 300 * time.Millisecond, Changed: func() { changes.Add(1) }})
	feed(g, tone(800))
	if st := g.State(); len(st.Busy) != 1 || st.Busy[0] != "Kitchen Show" || len(st.Failed) != 0 {
		t.Errorf("state %+v, want Kitchen Show busy", st)
	}
	if n := f.dials.Load(); n != 1 {
		t.Errorf("dialed %d times while busy, want 1", n)
	}
	feed(g, silence(600))
	if st := g.State(); st.Open || len(st.Busy) != 0 {
		t.Errorf("after silence: %+v", st)
	}
	f.busy.Store(false)
	feed(g, tone(200))
	within(t, f.starts, time.Second, "stream/start")
	if n := f.dials.Load(); n != 2 {
		t.Errorf("dialed %d times in all, want 2", n)
	}
}

func TestVolume(t *testing.T) {
	f := newFakeShow(t, flacFormat)
	g := f.group(Options{})
	g.SetVolume(0.5, false) // before the Show connects: sent once it does
	go feed(g, tone(500))
	want := []protocol.PlayerCommand{{Command: "volume", Volume: 50}, {Command: "mute", Mute: false}}
	for _, w := range want {
		if got := within(t, f.cmds, 2*time.Second, "command"); got != w {
			t.Errorf("got %+v, want %+v", got, w)
		}
	}
	g.SetVolume(0.5, true) // only the mute changed
	if got := within(t, f.cmds, time.Second, "command"); got != (protocol.PlayerCommand{Command: "mute", Mute: true}) {
		t.Errorf("got %+v, want the mute", got)
	}
	select {
	case got := <-f.cmds:
		t.Errorf("an extra command %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNoVolumeUntilSet(t *testing.T) {
	f := newFakeShow(t, flacFormat)
	g := f.group(Options{})
	feed(g, tone(300))
	within(t, f.starts, time.Second, "stream/start")
	select {
	case got := <-f.cmds:
		t.Errorf("the Show's own volume was overridden: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestFinderResolve(t *testing.T) {
	looks := 0
	f := &Finder{Browse: func(context.Context, time.Duration, string) ([]Player, error) {
		looks++
		return []Player{{Name: "Kitchen Show", URL: "ws://10.0.0.2:8928/sendspin"}}, nil
	}}
	tests := []struct {
		name, show, url string
		looks           int
	}{
		{"first look", "Kitchen Show", "ws://10.0.0.2:8928/sendspin", 1},
		{"remembered", "Kitchen Show", "ws://10.0.0.2:8928/sendspin", 1},
		{"unknown looks again", "Office Show", "", 2},
	}
	for _, tt := range tests {
		url, err := f.Resolve(context.Background(), tt.show)
		if url != tt.url || (err != nil) != (tt.url == "") || looks != tt.looks {
			t.Errorf("%s: %q, %v after %d looks; want %q after %d", tt.name, url, err, looks, tt.url, tt.looks)
		}
	}
	f.Forget("Kitchen Show")
	if _, err := f.Resolve(context.Background(), "Kitchen Show"); err != nil || looks != 3 {
		t.Errorf("after Forget: %v, %d looks", err, looks)
	}
}

// The cost of FLAC per Show: one 20 ms chunk of music-like sound.
func BenchmarkFLACEncode(b *testing.B) {
	e, err := newFLACEncoder()
	if err != nil {
		b.Fatal(err)
	}
	pcm := tone(20)
	b.SetBytes(int64(len(pcm)))
	var size int
	for b.Loop() {
		out, err := e.encode(pcm)
		if err != nil {
			b.Fatal(err)
		}
		size = len(out)
	}
	b.ReportMetric(float64(size), "bytes/chunk")
}
