package main

import "github.com/asmsaifs/techo5-streamdeck/internal/wire"

// gestures turns a mouse into the touch lines a Show sends, by the Show's own rules
// (techo5/echod/internal/hardware/touch/touch.go in follow mode, and dashGesture in
// internal/feature/display/dashboard.go):
//
//   - A finger that never moves more than followMove is a tap, sent once, when it lifts, at where it
//     landed. However long it was held: the Show 5 has no holds. No down comes before it.
//   - A finger that moves further is a down where it landed, a move as it goes, and an up where it
//     lifts.
//   - A drag that starts in the strip at the left or right edge, or along the top, is the device's
//     (leave the stream, open the drawer, bring the settings down), and nothing of it is sent. A tap
//     there is still a tap.
//
// A drag from the left edge further than far leaves the stream, as it does on the Show.
type gestures struct {
	w, h int

	down     bool
	followed bool // moved far enough to be a down, move, up
	edge     bool // started in an edge strip: the device's, not the page's
	left     bool // and that strip was the left
	x0, y0   int
}

const (
	followMove = 12  // touch.go's followMove: fixed, not scaled
	topBand    = 40  // topEdge/3 in render_settings.go: fixed, not scaled
	sideBand   = 40  // the dashboard's margin at 960 wide, scaled with the width
	farBase    = 80  // how far an edge drag has to go to count, at 960 wide
	drawnFor   = 960 // the width the device's sizes are given for
)

func (g *gestures) scale(n int) int { return n * g.w / drawnFor }

// press is the mouse button going down at x, y.
func (g *gestures) press(x, y int) {
	side := g.scale(sideBand)
	*g = gestures{w: g.w, h: g.h, down: true, x0: x, y0: y}
	g.left = x < side
	g.edge = g.left || x >= g.w-side || y < topBand
}

// move is the mouse moving to x, y with the button down.
func (g *gestures) move(x, y int) []wire.Touch {
	if !g.down {
		return nil
	}
	if !g.followed {
		if abs(x-g.x0) <= followMove && abs(y-g.y0) <= followMove {
			return nil
		}
		g.followed = true
		if g.edge {
			return nil
		}
		return []wire.Touch{{T: "down", X: g.x0, Y: g.y0}, {T: "move", X: x, Y: y}}
	}
	if g.edge {
		return nil
	}
	return []wire.Touch{{T: "move", X: x, Y: y}}
}

// release is the mouse button coming up at x, y. leave is a left-edge swipe: the Show would close
// the stream.
func (g *gestures) release(x, y int) (out []wire.Touch, leave bool) {
	if !g.down {
		return nil, false
	}
	// A lift without a move event in between still moved, as the device sees it.
	out = g.move(x, y)
	g.down = false
	switch {
	case !g.followed:
		return append(out, wire.Touch{T: "tap", X: g.x0, Y: g.y0}), false
	case g.edge:
		return nil, g.left && x-g.x0 > g.scale(farBase)
	}
	return append(out, wire.Touch{T: "up", X: x, Y: y}), false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
