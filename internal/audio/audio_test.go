package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

func s16(vals ...int16) []byte {
	var b []byte
	for _, v := range vals {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}

func TestResampler(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   Format
		pcm  []byte
		want []byte
	}{
		{"48k stereo comes through, minus the held first frame",
			Wire, s16(1, 2, 3, 4, 5, 6), s16(1, 2, 3, 4)},
		{"mono is copied to both channels", Format{48000, 1}, s16(10, 20, 30), s16(10, 10, 20, 20)},
		{"surplus channels are dropped", Format{48000, 3}, s16(1, 2, 9, 3, 4, 9, 5, 6, 9), s16(1, 2, 3, 4)},
		{"24k doubles, interpolating", Format{24000, 1}, s16(0, 100, 200),
			s16(0, 0, 50, 50, 100, 100, 150, 150)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewResampler(tt.in).Convert(tt.pcm); !bytes.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResamplerLengthAndSplitting(t *testing.T) {
	// One second of a ramp at 44.1 kHz mono.
	in := make([]byte, 0, 44100*2)
	for i := 0; i < 44100; i++ {
		in = binary.LittleEndian.AppendUint16(in, uint16(int16(i%2000)))
	}
	whole := NewResampler(Format{44100, 1}).Convert(in)
	if frames := len(whole) / 4; frames < Rate-3 || frames > Rate+1 {
		t.Errorf("a second in gave %d frames, want about %d", frames, Rate)
	}
	// Cut anywhere, the output is the same.
	r := NewResampler(Format{44100, 1})
	var split []byte
	for off := 0; off < len(in); off += 882 { // 441-frame pieces
		split = append(split, r.Convert(in[off:min(off+882, len(in))])...)
	}
	if !bytes.Equal(whole, split) {
		t.Error("output depends on where the input was cut")
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type got struct {
	kind string
	us   int64
	n    int
}

type fakeSink struct {
	mu   sync.Mutex
	got  []got
	hold chan struct{} // when non-nil, Audio waits for it to close
}

func (f *fakeSink) add(g got) { f.mu.Lock(); f.got = append(f.got, g); f.mu.Unlock() }
func (f *fakeSink) Setup(ms int) error {
	f.add(got{"setup", int64(ms), 0})
	return nil
}
func (f *fakeSink) Clock(us int64) error { f.add(got{"clock", us, 0}); return nil }
func (f *fakeSink) Audio(us int64, p []byte) error {
	if f.hold != nil {
		<-f.hold
	}
	f.add(got{"audio", us, len(p)})
	return nil
}
func (f *fakeSink) all() []got { f.mu.Lock(); defer f.mu.Unlock(); return append([]got(nil), f.got...) }

func silence(frames int) []byte { return make([]byte, frames*4) }

// drain runs the stream until it has sent want chunks, and returns what the sink saw.
func drain(t *testing.T, s *Stream, sink *fakeSink, want int) []got {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	for i := 0; i < 500; i++ {
		if int(s.Stats().Sent) >= want {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	return sink.all()
}

func TestChunksAreStampedEvery20ms(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	sink := &fakeSink{}
	s := New(sink, Options{Now: clk.Now})
	clk.Advance(time.Second)
	s.Write(Wire, silence(ChunkFrames*5+100)) // 5 chunks and a part
	g := drain(t, s, sink, 5)
	if len(g) < 7 || g[0].kind != "setup" || g[0].us != 300 || g[1].kind != "clock" {
		t.Fatalf("the Show must get the latency and the clock first: %v", g)
	}
	for i, c := range g[2:7] {
		if c.kind != "audio" || c.n != 3840 || c.us != 1_000_000+int64(i)*20_000 {
			t.Errorf("chunk %d = %+v", i, c)
		}
	}
	// The part left over joins the next write.
	s.Write(Wire, silence(ChunkFrames-100))
	if g = drain(t, s, sink, 6); g[len(g)-1].us != 1_100_000 || s.Stats().Sent != 6 {
		t.Errorf("the remainder was not completed: %v", g)
	}
}

func TestAVOffsetDelaysStamps(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	sink := &fakeSink{}
	s := New(sink, Options{Now: clk.Now, AVOffset: 120 * time.Millisecond})
	s.Write(Wire, silence(ChunkFrames))
	if g := drain(t, s, sink, 1); g[2].us != 120_000 {
		t.Errorf("stamp = %d, want 120000", g[2].us)
	}
}

func TestAStallMovesTheTimelineForward(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	sink := &fakeSink{}
	s := New(sink, Options{Now: clk.Now})
	s.Write(Wire, silence(ChunkFrames))
	clk.Advance(5 * time.Second) // the source went quiet, then delivers a burst
	s.Write(Wire, silence(ChunkFrames))
	g := drain(t, s, sink, 2)
	if g[3].us != 5_000_000 || s.Stats().Jumps != 1 {
		t.Errorf("after a stall the stamp = %d (jumps %d), want it at now", g[3].us, s.Stats().Jumps)
	}
}

func TestNothingIsStampedFurtherAheadThanTheLatency(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	sink := &fakeSink{}
	s := New(sink, Options{Now: clk.Now})
	s.Write(Wire, silence(Rate)) // a second at once: 50 chunks, 16 fit (0 to 300 ms ahead)
	st := s.Stats()
	if st.Skipped != 34 {
		t.Errorf("skipped %d, want 34", st.Skipped)
	}
	g := drain(t, s, sink, 16)
	if last := g[len(g)-1].us; last > 300_000 {
		t.Errorf("last stamp %d is more than the latency ahead", last)
	}
}

func TestASlowSinkLosesTheOldestChunks(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	sink := &fakeSink{hold: make(chan struct{})}
	s := New(sink, Options{Now: clk.Now})
	for i := 0; i < 40; i++ { // real time passes as chunks are made, so none is "too far ahead"
		s.Write(Wire, silence(ChunkFrames))
		clk.Advance(chunkLen)
	}
	if s.Stats().Dropped == 0 {
		t.Fatal("a queue nobody reads did not drop anything")
	}
	close(sink.hold)
	g := drain(t, s, sink, cap(s.queue))
	// What survives is the newest: the last chunk made is the last one sent.
	if last := g[len(g)-1]; last.us != 39*20_000 {
		t.Errorf("last stamp = %d, want the newest chunk (%d)", last.us, 39*20_000)
	}
}

func TestClockIsRepeated(t *testing.T) {
	sink := &fakeSink{}
	s := New(sink, Options{ClockEvery: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	n := 0
	for _, g := range sink.all() {
		if g.kind == "clock" {
			n++
		}
	}
	if n < 4 {
		t.Errorf("%d clock messages in 80 ms at 10 ms apart", n)
	}
}
