package screen

import (
	"bufio"
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

type msg struct {
	half bool
	r    image.Rectangle // where it lands, in full-size pixels
}

type fakeSink struct {
	mu   sync.Mutex
	msgs []msg
	err  error
}

func (f *fakeSink) add(half bool, at image.Point, jpg []byte) error {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		return err
	}
	w, h := cfg.Width, cfg.Height
	if half {
		w, h = w*2, h*2
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, msg{half, image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}})
	return nil
}
func (f *fakeSink) Picture(at image.Point, jpg []byte) error { return f.add(false, at, jpg) }
func (f *fakeSink) Half(at image.Point, jpg []byte) error    { return f.add(true, at, jpg) }

func (f *fakeSink) take() []msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.msgs
	f.msgs = nil
	return m
}

func frame(w, h int, c uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = c
	}
	return img
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

var red = color.RGBA{255, 0, 0, 255}

const settle = 40 * time.Millisecond

func newEnc() (*Encoder, *fakeSink) {
	s := &fakeSink{}
	return NewEncoder(s, Options{Settle: settle}), s
}

func TestFirstFrameIsWholeAndSharp(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	if err := e.Send(frame(960, 480, 40)); err != nil {
		t.Fatal(err)
	}
	got := s.take()
	if len(got) != 1 || got[0].half || got[0].r != image.Rect(0, 0, 960, 480) {
		t.Fatalf("first frame sent %+v", got)
	}
	time.Sleep(3 * settle)
	if got := s.take(); len(got) != 0 {
		t.Errorf("a sharp first frame was followed by %+v", got)
	}
}

func TestIdenticalFrameSendsNothing(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	e.Send(frame(960, 480, 40))
	s.take()
	if err := e.Send(frame(960, 480, 40)); err != nil {
		t.Fatal(err)
	}
	if got := s.take(); len(got) != 0 {
		t.Errorf("an unchanged frame sent %+v", got)
	}
}

// One changed cell is one rectangle that covers it, at full size, and nothing follows.
func TestOneChangedCellIsOneRect(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	a := frame(960, 480, 40)
	e.Send(a)
	s.take()
	b := frame(960, 480, 40)
	cell := image.Rect(198, 165, 381, 315)
	fill(b, cell, red)
	if err := e.Send(b); err != nil {
		t.Fatal(err)
	}
	got := s.take()
	if len(got) != 1 || got[0].half {
		t.Fatalf("sent %+v", got)
	}
	if !cell.In(got[0].r) {
		t.Errorf("%v does not cover the changed cell %v", got[0].r, cell)
	}
	// Tile aligned, so no more than one tile of slack on each side.
	if got[0].r.Dx() > cell.Dx()+2*tile || got[0].r.Dy() > cell.Dy()+2*tile {
		t.Errorf("%v is much bigger than the cell", got[0].r)
	}
	time.Sleep(3 * settle)
	if more := s.take(); len(more) != 0 {
		t.Errorf("followed by %+v", more)
	}
}

func TestChangesFarApartStayApart(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	e.Send(frame(320, 160, 40))
	s.take()
	b := frame(320, 160, 40)
	b.SetRGBA(2, 2, red)
	b.SetRGBA(300, 150, red)
	b.SetRGBA(20, 2, red) // near the first: one rect
	e.Send(b)
	if got := s.take(); len(got) != 2 {
		t.Fatalf("changes at two corners: %+v", got)
	}
}

// A big change goes at half size, and the full-size rectangles follow once, after the settle time.
func TestMotionGoesHalfThenSharpens(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	e.Send(frame(960, 480, 40))
	s.take()
	b := frame(960, 480, 40)
	area := image.Rect(0, 0, 960, 288) // 60 %
	fill(b, area, red)
	if err := e.Send(b); err != nil {
		t.Fatal(err)
	}
	got := s.take()
	if len(got) != 1 || !got[0].half || got[0].r != area {
		t.Fatalf("a 60%% change sent %+v", got)
	}
	time.Sleep(settle / 4)
	if more := s.take(); len(more) != 0 {
		t.Fatalf("full size came before the settle time: %+v", more)
	}
	deadline := time.Now().Add(2 * time.Second)
	var sharp []msg
	for len(sharp) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		sharp = s.take()
	}
	if len(sharp) != 1 || sharp[0].half || sharp[0].r != area {
		t.Fatalf("after settling: %+v", sharp)
	}
	time.Sleep(3 * settle)
	if more := s.take(); len(more) != 0 {
		t.Errorf("sharpened twice: %+v", more)
	}
}

// While the screen keeps moving the full-size pictures keep waiting.
func TestSharpenWaitsForTheLastMovingFrame(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	e.Send(frame(960, 480, 40))
	s.take()
	start := time.Now()
	for i := 0; i < 4; i++ {
		e.Send(frame(960, 480, uint8(100+i*30)))
		time.Sleep(settle / 2)
	}
	for _, m := range s.take() {
		if !m.half {
			t.Fatalf("a full-size picture %v while moving, %v in", m.r, time.Since(start))
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.take(); len(got) > 0 {
			if got[0].half || got[0].r != image.Rect(0, 0, 960, 480) {
				t.Fatalf("sharpened with %+v", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("never sharpened")
}

func TestCloseCancelsSharpen(t *testing.T) {
	e, s := newEnc()
	e.Send(frame(960, 480, 40))
	e.Send(frame(960, 480, 200))
	s.take()
	e.Close()
	time.Sleep(3 * settle)
	if got := s.take(); len(got) != 0 {
		t.Errorf("sent after Close: %+v", got)
	}
}

func TestResizeAndInvalidateSendEverything(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	e.Send(frame(960, 480, 40))
	s.take()
	e.Send(frame(1280, 800, 40))
	if got := s.take(); len(got) != 1 || got[0].half || got[0].r != image.Rect(0, 0, 1280, 800) {
		t.Errorf("after a resize: %+v", got)
	}
	e.Invalidate()
	e.Send(frame(1280, 800, 40))
	if got := s.take(); len(got) != 1 || got[0].r != image.Rect(0, 0, 1280, 800) {
		t.Errorf("after Invalidate: %+v", got)
	}
}

func TestErrorsAreReported(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	s.err = errors.New("gone")
	if err := e.Send(frame(64, 64, 1)); err == nil {
		t.Error("a write error was swallowed")
	}
	// One met by the settle timer comes out of the next Send.
	s.err = nil
	e.Send(frame(960, 480, 40))
	e.Send(frame(960, 480, 200))
	s.mu.Lock()
	s.err = errors.New("gone")
	s.mu.Unlock()
	time.Sleep(4 * settle)
	if err := e.Send(frame(960, 480, 40)); err == nil {
		t.Error("an error from the settle timer was lost")
	}
}

// Odd sizes: the doubled half picture must stay inside the screen.
func TestHalfStaysOnTheScreen(t *testing.T) {
	e, s := newEnc()
	defer e.Close()
	e.Send(frame(101, 61, 40))
	s.take()
	e.Send(frame(101, 61, 200))
	for _, m := range s.take() {
		if !m.r.In(image.Rect(0, 0, 102, 62)) {
			t.Errorf("%v is off the screen", m.r)
		}
	}
}

func TestShrinkAverages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{100, 0, 0, 255})
	img.SetRGBA(0, 1, color.RGBA{100, 0, 0, 255})
	img.SetRGBA(1, 1, color.RGBA{200, 0, 0, 255})
	if out := shrink(img); out.Bounds().Dx() != 1 || out.RGBAAt(0, 0).R != 100 {
		t.Errorf("shrink = %v %v", out.Bounds(), out.RGBAAt(0, 0))
	}
}

func TestMerge(t *testing.T) {
	r := image.Rect
	got := merge(nil, r(0, 0, 16, 16))
	got = merge(got, r(24, 0, 40, 16)) // less than a tile away: joined
	got = merge(got, r(60, 0, 76, 16)) // a tile and more away: apart
	got = merge(got, r(200, 200, 216, 216))
	if len(got) != 3 {
		t.Errorf("merge = %v", got)
	}
}

// The encoder's output reads back through the real wire framing, as a device would read it.
func TestOverTheWire(t *testing.T) {
	var buf bytes.Buffer
	e := NewEncoder(wire.NewSender(&buf), Options{Settle: settle})
	defer e.Close()
	if err := e.Send(frame(960, 480, 40)); err != nil {
		t.Fatal(err)
	}
	m, err := wire.ReadMsg(bufio.NewReader(&buf))
	if err != nil || m.Kind != wire.KindPicture || m.At != (image.Point{}) {
		t.Fatalf("%+v %v", m, err)
	}
	img, err := jpeg.Decode(bytes.NewReader(m.Data))
	if err != nil || img.Bounds().Size() != image.Pt(960, 480) {
		t.Errorf("%v %v", img, err)
	}
}
