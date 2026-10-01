package render

import (
	"image"
	"math"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// refSize is the screen the grid's gap and radius are written for (an Echo Show 5).
var refSize = image.Point{960, 480}

// Layout is where a grid's cells fall on a screen of one size. The renderer draws with it and the
// deck hit-tests touches with it, so a touch always lands on the cell that was drawn there.
type Layout struct {
	Grid  deck.Grid
	Size  image.Point
	Scale float64 // how much bigger than the 960 by 480 reference this screen is
	rects []image.Rectangle
}

// NewLayout divides a screen of size into g's cells. The gap is also the margin round the grid,
// so the buttons never touch the screen's edge.
func NewLayout(g deck.Grid, size image.Point) Layout {
	// The smaller ratio, so that a taller Show (1280 by 800) does not stretch gaps and corners.
	scale := math.Min(float64(size.X)/float64(refSize.X), float64(size.Y)/float64(refSize.Y))
	l := Layout{Grid: g, Size: size, Scale: scale}
	if g.Cols < 1 || g.Rows < 1 {
		return l
	}
	gap := math.Round(float64(g.Gap) * scale)
	cw := (float64(size.X) - gap*float64(g.Cols+1)) / float64(g.Cols)
	ch := (float64(size.Y) - gap*float64(g.Rows+1)) / float64(g.Rows)
	for row := 0; row < g.Rows; row++ {
		for col := 0; col < g.Cols; col++ {
			x := gap + float64(col)*(cw+gap)
			y := gap + float64(row)*(ch+gap)
			// Rounding each edge, not the width, keeps the gaps even to the pixel.
			l.rects = append(l.rects, image.Rect(round(x), round(y), round(x+cw), round(y+ch)))
		}
	}
	return l
}

func round(f float64) int { return int(math.Round(f)) }

// Rect is the pixels of cell c, or the empty rectangle when c is not on the grid.
func (l Layout) Rect(c deck.Cell) image.Rectangle {
	if !c.In(l.Grid) {
		return image.Rectangle{}
	}
	return l.rects[c.Row*l.Grid.Cols+c.Col]
}

// CellAt is the cell under p. A touch in a gap or the margin is on no cell.
func (l Layout) CellAt(p image.Point) (deck.Cell, bool) {
	for i, r := range l.rects {
		if p.In(r) {
			return deck.Cell{Col: i % l.Grid.Cols, Row: i / l.Grid.Cols}, true
		}
	}
	return deck.Cell{}, false
}

// Radius is the corner radius in pixels on this screen.
func (l Layout) Radius() float64 { return float64(l.Grid.Radius) * l.Scale }
