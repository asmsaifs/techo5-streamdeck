package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"
)

// The Show's side of the audio1 extension (docs/protocol.md), as much of it as matters for trying a
// server: the clock offset taken the way the device takes it (arrival minus stamp, the smallest of
// the last ten), a chunk heard at stamp + offset + latency, late chunks dropped, a gap of silence
// where the stream has one. What it does not copy is the device's drift correction, since a laptop
// sound card and the Show's differ in different ways.
const (
	clockWindow = 10
	lateMax     = 100 * time.Millisecond // as the device's cast receiver
	farMax      = 2 * time.Second
	defaultLat  = 300 * time.Millisecond
	frameBytes  = 4
	soundRate   = 48000
)

// playout takes the audio messages and is the io.Reader a sound card pulls from: S16LE stereo,
// silence whenever nothing is queued, so the player never runs dry.
type playout struct {
	// now stands in for the clock, so a test can hold time still.
	now   func() time.Time
	start time.Time

	mu      sync.Mutex
	latency time.Duration
	samples [clockWindow]int64
	n       int
	offset  int64
	known   bool
	buf     []byte
	playing bool // there was audio in the buffer at the last read

	st soundStats
}

// soundStats is what a run is judged by.
type soundStats struct {
	Chunks   int     // chunks queued
	Late     int     // chunks dropped for being past their time
	Dropped  int     // chunks dropped for a stamp from nowhere near now, or not whole frames
	Underrun int     // times the queue ran dry after playing
	Silence  int     // milliseconds of silence put in for gaps
	Peak     float64 // loudest sample so far, 0 to 1
	Clocks   int
}

func newPlayout() *playout {
	return &playout{now: time.Now, start: time.Now(), latency: defaultLat}
}

// reset is for a new connection: nothing carries over from the last one.
func (p *playout) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.start = p.now()
	p.latency, p.n, p.known, p.buf, p.playing = defaultLat, 0, false, nil, false
	p.st = soundStats{}
}

func (p *playout) nowUs() int64 { return p.now().Sub(p.start).Microseconds() }

// setup takes a kindSetup message.
func (p *playout) setup(body []byte) {
	var m struct {
		LatencyMs int `json:"latency_ms"`
	}
	if json.Unmarshal(body, &m) != nil || m.LatencyMs <= 0 {
		return
	}
	p.mu.Lock()
	p.latency = min(max(time.Duration(m.LatencyMs)*time.Millisecond, 100*time.Millisecond), time.Second)
	p.mu.Unlock()
}

// clock takes the server's clock, in µs.
func (p *playout) clock(us int64) {
	d := p.nowUs() - us
	p.mu.Lock()
	defer p.mu.Unlock()
	p.samples[p.n%clockWindow] = d
	p.n++
	lo := d
	for i := 0; i < min(p.n, clockWindow); i++ {
		lo = min(lo, p.samples[i])
	}
	p.offset, p.known = lo, true
	p.st.Clocks++
}

// audio takes one chunk stamped us.
func (p *playout) audio(us int64, pcm []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.known {
		return // nothing can be placed before the first clock message
	}
	if len(pcm) == 0 || len(pcm)%frameBytes != 0 {
		p.st.Dropped++
		return
	}
	now := p.nowUs()
	due := us + p.offset + p.latency.Microseconds()
	switch {
	case now-due > lateMax.Microseconds():
		p.st.Late++
		return
	case due-now > farMax.Microseconds():
		p.st.Dropped++
		return
	}
	// Where the end of what is queued will be heard: now if nothing is, else after it.
	end := now + int64(len(p.buf)/frameBytes)*1_000_000/soundRate
	if gap := due - end; gap > 20_000 {
		p.buf = append(p.buf, make([]byte, int(gap)*soundRate/1_000_000*frameBytes)...)
		p.st.Silence += int(gap / 1000)
	}
	p.buf = append(p.buf, pcm...)
	p.st.Chunks++
	for i := 0; i+1 < len(pcm); i += 2 {
		v := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i:])))) / 32768
		p.st.Peak = max(p.st.Peak, v)
	}
}

// Read is what the sound card pulls: queued audio, then silence.
func (p *playout) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	if n > 0 {
		p.playing = true
	} else if p.playing {
		p.playing = false
		p.st.Underrun++
	}
	clear(b[n:])
	return len(b), nil
}

func (p *playout) stats() soundStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

func (s soundStats) String() string {
	return fmt.Sprintf("audio: %d chunks, peak %.2f, %d late, %d dropped, %d underruns, %d ms of silence, %d clocks",
		s.Chunks, s.Peak, s.Late, s.Dropped, s.Underrun, s.Silence, s.Clocks)
}
