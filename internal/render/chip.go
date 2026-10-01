package render

import (
	"image"
	"image/color"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// ChipRect is where the "back to the deck" chip sits on a screen: the top left corner, big
// enough for a finger and clear of the strips along the edges where the device keeps drags.
func ChipRect() image.Rectangle { return image.Rect(8, 8, 128, 52) }

// Chip draws the chip, an arrow and label, over dst. It is drawn into the pictures of a website or
// app the Show is looking at, which are not the deck's own, so it is translucent: what is under
// it still shows.
func (r *Renderer) Chip(dst *image.RGBA, label string) {
	rect := ChipRect()
	dc := gg.NewContextForRGBA(dst)
	dc.SetColor(color.RGBA{16, 17, 20, 217})
	dc.DrawRoundedRectangle(float64(rect.Min.X), float64(rect.Min.Y), float64(rect.Dx()), float64(rect.Dy()), float64(rect.Dy())/2)
	dc.Fill()
	dc.SetColor(color.White)
	// The arrow is drawn, not typed: the built-in font has no glyph for it.
	cy := float64(rect.Min.Y+rect.Max.Y) / 2
	ax := float64(rect.Min.X) + 22
	dc.MoveTo(ax-8, cy)
	dc.LineTo(ax+4, cy-9)
	dc.LineTo(ax+4, cy+9)
	dc.ClosePath()
	dc.Fill()
	if face, err := opentype.NewFace(r.font, &opentype.FaceOptions{Size: 20, DPI: 72, Hinting: font.HintingNone}); err == nil {
		dc.SetFontFace(face)
		dc.DrawStringAnchored(label, ax+16, cy, 0, 0.35)
	}
}
