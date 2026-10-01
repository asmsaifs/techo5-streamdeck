// Package screen turns a stream of frames into what goes over the wire: only the rectangles that
// changed, as JPEGs, and at half size while most of the screen is moving.
//
// The logic is dashcast's (techo5/dashcast/serve.go), made independent of Chrome so the deck,
// website tiles and window capture can all use it.
package screen

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"
	"time"
)

// Sink is where pictures go: wire.Sender, or a fake in tests.
type Sink interface {
	// Picture draws a JPEG with its top left at at; Half draws one at half size, doubled.
	Picture(at image.Point, jpg []byte) error
	Half(at image.Point, jpg []byte) error
}

// Options tune an Encoder. Zero values mean the defaults.
type Options struct {
	Quality int           // JPEG quality; 85
	Soft    float64       // the fraction of the screen that must change in one frame to go half size; 0.35
	Settle  time.Duration // how long after the last half-size frame the full-size ones follow; 250 ms
}

func (o Options) filled() Options {
	if o.Quality == 0 {
		o.Quality = 85
	}
	if o.Soft == 0 {
		o.Soft = 0.35
	}
	if o.Settle == 0 {
		o.Settle = 250 * time.Millisecond
	}
	return o
}

// tile is the grain changes are found at: fine enough that a clock ticking is a small picture,
// coarse enough that comparing is cheap.
const tile = 16

// Encoder remembers the last frame it sent and sends what is different in the next. Use one per
// connection. It is safe for concurrent use, though frames should come from one goroutine.
type Encoder struct {
	out Sink
	opt Options

	mu   sync.Mutex
	last *image.RGBA

	// blurred is what went at half size and is still owed at full size; sharpen is the timer that
	// sends it once the screen settles, and gen tells a timer that was stopped too late that it was.
	blurred []image.Rectangle
	sharpen *time.Timer
	gen     int
	err     error // the first error the settle timer met, for the next Send to report
}

func NewEncoder(out Sink, opt Options) *Encoder { return &Encoder{out: out, opt: opt.filled()} }

// Send sends what changed from the last frame to img: all of it the first time or when the size
// changes, nothing when nothing changed. img is copied, so the caller may draw on it again.
func (e *Encoder) Send(img image.Image) error {
	b := img.Bounds()
	cur := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(cur, cur.Rect, img, b.Min, draw.Src)

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.err; err != nil {
		e.err = nil
		return err
	}
	prev := e.last
	e.last = cur
	if prev == nil || prev.Rect != cur.Rect {
		// A whole new picture is sent sharp: half size and then full size again would only be
		// the same pixels twice.
		e.cancelSharpen()
		e.blurred = nil
		return e.picture(cur.Rect)
	}
	rects := changes(prev, cur)
	if len(rects) == 0 {
		return nil
	}

	area := 0
	for _, r := range rects {
		area += r.Dx() * r.Dy()
	}
	moving := float64(area) > e.opt.Soft*float64(cur.Rect.Dx()*cur.Rect.Dy())
	if !moving {
		for _, r := range rects {
			if err := e.picture(r); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range rects {
		// Even edges, so the doubled picture lands on the pixels it came from.
		r = image.Rect(r.Min.X&^1, r.Min.Y&^1, min((r.Max.X+1)&^1, cur.Rect.Max.X), min((r.Max.Y+1)&^1, cur.Rect.Max.Y))
		e.blurred = merge(e.blurred, r)
		if err := e.half(r); err != nil {
			return err
		}
	}
	e.cancelSharpen()
	gen := e.gen
	e.sharpen = time.AfterFunc(e.opt.Settle, func() { e.sharpenNow(gen) })
	return nil
}

// Close stops the full-size pictures that are still owed: the connection is going away.
func (e *Encoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancelSharpen()
	e.blurred = nil
}

// Invalidate forgets the last frame, so the next Send is the whole screen. For a device whose
// screen is no longer what the encoder thinks it is.
func (e *Encoder) Invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.last = nil
}

func (e *Encoder) cancelSharpen() {
	e.gen++
	if e.sharpen != nil {
		e.sharpen.Stop()
		e.sharpen = nil
	}
}

// sharpenNow sends at full size what went at half, now the screen has stopped moving.
func (e *Encoder) sharpenNow(gen int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if gen != e.gen || e.last == nil {
		return
	}
	rects := e.blurred
	e.blurred = nil
	for _, r := range rects {
		if err := e.picture(r); err != nil {
			e.err = err
			return
		}
	}
}

func (e *Encoder) picture(r image.Rectangle) error {
	jpg, err := e.encode(e.last.SubImage(r))
	if err != nil {
		return err
	}
	return e.out.Picture(r.Min, jpg)
}

func (e *Encoder) half(r image.Rectangle) error {
	jpg, err := e.encode(shrink(e.last.SubImage(r)))
	if err != nil {
		return err
	}
	return e.out.Half(r.Min, jpg)
}

func (e *Encoder) encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: e.opt.Quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shrink is img at half size, each pixel the average of the four it stands for.
func shrink(img image.Image) *image.RGBA {
	b := img.Bounds()
	src := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(src, src.Rect, img, b.Min, draw.Src)
	w, h := b.Dx()/2, b.Dy()/2
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i, j := src.PixOffset(2*x, 2*y), src.PixOffset(2*x, 2*y+1)
			o := out.PixOffset(x, y)
			for c := 0; c < 4; c++ {
				sum := int(src.Pix[i+c]) + int(src.Pix[i+4+c]) + int(src.Pix[j+c]) + int(src.Pix[j+4+c])
				out.Pix[o+c] = uint8((sum + 2) / 4)
			}
		}
	}
	return out
}

// changes is the parts of cur that differ from prev, as few rectangles as cover them. Changes
// far apart - the clock at the top, a sensor at the bottom - are separate rectangles rather than
// one that spans both.
func changes(prev, cur *image.RGBA) []image.Rectangle {
	var rects []image.Rectangle
	for ty := 0; ty < cur.Rect.Dy(); ty += tile {
		for tx := 0; tx < cur.Rect.Dx(); tx += tile {
			r := image.Rect(tx, ty, min(tx+tile, cur.Rect.Dx()), min(ty+tile, cur.Rect.Dy()))
			if !same(prev, cur, r) {
				rects = merge(rects, r)
			}
		}
	}
	return rects
}

func same(a, b *image.RGBA, r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		i := a.PixOffset(r.Min.X, y)
		j := i + r.Dx()*4
		if !bytes.Equal(a.Pix[i:j], b.Pix[i:j]) {
			return false
		}
	}
	return true
}

// merge adds r to the rectangles, joining it to any it touches or comes near, so a changed region
// arrives as one picture and unrelated ones stay apart.
func merge(rects []image.Rectangle, r image.Rectangle) []image.Rectangle {
	for {
		joined := false
		for i, have := range rects {
			if have.Inset(-tile).Overlaps(r) {
				r = r.Union(have)
				rects = append(rects[:i], rects[i+1:]...)
				joined = true
				break
			}
		}
		if !joined {
			return append(rects, r)
		}
	}
}
