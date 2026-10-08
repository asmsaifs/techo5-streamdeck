// Package speaker makes the Shows this computer's speaker: a sound device the user picks in the
// OS, read by a Tap, sent by internal/sendspin to the Shows the config names (docs/speaker.md).
package speaker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/sendspin"
)

// DeviceName is what the sound device is called in the OS.
const DeviceName = "TECHO5 Show"

// Sound is what a Tap delivers: S16LE PCM, as the device gives it.
type Sound struct {
	Format audio.Format
	PCM    []byte
}

// Volume is the device's volume slider, 0 to 1, and its mute.
type Volume struct {
	Level float64
	Muted bool
}

// Tap reads the sound device: one per OS, behind this interface.
type Tap interface {
	// Start opens the device. The channels close when the Tap stops or fails.
	Start() (<-chan Sound, error)
	// Volume is the device's slider, sent whenever it moves and once at the start.
	Volume() <-chan Volume
	Stop()
}

// ErrNoDevice is a computer this build has no sound device for yet.
var ErrNoDevice = errors.New("the " + DeviceName + " sound device is not available on this computer yet")

// newTap opens this OS's Tap. Each OS that has one sets it from its own file.
var newTap = func() (Tap, error) { return nil, ErrNoDevice }

// Status is what the tray and the editor show.
type Status struct {
	Enabled bool
	// Text is one line for the tray: "Off", "Waiting for sound", "Playing on Kitchen Show"...
	Text    string
	Playing []string
	Busy    []string
	Failed  map[string]string
}

// Speaker runs the speaker as the config says.
type Speaker struct {
	Finder *sendspin.Finder
	name   string
	id     string

	mu       sync.Mutex
	cfg      deck.Speaker
	group    *sendspin.Group
	tap      Tap
	tapErr   error
	tapDone  chan struct{}
	onChange []func()
}

// New is a Speaker that does nothing until Apply turns it on.
func New() *Speaker {
	host, _ := os.Hostname()
	host = strings.TrimSuffix(host, ".local")
	if host == "" {
		host = "computer"
	}
	return &Speaker{Finder: &sendspin.Finder{}, name: host, id: "techo5-streamdeck-" + host}
}

// OnChange calls fn after the Status may have changed, on whatever goroutine changed it.
func (s *Speaker) OnChange(fn func()) {
	s.mu.Lock()
	s.onChange = append(s.onChange, fn)
	s.mu.Unlock()
}

func (s *Speaker) changed() {
	s.mu.Lock()
	fns := slices.Clone(s.onChange)
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// Apply follows the config: the device is read and the Shows are dialed while it is enabled and
// names a Show. Nil is off.
func (s *Speaker) Apply(c *deck.Speaker) {
	var want deck.Speaker
	if c != nil {
		want = *c
		want.Shows = slices.Clone(c.Shows)
	}
	on := want.Enabled && len(want.Shows) > 0

	s.mu.Lock()
	old := s.cfg
	s.cfg = want
	var closeGroup *sendspin.Group
	var stopTap Tap
	var done chan struct{}
	switch {
	case !on:
		closeGroup, s.group = s.group, nil
		stopTap, s.tap, done = s.tap, nil, s.tapDone
		s.tapErr = nil
	case s.group != nil && (old.LeadMs != want.LeadMs || old.IdleS != want.IdleS):
		// The lead is the stamps' spacing from now; changing it under a running stream would move
		// them, so the Group starts again.
		closeGroup = s.group
		s.group = s.newGroup(want)
	case s.group == nil:
		s.group = s.newGroup(want)
	default:
		s.group.SetShows(want.Shows)
	}
	startTap := on && s.tap == nil
	s.mu.Unlock()

	if closeGroup != nil {
		closeGroup.Close()
	}
	if stopTap != nil {
		stopTap.Stop()
		<-done
	}
	if startTap {
		s.startTap()
	}
	s.changed()
}

// newGroup wants mu.
func (s *Speaker) newGroup(c deck.Speaker) *sendspin.Group {
	return sendspin.NewGroup(c.Shows, sendspin.Options{
		Name: s.name, ID: s.id,
		Lead:    time.Duration(c.LeadMs) * time.Millisecond,
		Idle:    time.Duration(c.IdleS) * time.Second,
		Resolve: s.Finder.Resolve,
		Forget:  s.Finder.Forget,
		Changed: s.changed,
	})
}

func (s *Speaker) startTap() {
	tap, err := newTap()
	var sounds <-chan Sound
	if err == nil {
		sounds, err = tap.Start()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.tapErr = err
		slog.Warn("speaker: the sound device cannot be read", "err", err)
		return
	}
	if s.group == nil { // turned off while the device was opening
		go tap.Stop()
		return
	}
	s.tap, s.tapErr = tap, nil
	s.tapDone = make(chan struct{})
	go s.pump(tap, sounds, s.tapDone)
}

// pump carries the device's sound and volume to whichever Group is current.
func (s *Speaker) pump(tap Tap, sounds <-chan Sound, done chan struct{}) {
	defer close(done)
	vols := tap.Volume()
	for {
		select {
		case snd, ok := <-sounds:
			if !ok {
				s.mu.Lock()
				if s.tap == tap {
					s.tap, s.tapErr = nil, errors.New("the sound device stopped")
				}
				s.mu.Unlock()
				s.changed()
				return
			}
			if g := s.current(); g != nil {
				g.Write(snd.Format, snd.PCM)
			}
		case v, ok := <-vols:
			if !ok {
				vols = nil
				continue
			}
			if g := s.current(); g != nil {
				g.SetVolume(v.Level, v.Muted)
			}
		}
	}
}

func (s *Speaker) current() *sendspin.Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.group
}

// Status is what the speaker is doing now.
func (s *Speaker) Status() Status {
	s.mu.Lock()
	cfg, g, tapErr := s.cfg, s.group, s.tapErr
	s.mu.Unlock()
	st := Status{Enabled: cfg.Enabled}
	var gs sendspin.State
	if g != nil {
		gs = g.State()
		st.Playing, st.Busy, st.Failed = gs.Playing, gs.Busy, gs.Failed
	}
	st.Text = describe(cfg, tapErr, gs)
	return st
}

func describe(cfg deck.Speaker, tapErr error, gs sendspin.State) string {
	switch {
	case !cfg.Enabled:
		return "Off"
	case len(cfg.Shows) == 0:
		return "No Show picked"
	case errors.Is(tapErr, ErrNoDevice):
		return "Not available on this computer yet"
	case tapErr != nil:
		return "Sound device failed"
	case len(gs.Playing) == 1:
		return "Playing on " + gs.Playing[0]
	case len(gs.Playing) > 1:
		return fmt.Sprintf("Playing on %d Shows", len(gs.Playing))
	case len(gs.Busy) > 0:
		return "Show busy (another source playing)"
	case len(gs.Failed) > 0:
		return "Show not reachable"
	case gs.Open:
		return "Connecting…"
	}
	return "Waiting for sound"
}

// Shows lists the Sendspin players on the network, for the editor's picker.
func (s *Speaker) Shows(ctx context.Context) ([]sendspin.Player, error) {
	return s.Finder.Look(ctx, 3*time.Second)
}

// toneFor is how long the test tone plays.
const toneFor = 2 * time.Second

// TestTone plays a short tone on the given Shows, through a Group of its own, and says what went
// wrong if no Show played it. It works whether the speaker is on or not; while the speaker is
// playing on a Show, that Show turns the tone away as busy.
func (s *Speaker) TestTone(ctx context.Context, shows []string, leadMs int) error {
	if len(shows) == 0 {
		return errors.New("pick a Show first")
	}
	if leadMs != 0 {
		leadMs = max(deck.MinLeadMs, min(deck.MaxLeadMs, leadMs))
	}
	g := sendspin.NewGroup(shows, sendspin.Options{
		Name: s.name, ID: s.id + "-tone",
		Lead:    time.Duration(leadMs) * time.Millisecond,
		Resolve: s.Finder.Resolve, Forget: s.Finder.Forget,
	})
	defer g.Close()

	chunk := make([]byte, audio.ChunkFrames*4)
	amp := 32767 * math.Pow(10, -20.0/20) // -20 dBFS: audible, not loud
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	start := time.Now()
	var played bool
	// The tone plays for toneFor once a Show is connected, and gives up after a few seconds if
	// none connects. Then a lead's worth of silence, so the end of the tone is heard before
	// stream/end clears what the Show holds.
	for n := 0; ; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		st := g.State()
		if len(st.Playing) > 0 && !played {
			played, start = true, time.Now()
		}
		if !played && pending(st, shows) == 0 {
			break
		}
		if played && time.Since(start) >= toneFor || !played && time.Since(start) >= 8*time.Second {
			break
		}
		for i := range audio.ChunkFrames {
			v := uint16(int16(amp * math.Sin(2*math.Pi*440*float64(n)/audio.Rate)))
			binary.LittleEndian.PutUint16(chunk[i*4:], v)
			binary.LittleEndian.PutUint16(chunk[i*4+2:], v)
			n++
		}
		g.Write(audio.Wire, chunk)
	}
	st := g.State()
	if played {
		quiet := make([]byte, len(chunk))
		for end := time.Now().Add(g.Lead() + 100*time.Millisecond); time.Now().Before(end); {
			<-t.C
			g.Write(audio.Wire, quiet)
		}
	}
	return toneResult(shows, st)
}

// pending counts the Shows the tone is still waiting on: neither playing, busy nor failed.
func pending(st sendspin.State, shows []string) int {
	n := 0
	for _, s := range shows {
		if !slices.Contains(st.Playing, s) && !slices.Contains(st.Busy, s) && st.Failed[s] == "" {
			n++
		}
	}
	return n
}

// toneResult is nil if every Show played the tone, and otherwise says which did not and why.
func toneResult(shows []string, st sendspin.State) error {
	var problems []string
	for _, s := range shows {
		switch {
		case slices.Contains(st.Playing, s):
		case slices.Contains(st.Busy, s):
			problems = append(problems, s+" is playing from another source (Music Assistant, or this computer)")
		case st.Failed[s] != "":
			problems = append(problems, s+": "+st.Failed[s])
		default:
			problems = append(problems, s+" did not answer")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

// Close turns the speaker off.
func (s *Speaker) Close() { s.Apply(nil) }
