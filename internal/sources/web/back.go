package web

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/chromedp/cdproto/page"
)

// The Show can only touch the picture it is sent, so a way back through the page's history has to
// be part of that picture. It is a small tab on the left edge, half way down, where pages of the
// kind this is used for (video sites, dashboards) keep nothing to press. Tapping it goes back.
const (
	backW = 48
	backH = 80
)

// backRect is the tab's place on a screen of this size.
func backRect(size image.Point) image.Rectangle {
	y := size.Y/2 - backH/2
	return image.Rect(0, y, backW, y+backH)
}

// drawBack puts the tab on a frame: a dark, half see-through tab with a white chevron.
func drawBack(img *image.RGBA) {
	r := backRect(img.Rect.Size())
	draw.Draw(img, r, image.NewUniform(color.NRGBA{0, 0, 0, 150}), image.Point{}, draw.Over)
	white := color.RGBA{255, 255, 255, 255}
	cx, cy := r.Min.X+backW/2+4, r.Min.Y+backH/2
	for i := 0; i <= 12; i++ {
		for w := -1; w <= 1; w++ {
			img.SetRGBA(cx-i+w, cy-12+i, white)
			img.SetRGBA(cx-i+w, cy+12-i, white)
		}
	}
}

// goBack steps back in the page's history, but never onto the blank page the tab started on: the
// first page of the tile has nothing before it, and going "back" there left the Show with a
// blank screen.
func goBack(ctx context.Context) error {
	i, entries, err := page.GetNavigationHistory().Do(ctx)
	if err != nil || i <= 0 || int(i) > len(entries) {
		return err
	}
	prev := entries[i-1]
	if prev.URL == "" || strings.HasPrefix(prev.URL, "about:") || strings.HasPrefix(prev.URL, "chrome:") {
		return nil
	}
	return page.NavigateToHistoryEntry(prev.ID).Do(ctx)
}
