package audio

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ChunkFrames is one chunk: 20 ms at 48 kHz, the size docs/protocol.md asks for.
	ChunkFrames = Rate / 50
	chunkBytes  = ChunkFrames * Channels * 2
	chunkLen    = 20 * time.Millisecond

	// maxLag is how far the stamps may fall behind real time before they are moved forward. A
	// source that stalled and then caught up in a burst would otherwise be stamped in the past,
	// and the Show drops what is late: seconds of sound. A pause and then playing on is better
	// (the cast protocol's "Stalls" rule).
	maxLag = 300 * time.Millisecond
)

// Sink is where chunks go: wire.Sender for a Show, a fake in tests.
type Sink interface {
	Setup(latencyMs int) error
	Clock(nowUs int64) error
	Audio(stampUs int64, pcm []byte) error
}

// Options tunes a Stream. The zero value is usable.
type Options struct {
	// Latency is how long after its stamp the Show plays a chunk, and how far ahead of real time
	// chunks may be stamped. Default 300 ms.
	Latency time.Duration
	// AVOffset is added to every stamp: positive delays the sound so that it meets a picture that
	// takes that long to arrive (docs/protocol.md, "Pictures stay unstamped").
	AVOffset time.Duration
	// ClockEvery is how often the clock message goes. Default 1 s.
	ClockEvery time.Duration
	// Now stands in for the clock, so a test can hold time still. Default time.Now.
	Now func() time.Time
}

// Stats are counters since the Stream began.
type Stats struct {
	Sent    int64 // chunks written to the sink
	Dropped int64 // chunks thrown out of a full queue: the Show or the network fell behind
	Skipped int64 // chunks not stamped because the source delivered faster than real time
	Jumps   int64 // times the timeline was moved forward after a stall
}

// Stream turns whatever PCM a source writes into stamped 20 ms chunks and sends them, with the
// clock messages the Show needs to place them. Write may be called from a capture goroutine; Run
// does the sending, so a slow socket never blocks the capture.
type Stream struct {
	sink  Sink
	o     Options
	epoch time.Time
	queue chan chunk

	mu      sync.Mutex // guards the fields below, which Write alone touches
	in      Format
	rs      *Resampler
	pending []byte
	next    time.Duration // the stamp of the next chunk, on the stream's clock
	started bool

	sent, dropped, skipped, jumps atomic.Int64
}

type chunk struct {
	us  int64
	pcm []byte
}

func New(sink Sink, o Options) *Stream {
	if o.Latency <= 0 {
		o.Latency = 300 * time.Millisecond
	}
	if o.ClockEvery <= 0 {
		o.ClockEvery = time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Stream{
		sink: sink, o: o, epoch: o.Now(),
		// Room for the latency's worth of chunks and a little over: a fuller queue means the sink is
		// behind, and the oldest chunk is the one least worth sending.
		queue: make(chan chunk, int(o.Latency/chunkLen)+2),
	}
}

func (s *Stream) clock() time.Duration { return s.o.Now().Sub(s.epoch) }

// Write takes PCM in format f, converts it, cuts it into chunks and queues them. It never blocks.
func (s *Stream) Write(f Format, pcm []byte) {
	if !f.Valid() || len(pcm) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rs == nil || s.in != f {
		s.in, s.rs = f, NewResampler(f)
	}
	if f == Wire {
		// Already what the Show plays: whole frames only, no interpolation.
		s.pending = append(s.pending, pcm[:len(pcm)-len(pcm)%4]...)
	} else {
		s.pending = append(s.pending, s.rs.Convert(pcm)...)
	}
	for len(s.pending) >= chunkBytes {
		s.stampAndQueue(s.pending[:chunkBytes:chunkBytes])
		s.pending = s.pending[chunkBytes:]
	}
	if len(s.pending) == 0 {
		s.pending = nil // let the backing array go
	}
}

// stampAndQueue gives a chunk its time. Wants mu.
func (s *Stream) stampAndQueue(pcm []byte) {
	now := s.clock()
	if !s.started || s.next < now-maxLag {
		if s.started {
			s.jumps.Add(1)
		}
		s.next, s.started = now, true
	}
	if s.next > now+s.o.Latency {
		s.skipped.Add(1) // faster than real time: the Show may be sent no further ahead than this
		return
	}
	c := chunk{us: (s.next + s.o.AVOffset).Microseconds(), pcm: append([]byte(nil), pcm...)}
	s.next += chunkLen
	for {
		select {
		case s.queue <- c:
			return
		default:
			select {
			case <-s.queue:
				s.dropped.Add(1)
			default:
			}
		}
	}
}

// Run sends the latency and the clock, then chunks as they come and the clock every ClockEvery,
// until ctx ends or the sink fails, and returns why. The clock goes first, before any audio: the
// Show drops audio it cannot place.
func (s *Stream) Run(ctx context.Context) error {
	if err := s.sink.Setup(int(s.o.Latency / time.Millisecond)); err != nil {
		return err
	}
	if err := s.sink.Clock(s.clock().Microseconds()); err != nil {
		return err
	}
	tick := time.NewTicker(s.o.ClockEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := s.sink.Clock(s.clock().Microseconds()); err != nil {
				return err
			}
		case c := <-s.queue:
			if err := s.sink.Audio(c.us, c.pcm); err != nil {
				return err
			}
			s.sent.Add(1)
		}
	}
}

func (s *Stream) Stats() Stats {
	return Stats{s.sent.Load(), s.dropped.Load(), s.skipped.Load(), s.jumps.Load()}
}
