package server

import (
	"image"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/sources"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// The way back from a website. The Show's own gesture, a swipe in from the left edge, leaves the
// whole stream; a tap on a chip in the corner returns to the deck and keeps the connection. The
// chip is drawn into the page's pictures for chipFor when the page opens, and again after a tap
// near the top edge, so it is out of the way the rest of the time.
const (
	chipFor = 3 * time.Second
	// chipZone is how far down from the top a tap calls the chip up.
	chipZone = 40
)

// chipState belongs to a session and is guarded by its sendMu, with the picture it is drawn on.
type chipState struct {
	on    bool
	timer *time.Timer
	last  *image.RGBA // the page's latest picture, without the chip
}

// framed is img as the Show should get it now: with the chip if it is up. The caller holds sendMu.
func (s *Server) framed(se *session, img *image.RGBA) *image.RGBA {
	se.chip.last = img
	if !se.chip.on || s.Renderer == nil {
		return img
	}
	out := image.NewRGBA(img.Rect)
	copy(out.Pix, img.Pix)
	s.Renderer.Chip(out, "Deck")
	return out
}

// raiseChip shows the chip for chipFor. The caller holds sendMu.
func (s *Server) raiseChip(se *session) {
	se.chip.on = true
	if se.chip.timer != nil {
		se.chip.timer.Stop()
	}
	se.chip.timer = time.AfterFunc(chipFor, func() { s.lowerChip(se) })
}

// lowerChip takes the chip down and repaints the page without it.
func (s *Server) lowerChip(se *session) {
	se.sendMu.Lock()
	defer se.sendMu.Unlock()
	se.chip.on = false
	if se.current() == sources.Source(se.deck) || se.chip.last == nil {
		return
	}
	if err := se.enc.Send(se.chip.last); err != nil {
		s.log().Warn("send", "name", se.hello.Name, "err", err)
	}
}

// touch routes a touch: to the deck or page being shown, except that a tap on the chip goes back
// to the deck, and a tap near the top edge of a page calls the chip up (and reaches the page too).
func (s *Server) touch(se *session, t wire.Touch) {
	src := se.current()
	if src == sources.Source(se.deck) {
		src.Touch(t)
		return
	}
	pt := image.Pt(t.X, t.Y)
	se.sendMu.Lock()
	chipUp := se.chip.on
	se.sendMu.Unlock()
	if chipUp && t.T == "tap" && pt.In(render.ChipRect()) {
		s.showDeck(se, src)
		return
	}
	if t.T == "tap" && t.Y < chipZone {
		se.sendMu.Lock()
		s.raiseChip(se)
		if se.chip.last != nil {
			_ = se.enc.Send(s.framed(se, se.chip.last))
		}
		se.sendMu.Unlock()
	}
	src.Touch(t)
}
