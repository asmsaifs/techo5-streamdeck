package main

import (
	"context"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/sendspin"
	"github.com/asmsaifs/techo5-streamdeck/internal/speaker"
)

// SpeakerStatus says what the Shows-as-speaker is doing, for the editor's Speaker panel.
func (e *Editor) SpeakerStatus() speaker.Status { return e.core.Speaker.Status() }

// SpeakerShows lists the Sendspin players that answer on the network now, for the Show picker.
func (e *Editor) SpeakerShows() ([]sendspin.Player, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return e.core.Speaker.Shows(ctx)
}

// SpeakerTestTone plays a two-second tone on the given Shows, with the given lead (0: the
// default), and says which did not play it and why. The Shows come from the editor's unsaved
// picks, so a choice can be tried before it is saved.
func (e *Editor) SpeakerTestTone(shows []string, leadMs int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return e.core.Speaker.TestTone(ctx, shows, leadMs)
}
