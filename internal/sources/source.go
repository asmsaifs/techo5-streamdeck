// Package sources holds what a Show can display: the deck, a website, a desktop app.
package sources

import (
	"context"
	"image"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Source is one thing the Show can display. The server holds one per connected device, runs its
// frames through the screen encoder and hands it the device's touches.
//
// Sound is left out until Phase 5, which adds an Audio channel beside Frames.
type Source interface {
	// Start begins producing frames for a screen of size. It returns at once; the source stops
	// when ctx ends or Close is called.
	Start(ctx context.Context, size image.Point) error

	// Frames delivers whole-screen pictures when the screen changes, the first as soon as Start has
	// one. A slow reader gets the latest picture, not every one: the channel holds one and a new
	// frame replaces an unread older frame. It is closed when the source stops.
	Frames() <-chan *image.RGBA

	// Touch is a touch from the device, in its pixels.
	Touch(t wire.Touch)

	Close() error
}
