package main

import (
	"encoding/binary"
	"testing"
	"time"
)

type clk struct{ t time.Time }

func (c *clk) now() time.Time { return c.t }

func tone(frames int, v int16) []byte {
	b := make([]byte, 0, frames*4)
	for i := 0; i < frames*2; i++ {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}

func newTest() (*playout, *clk) {
	c := &clk{t: time.Unix(100, 0)}
	p := newPlayout()
	p.now, p.start = c.now, c.t
	return p, c
}

func TestNothingIsPlacedBeforeTheClock(t *testing.T) {
	p, _ := newTest()
	p.audio(0, tone(960, 1000))
	if s := p.stats(); s.Chunks != 0 {
		t.Errorf("%+v", s)
	}
}

func TestChunkWaitsForItsTime(t *testing.T) {
	p, c := newTest()
	p.clock(0) // the server's clock reads 0 now: offset 0
	// Stamped now: heard 300 ms from now, so 300 ms of silence goes in first.
	p.audio(0, tone(960, 1000))
	s := p.stats()
	if s.Chunks != 1 || s.Silence != 300 {
		t.Fatalf("%+v", s)
	}
	buf := make([]byte, 4*48*300) // 300 ms
	p.Read(buf)
	for _, v := range buf {
		if v != 0 {
			t.Fatal("the gap is not silence")
		}
	}
	c.t = c.t.Add(300 * time.Millisecond)
	buf = make([]byte, 3840)
	p.Read(buf)
	if binary.LittleEndian.Uint16(buf) != 1000 {
		t.Error("the chunk did not follow the gap")
	}
}

func TestContinuousChunksAddNoSilence(t *testing.T) {
	p, c := newTest()
	p.clock(0)
	for i := 0; i < 5; i++ {
		p.audio(int64(i)*20_000, tone(960, 5))
		c.t = c.t.Add(20 * time.Millisecond)
		p.clock(int64(i+1) * 20_000)
	}
	if s := p.stats(); s.Chunks != 5 || s.Silence != 300 {
		t.Errorf("%+v: only the first chunk's wait is silence", s)
	}
}

func TestLateAndFarChunksAreDropped(t *testing.T) {
	p, c := newTest()
	p.clock(0)
	c.t = c.t.Add(time.Second)
	p.audio(0, tone(960, 1))         // due at 300 ms, now 1000: 700 ms late
	p.audio(5_000_000, tone(960, 1)) // 4 s ahead
	p.audio(1_000_000, tone(7, 1))   // 7 frames is fine: whole frames
	p.audio(1_000_000, []byte{1, 2, 3})
	s := p.stats()
	if s.Late != 1 || s.Dropped != 2 || s.Chunks != 1 {
		t.Errorf("%+v", s)
	}
}

func TestOffsetIsTheSmallestDifference(t *testing.T) {
	p, c := newTest()
	c.t = c.t.Add(500 * time.Millisecond)
	p.clock(0) // arrived 500 ms after its stamp
	c.t = c.t.Add(100 * time.Millisecond)
	p.clock(500_000) // 100 ms after its stamp: a truer reading
	if p.offset != 100_000 {
		t.Errorf("offset %d, want the smaller, 0.1 s", p.offset)
	}
}

func TestUnderrunIsCountedOnce(t *testing.T) {
	p, c := newTest()
	p.setup([]byte(`{"latency_ms":100}`))
	p.clock(0)
	p.audio(0, tone(960, 1))
	c.t = c.t.Add(100 * time.Millisecond)
	buf := make([]byte, 100_000)
	for i := 0; i < 3; i++ {
		p.Read(buf)
	}
	if s := p.stats(); s.Underrun != 1 {
		t.Errorf("%+v", s)
	}
}
