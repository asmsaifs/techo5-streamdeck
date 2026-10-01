// Package render draws the deck: a grid of buttons as pictures for the Show.
//
// The deck server and the desktop editor's preview both draw with this package, so what the
// editor shows is what the Show shows. Everything here is pure: the same inputs give the same
// pixels, which is what the golden tests rely on.
package render

import (
	"image"
	"image/color"
	"image/draw"
	"sync"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Flash is the 150 ms colour a button shows after its action ran.
type Flash uint8

const (
	FlashNone Flash = iota
	FlashOK
	FlashErr
)

// CellState is how one button looks beyond what its definition says.
type CellState struct {
	Pressed  bool  // a finger is on it: darker and a little smaller
	On       bool  // a toggle that is on: ringed in the accent colour
	Flash    Flash // the result of the action that just ran
	Disabled bool  // greyed out
	// Text is a live tile's value, drawn big where the icon would be (and instead of it).
	Text string
}

// State is the CellState of the cells that are not plain. A cell not in it is plain.
type State map[deck.Cell]CellState

const (
	pressedScale = 0.96
	// disabledMix is how far a disabled button's colours fade into the background.
	disabledMix = 0.55
	disabledA   = 110 // an icon's alpha when disabled, of 255
)

var (
	flashOK  = color.RGBA{0x2f, 0xbf, 0x71, 255}
	flashErr = color.RGBA{0xe5, 0x48, 0x4d, 255}
)

// Renderer draws buttons. It keeps the icons it has drawn, so one Renderer should serve the
// whole program; it is safe for concurrent use.
type Renderer struct {
	iconsDir string
	font     *opentype.Font

	mu    sync.Mutex
	icons map[iconKey]*image.RGBA
}

// New is a Renderer that reads image icons from iconsDir (config's icons/).
func New(iconsDir string) *Renderer {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		panic("render: the built-in font does not parse: " + err.Error()) // a build error, not a runtime one
	}
	return &Renderer{iconsDir: iconsDir, font: f, icons: map[iconKey]*image.RGBA{}}
}

// palette is a theme with its colours parsed.
type palette struct{ bg, button, text, accent color.RGBA }

func parseTheme(t deck.Theme) palette {
	get := func(s, def string) color.RGBA {
		if c, err := ParseColour(s); err == nil {
			return c
		}
		c, _ := ParseColour(def)
		return c
	}
	return palette{
		bg:     get(t.BG, deck.DefaultTheme.BG),
		button: get(t.Button, deck.DefaultTheme.Button),
		text:   get(t.Text, deck.DefaultTheme.Text),
		accent: get(t.Accent, deck.DefaultTheme.Accent),
	}
}

// Grid draws a whole page on a screen of size. Cells with no button are left as background.
func (r *Renderer) Grid(p *deck.Profile, pg *deck.Page, size image.Point, st State) *image.RGBA {
	l := NewLayout(p.Grid, size)
	pal := parseTheme(p.Theme)
	img := image.NewRGBA(image.Rectangle{Max: size})
	draw.Draw(img, img.Bounds(), image.NewUniform(pal.bg), image.Point{}, draw.Src)
	dc := gg.NewContextForRGBA(img)
	for key, b := range pg.Buttons {
		c, err := deck.ParseCell(key)
		if err != nil || !c.In(p.Grid) || b == nil {
			continue
		}
		r.drawCell(dc, img, image.Point{}, l.Rect(c), l.Scale, l.Radius(), pal, b, st[c])
	}
	return img
}

// Cell draws one button as its own picture, the size of its cell. It is the same pixels Grid puts
// at Layout.Rect(c), so a changed cell can be repainted without drawing the rest. A nil button
// is an empty cell.
func (r *Renderer) Cell(p *deck.Profile, b *deck.Button, c deck.Cell, size image.Point, st CellState) *image.RGBA {
	l := NewLayout(p.Grid, size)
	rect := l.Rect(c)
	pal := parseTheme(p.Theme)
	img := image.NewRGBA(image.Rectangle{Max: rect.Size()})
	draw.Draw(img, img.Bounds(), image.NewUniform(pal.bg), image.Point{}, draw.Src)
	if b != nil {
		r.drawCell(gg.NewContextForRGBA(img), img, rect.Min, rect, l.Scale, l.Radius(), pal, b, st)
	}
	return img
}

// drawCell draws b into rect. img is what dc draws on, whose top left is origin on the screen.
func (r *Renderer) drawCell(dc *gg.Context, img *image.RGBA, origin image.Point, rect image.Rectangle,
	scale, radius float64, pal palette, b *deck.Button, st CellState) {

	// Put the cell's own background down first, so repainting a cell in place erases what it
	// showed before.
	draw.Draw(img, rect.Sub(origin), image.NewUniform(pal.bg), image.Point{}, draw.Src)

	// A pressed button shrinks towards its centre; everything is worked out from the shrunk box.
	k := 1.0
	if st.Pressed {
		k = pressedScale
	}
	cx := (float64(rect.Min.X+rect.Max.X)/2 - float64(origin.X))
	cy := (float64(rect.Min.Y+rect.Max.Y)/2 - float64(origin.Y))
	w, h := float64(rect.Dx())*k, float64(rect.Dy())*k
	x0, y0 := cx-w/2, cy-h/2
	radius *= k

	fill := pal.button
	switch {
	case st.Pressed:
		fill = mix(fill, color.RGBA{0, 0, 0, 255}, 0.3)
	case st.Flash == FlashOK:
		fill = mix(fill, flashOK, 0.55)
	case st.Flash == FlashErr:
		fill = mix(fill, flashErr, 0.55)
	case st.On:
		fill = mix(fill, pal.accent, 0.12)
	}
	fg := pal.text
	alpha := uint8(255)
	if st.Disabled {
		fill = mix(fill, pal.bg, disabledMix)
		fg = mix(fg, pal.bg, disabledMix)
		alpha = disabledA
	}
	dc.SetColor(fill)
	dc.DrawRoundedRectangle(x0, y0, w, h, radius)
	dc.Fill()
	if st.On && !st.Disabled {
		lw := 3 * scale * k
		dc.SetColor(pal.accent)
		dc.SetLineWidth(lw)
		dc.DrawRoundedRectangle(x0+lw/2, y0+lw/2, w-lw, h-lw, radius-lw/2)
		dc.Stroke()
	}

	// Content: the icon over the label, or whichever there is, centred.
	pad := 0.07 * w
	faces := map[int]font.Face{}
	defer func() {
		for _, f := range faces {
			f.Close()
		}
	}()
	face := func(px int) font.Face {
		if f, ok := faces[px]; ok {
			return f
		}
		f, err := opentype.NewFace(r.font, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			panic("render: " + err.Error())
		}
		faces[px] = f
		return f
	}
	measure := func(px int) func(string) float64 {
		dc.SetFontFace(face(px))
		return func(s string) float64 { w, _ := dc.MeasureString(s); return w }
	}

	var lines []string
	var px int
	var lineH float64
	if b.Label != "" {
		base := 0.13 * h
		if b.Icon == "" {
			base = 0.17 * h // alone, the label can be bigger
		}
		lines, px = fitLabel(b.Label, w-2*pad, base, measure)
		lineH = float64(px) * 1.2
	}
	labelH := lineH * float64(len(lines))

	// A live tile's value takes the place of the icon.
	var vLines []string
	var vPx int
	var vLineH float64
	if st.Text != "" {
		base := 0.30 * h
		if b.Label == "" {
			base = 0.38 * h
		}
		vLines, vPx = fitLabel(st.Text, w-2*pad, base, measure)
		vLineH = float64(vPx) * 1.2
	}

	var ic *image.RGBA
	var iconSize float64
	iconCY := cy
	if b.Icon != "" && st.Text == "" {
		if len(lines) == 0 {
			iconSize = 0.6 * min(w, h)
		} else {
			// The icon gets the room the label leaves, between a top margin and the label.
			top := y0 + 0.08*h
			bottom := y0 + h - 0.07*h - labelH - 0.04*h
			iconSize = min(bottom-top, 0.55*min(w, h))
			iconCY = (top + bottom) / 2
		}
		ic = r.icon(b.Icon, round(iconSize), fg)
	}
	if ic != nil {
		sz := ic.Bounds().Dx()
		at := image.Rect(round(cx)-sz/2, round(iconCY)-sz/2, round(cx)-sz/2+sz, round(iconCY)-sz/2+sz)
		draw.DrawMask(img, at, ic, image.Point{}, image.NewUniform(color.Alpha{alpha}), image.Point{}, draw.Over)
	}

	if len(vLines) > 0 {
		dc.SetFontFace(face(vPx))
		dc.SetColor(fg)
		mid := cy
		if len(lines) > 0 {
			top := y0 + 0.08*h
			bottom := y0 + h - 0.07*h - labelH - 0.04*h
			mid = (top + bottom) / 2
		}
		ty := mid - vLineH*float64(len(vLines))/2
		for i, line := range vLines {
			dc.DrawStringAnchored(line, cx, ty+vLineH*(float64(i)+0.5), 0.5, 0.35)
		}
	}

	if len(lines) > 0 {
		dc.SetFontFace(face(px))
		dc.SetColor(fg)
		// Centred in the room below the icon, or in the whole cell when there is no icon.
		ty := cy - labelH/2
		if ic != nil || len(vLines) > 0 {
			ty = y0 + h - 0.07*h - labelH
		}
		for i, line := range lines {
			dc.DrawStringAnchored(line, cx, ty+lineH*(float64(i)+0.5), 0.5, 0.35)
		}
	}
}

// fitLabel picks the biggest size at which the label fits the width: one line first, shrunk to
// three quarters of base; then two lines, from a little under base down to 60 % of it; and if
// that is still too long, two lines at the smallest size with an ellipsis. measure(px) is a
// width function for a font size.
func fitLabel(text string, width, base float64, measure func(px int) func(string) float64) (lines []string, px int) {
	big := max(int(base), 10)
	for p := big; p >= max(int(base*0.75), 10); p-- {
		if ls, whole := wrapLabel(text, width, 1, measure(p)); whole {
			return ls, p
		}
	}
	small := max(int(base*0.6), 10)
	for p := max(int(base*0.85), small); p >= small; p-- {
		if ls, whole := wrapLabel(text, width, 2, measure(p)); whole {
			return ls, p
		}
	}
	ls, _ := wrapLabel(text, width, 2, measure(small))
	return ls, small
}
