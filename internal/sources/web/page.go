package web

import (
	"context"
	"image"
	"log/slog"
	"regexp"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

var nonName = regexp.MustCompile(`[^a-z0-9]+`)

// viewport makes the tab the Show's screen: its size, at one pixel to the pixel.
func viewport(size image.Point) chromedp.Action {
	return emulation.SetDeviceMetricsOverride(int64(size.X), int64(size.Y), 1, false)
}

// finger turns the Show's touches into what a page gets from a mouse. dashcast sent real touch
// events, but headless Chrome of the kind that is a browser's own window waits for ever for the
// answer to one; mouse events are answered at once on every Chromium there is.
//
// A tap is a click. A drag is a scroll: the page follows the finger, as it would on a phone,
// so the wheel turns by what the finger moved. A slider on a page cannot be dragged for that
// reason; it can be tapped.
//
// A drag that starts with the page at its top and ends well below where it began is a pull to
// refresh: the page is loaded again. A page that has stuck (a player paused behind a hidden
// tab, a half drawn layout) has no other way out from the Show.
type finger struct {
	down  bool
	at    image.Point
	start image.Point
	top   bool // the page was at its top when the finger came down

	refreshed bool // the last touch reloaded the page
}

// pullDistance is how far the finger must travel down, in the Show's pixels, to refresh.
const pullDistance = 200

// reload loads the page again and brings it to the front, as a hidden page is not drawn.
func reload(ctx context.Context) error {
	if err := page.Reload().Do(ctx); err != nil {
		return err
	}
	return page.BringToFront().Do(ctx)
}

// atTop reports whether the page is scrolled to its top.
func atTop(ctx context.Context) bool {
	var y float64
	if err := chromedp.Evaluate(`window.scrollY || document.scrollingElement.scrollTop || 0`, &y).Do(ctx); err != nil {
		return false
	}
	return y <= 0
}

// replay plays one of the Show's touches on the page.
func (f *finger) replay(ctx context.Context, t wire.Touch) error {
	p := image.Pt(t.X, t.Y)
	switch t.T {
	case "tap":
		// No mouse move first, though a page would like to see the pointer arrive: Chrome holds a
		// bare move for five seconds waiting for a frame, and the press carries the position anyway.
		if err := input.DispatchMouseEvent(input.MousePressed, float64(p.X), float64(p.Y)).
			WithButton(input.Left).WithClickCount(1).Do(ctx); err != nil {
			return err
		}
		return input.DispatchMouseEvent(input.MouseReleased, float64(p.X), float64(p.Y)).
			WithButton(input.Left).WithClickCount(1).Do(ctx)
	case "down":
		f.down, f.at, f.start = true, p, p
		f.top = atTop(ctx)
	case "move":
		if !f.down {
			return nil
		}
		d := f.at.Sub(p)
		f.at = p
		if d == (image.Point{}) {
			return nil
		}
		return input.DispatchMouseEvent(input.MouseWheel, float64(p.X), float64(p.Y)).
			WithDeltaX(float64(d.X)).WithDeltaY(float64(d.Y)).Do(ctx)
	case "up":
		// The lift's own position counts too: a busy page drops moves, but never the lift.
		pulled := f.down && f.top && max(f.at.Y, p.Y)-f.start.Y >= pullDistance
		f.down = false
		if pulled {
			slog.Info("pull to refresh")
			f.refreshed = true
			return reload(ctx)
		}
	}
	return nil
}
