package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// show is the stream side of a Show: it connects as techo5/echod/internal/feature/dashboard/stream.go
// does, paints the pictures into a frame as they come, and sends touches. It knows nothing of the
// window, so it runs the same with one (main.go) or without (-shot).
type show struct {
	server, key string
	hello       wire.Hello

	// pictures counts every picture painted, for -shot to wait on; it is signalled on painted.
	painted chan struct{}

	mu      sync.Mutex
	frame   *image.RGBA
	problem string
	version uint64 // counts pictures, so the window can tell a new one from the last
	conn    net.Conn
	enc     *json.Encoder
	stats   stats

	// sound is the Show's audio, when the hello advertises audio1; nil otherwise.
	sound *playout
}

// stats is what came in, for the window's title.
type stats struct {
	messages, bytes int
}

func newShow(server, key string, h wire.Hello) *show {
	s := &show{server: server, key: key, hello: h, painted: make(chan struct{}, 1),
		frame: image.NewRGBA(image.Rect(0, 0, h.W, h.H))}
	if h.Has(wire.CapAudio) {
		s.sound = newPlayout()
	}
	return s
}

// run connects and reconnects until ctx ends, waiting longer each time it fails, as the device does.
func (s *show) run(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		err := s.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Info("stream", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

func (s *show) setProblem(text string) {
	s.mu.Lock()
	s.problem = text
	s.mu.Unlock()
}

func (s *show) once(ctx context.Context) error {
	raw, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", s.server)
	if err != nil {
		s.setProblem("Can't reach the dashboard server at " + s.server + ".")
		return err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := wire.ClientHandshake(raw, s.key)
	if err != nil {
		s.setProblem("The dashboard server did not accept this device's key.")
		return err
	}
	_ = raw.SetDeadline(time.Time{})
	enc := json.NewEncoder(c)
	if err := enc.Encode(s.hello); err != nil {
		return err
	}
	s.mu.Lock()
	s.conn, s.enc, s.problem = c, enc, ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.conn, s.enc = nil, nil
		s.mu.Unlock()
	}()
	if s.sound != nil {
		s.sound.reset() // a new connection starts the device's audio over
	}
	slog.Info("connected", "server", s.server)

	r := bufio.NewReaderSize(c, 256<<10)
	for {
		m, err := wire.ReadMsg(r)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.stats.messages++
		s.stats.bytes += len(m.Data) + 5
		s.mu.Unlock()
		switch m.Kind {
		case wire.KindPicture:
			img, err := decodeWithin(m.Data, s.hello.W, s.hello.H)
			if err != nil {
				slog.Warn("a picture that does not decode", "err", err)
				continue
			}
			s.paint(m.At, img)
		case wire.KindHalf:
			img, err := decodeWithin(m.Data, s.hello.W/2+1, s.hello.H/2+1)
			if err != nil {
				slog.Warn("a half picture that does not decode", "err", err)
				continue
			}
			s.paintDoubled(m.At, img)
		case wire.KindProblem:
			s.setProblem(string(m.Data))
		case wire.KindSetup, wire.KindClock, wire.KindAudio:
			if s.sound == nil {
				slog.Warn("sound sent to a Show that did not advertise it", "kind", m.Kind)
				continue
			}
			s.soundMsg(m)
		default:
			// The device ignores kinds it does not know; a server that sends one did not check the
			// hello's caps, which is worth seeing.
			slog.Warn("a message kind this Show never advertised", "kind", m.Kind)
		}
	}
}

// soundMsg hands kinds 4 to 6 to the audio.
func (s *show) soundMsg(m wire.Msg) {
	if m.Kind == wire.KindSetup {
		s.sound.setup(m.Data)
		return
	}
	if len(m.Data) < 8 {
		return
	}
	us := int64(binary.BigEndian.Uint64(m.Data))
	if m.Kind == wire.KindClock {
		s.sound.clock(us)
		return
	}
	s.sound.audio(us, m.Data[8:])
}

// touch sends one touch line, given a moment at most, as the device does.
func (s *show) touch(t wire.Touch) {
	s.mu.Lock()
	enc, c := s.enc, s.conn
	s.mu.Unlock()
	if enc == nil {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = enc.Encode(t)
}

// decodeWithin decodes a JPEG no larger than w by h, as the device does: it refuses a larger one,
// so a server that sends one is caught here rather than on a Show.
func decodeWithin(b []byte, w, h int) (image.Image, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > w || cfg.Height > h {
		return nil, errors.New("a picture larger than the screen")
	}
	return jpeg.Decode(bytes.NewReader(b))
}

func (s *show) paint(at image.Point, img image.Image) {
	b := img.Bounds()
	s.mu.Lock()
	draw.Draw(s.frame, image.Rectangle{Min: at, Max: at.Add(b.Size())}, img, b.Min, draw.Src)
	s.version++
	s.mu.Unlock()
	s.signal()
}

// paintDoubled is the device's: each pixel of the half-size picture as four, clipped to the frame.
func (s *show) paintDoubled(at image.Point, img image.Image) {
	b := img.Bounds()
	small := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(small, small.Rect, img, b.Min, draw.Src)
	w, h := s.hello.W, s.hello.H
	if at.X < 0 || at.Y < 0 || at.X >= w || at.Y >= h {
		return
	}
	s.mu.Lock()
	f := s.frame
	for y := 0; y < small.Rect.Dy(); y++ {
		ty := at.Y + 2*y
		if ty+1 >= h {
			break
		}
		src := small.Pix[y*small.Stride : y*small.Stride+small.Rect.Dx()*4]
		r0 := f.Pix[ty*f.Stride : (ty+1)*f.Stride]
		for x := 0; x*4 < len(src); x++ {
			tx := at.X + 2*x
			if tx+1 >= w {
				break
			}
			p := src[x*4 : x*4+4]
			o := tx * 4
			copy(r0[o:o+4], p)
			copy(r0[o+4:o+8], p)
		}
		lo, hi := at.X*4, min(at.X+2*small.Rect.Dx(), w)*4
		if hi > lo {
			copy(f.Pix[(ty+1)*f.Stride+lo:(ty+1)*f.Stride+hi], r0[lo:hi])
		}
	}
	s.version++
	s.mu.Unlock()
	s.signal()
}

func (s *show) signal() {
	select {
	case s.painted <- struct{}{}:
	default:
	}
}

// snapshot is a copy of the frame and what goes with it, for drawing outside the lock.
func (s *show) snapshot(into *image.RGBA, since uint64) (version uint64, problem string, connected bool, st stats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if into != nil && s.version != since {
		copy(into.Pix, s.frame.Pix)
	}
	return s.version, s.problem, s.conn != nil, s.stats
}
