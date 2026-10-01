package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Spec says what a tile shows.
type Spec struct {
	URL string
	// Allow names sites the page may be on besides the URL's own domain (allow.go). A sign-in that
	// lives on another domain needs its domain here.
	Allow []string
	// Profile names the browser profile, and with it which logins the page has. Empty: one per
	// registrable domain, so two tiles of the same site share a sign-in.
	Profile string
	// Sound is where the page's sound goes: SoundShow (the default), SoundDesktop or SoundOff.
	// Anything but SoundDesktop captures it, which is what keeps it off the computer's speakers;
	// only SoundShow, and only if the source has somewhere to send it (SetSound), passes it on.
	Sound string
}

// Pacing, as dashcast has it: after a frame that changed something the next is asked for almost
// at once, so a page that moves moves smoothly; after each that changed nothing the wait doubles
// up to idlePace, so a page that only thinks it is changing costs next to nothing.
const (
	busyPace = 40 * time.Millisecond
	idlePace = time.Second
)

// touchQueue is how many touches wait for the browser. A finger sends a move per frame, so a
// slow page drops moves rather than reply late.
const touchQueue = 64

// Source is one website in one browser tab, shown to one Show.
type Source struct {
	m     *Manager
	spec  Spec
	allow *Allow
	log   *slog.Logger

	profile string
	ctx     context.Context
	cancel  context.CancelFunc
	size    image.Point
	touches chan wire.Touch

	fmu    sync.Mutex // guards frames' closing and the error
	frames chan *image.RGBA
	closed bool
	err    error

	// snd takes the page's sound, 48 kHz stereo S16LE, if the Show can play it.
	snd func(pcm []byte)
	// tabp is the tab the source is showing, once it has one.
	tabp atomic.Pointer[tab]

	// The last frame as Chrome sent it, and how many in a row changed nothing.
	pmu     sync.Mutex
	lastRaw []byte
	still   int
}

// New is the tile for spec. It checks the spec but opens nothing until Start.
func (m *Manager) New(spec Spec) (*Source, error) {
	allow, err := NewAllow(spec.URL, spec.Allow)
	if err != nil {
		return nil, err
	}
	u, _ := parseWeb(spec.URL)
	profile := spec.Profile
	if profile == "" {
		profile = strings.Trim(nonName.ReplaceAllString(registrable(u.Hostname()), "-"), "-")
	}
	if !profileName.MatchString(profile) {
		return nil, fmt.Errorf("%q is not a profile name: use lower case letters, digits, - and _", profile)
	}
	return &Source{m: m, spec: spec, allow: allow, profile: profile,
		log:     m.log().With("site", u.Hostname()),
		frames:  make(chan *image.RGBA, 1),
		touches: make(chan wire.Touch, touchQueue)}, nil
}

// SetSound gives the source somewhere to send the page's sound, as 48 kHz stereo S16LE PCM. Call it
// before Start. Without it, or with Spec.Sound "off", the sound is captured and thrown away.
func (s *Source) SetSound(f func(pcm []byte)) { s.snd = f }

func (s *Source) sound(pcm []byte) {
	if s.snd != nil && s.spec.Sound != SoundOff {
		s.snd(pcm)
	}
}

// wantsSound is whether the page's sound is to be captured: always, unless it is to come out of
// the computer.
func (s *Source) wantsSound() bool { return s.spec.Sound != SoundDesktop }

// Start opens the page and returns at once; the first frame comes when the page has drawn.
func (s *Source) Start(ctx context.Context, size image.Point) error {
	if s.ctx != nil {
		return errors.New("web: started twice")
	}
	s.size = size
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run()
	return nil
}

func (s *Source) Frames() <-chan *image.RGBA { return s.frames }

// Err is why the source stopped by itself, once Frames is closed: the browser would not start,
// the page would not open, the tab died. Nil when it was closed on purpose.
func (s *Source) Err() error {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	return s.err
}

// Profile is the browser profile the tile runs in.
func (s *Source) Profile() string { return s.profile }

func (s *Source) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *Source) fail(err error) {
	s.fmu.Lock()
	if s.err == nil && s.ctx.Err() == nil {
		s.err = err
	}
	s.fmu.Unlock()
	s.cancel()
}

func (s *Source) finish() {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.frames)
	}
}

// push replaces the frame nobody has taken yet.
func (s *Source) push(img *image.RGBA) {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	if s.closed {
		return
	}
	select {
	case <-s.frames:
	default:
	}
	s.frames <- img
}

func (s *Source) run() {
	defer s.finish()
	t, reused, err := s.m.openTab(s.spec, s.allow, s.profile, s.size)
	if err != nil {
		s.fail(err)
		return
	}
	t.setOwner(s)
	s.tabp.Store(t)
	// A tile that ends well is parked for the next one that wants the same page; one that failed
	// is not worth keeping.
	defer func() {
		if s.Err() != nil {
			t.close()
		} else {
			s.m.park(t)
		}
	}()
	tab := t.ctx

	// What is run for this source stops when it does, so a page that is slow to load does not
	// hold up its leaving. The tab itself is not ended by that.
	rc, cancel := context.WithCancel(tab)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()

	var acts chromedp.Tasks
	if !reused {
		acts = chromedp.Tasks{
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, err := page.AddScriptToEvaluateOnNewDocument(s.allow.guardScript()).Do(ctx)
				return err
			}),
			chromedp.ActionFunc(func(ctx context.Context) error {
				if err := runtime.AddBinding(t.binding).Do(ctx); err != nil {
					return err
				}
				_, err := page.AddScriptToEvaluateOnNewDocument(captureSource(t.binding)).Do(ctx)
				return err
			}),
			// Only main-page navigations are looked at; everything else goes on without being paused.
			fetch.Enable().WithPatterns([]*fetch.RequestPattern{{
				ResourceType: network.ResourceTypeDocument, RequestStage: fetch.RequestStageRequest}}),
			viewport(s.size),
			chromedp.Navigate(s.spec.URL),
		}
	}
	acts = append(acts,
		// A window that is not in front is hidden, and a hidden page is not drawn: no frames.
		page.BringToFront(),
		page.StartScreencast().WithFormat(page.ScreencastFormatPng).
			WithMaxWidth(int64(s.size.X)).WithMaxHeight(int64(s.size.Y)),
	)
	if err := chromedp.Run(rc, acts); err != nil {
		s.fail(fmt.Errorf("the page would not open: %w", err))
		return
	}
	if s.wantsSound() {
		t.capturing.Store(true)
		if err := t.startCapture(rc); err != nil && s.ctx.Err() == nil {
			s.log.Warn("sound", "err", err)
		}
	}
	s.log.Info("page opened", "profile", s.profile, "allowed", s.allow.Domains(), "warm", reused)
	// Chrome sends a frame when something is drawn, and a page that has finished loading and
	// stands still draws nothing more: the Show would wait for ever for its first picture.
	s.snapshot(rc)

	var f finger
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev := <-s.touches:
			if err := chromedp.Run(rc, chromedp.ActionFunc(func(ctx context.Context) error { return f.replay(ctx, ev) })); err != nil && s.ctx.Err() == nil {
				s.log.Warn("touch", "err", err)
			}
		}
	}
}

// navigate sends the page to url, for the tests: a Show cannot do this, only the page's own links.
func (s *Source) navigate(url string) error {
	t := s.tabp.Load()
	if t == nil {
		return errors.New("no tab")
	}
	return chromedp.Run(t.ctx, chromedp.Navigate(url))
}

// snapshot sends the page as it is now.
func (s *Source) snapshot(tab context.Context) {
	var shot []byte
	if err := chromedp.Run(tab, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		shot, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).Do(ctx)
		return err
	})); err != nil {
		return
	}
	if s.same(shot) {
		return
	}
	if img, err := png.Decode(bytes.NewReader(shot)); err == nil && img.Bounds().Size() == s.size {
		s.push(toRGBA(img))
	}
}

func toRGBA(img image.Image) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(rgba, rgba.Rect, img, img.Bounds().Min, draw.Src)
	return rgba
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// onFrame takes one picture from Chrome.
func (s *Source) onFrame(tab context.Context, f *page.EventScreencastFrame) {
	// Acknowledged once handled, which is what paces Chrome: it sends the next frame only after
	// this one is acknowledged, so a slow Show holds frames back rather than queuing them.
	changed := false
	defer func() {
		select {
		case <-time.After(s.pace(changed)):
		case <-s.ctx.Done():
			return
		}
		_ = chromedp.Run(tab, page.ScreencastFrameAck(f.SessionID))
	}()
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return
	}
	// A page that redraws without changing sends the same picture again and again, byte for byte:
	// that is found without decoding it. PNG, not JPEG, for exactly this.
	if s.same(raw) {
		return
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return
	}
	// A frame of another size than the Show's screen would be drawn in the wrong place.
	if img.Bounds().Size() != s.size {
		return
	}
	changed = true
	s.push(toRGBA(img))
}

func (s *Source) same(raw []byte) bool {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	if bytes.Equal(raw, s.lastRaw) {
		return true
	}
	s.lastRaw = raw
	return false
}

// pace is how long to wait before asking for the next frame.
func (s *Source) pace(changed bool) time.Duration {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	if changed {
		s.still = 0
		return busyPace
	}
	s.still = min(s.still+1, 16)
	return min(busyPace<<s.still, idlePace)
}

// Touch queues a touch for the page. Before the page is open and when it falls behind, touches
// are dropped, moves first.
func (s *Source) Touch(t wire.Touch) {
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	select {
	case s.touches <- t:
	default:
		if t.T != "move" {
			// A tap or a lift must arrive, or the page is left holding a finger down.
			select {
			case s.touches <- t:
			case <-s.ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}
