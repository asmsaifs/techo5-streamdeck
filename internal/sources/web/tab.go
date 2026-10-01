package web

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Loading a page is the dear part: a heavy one keeps a browser busy for seconds. So a tile that
// the Show leaves (back to the deck, a dropped connection) does not take its window with it at
// once. The window is parked - its stream stopped, the page frozen so its scripts stop too - and
// a tile that opens the same page within warmFor gets it back, thawed, without loading anything.
// Only warmCount are parked at a time, the oldest closed to make room.
const (
	defaultWarmFor   = 10 * time.Minute
	defaultWarmCount = 2
)

// tab is a window of a browser, showing one page for one screen size, and whoever is watching it:
// the source using it, or nobody while it is parked. A tab's events arrive through one listener for
// its whole life, since a listener cannot be taken off again.
type tab struct {
	m       *Manager
	b       *browser
	profile string
	key     string
	spec    Spec
	allow   *Allow
	log     *slog.Logger

	ctx     context.Context
	closeFn func()
	once    sync.Once

	mu     sync.Mutex
	owner  *Source
	expiry *time.Timer
}

func (t *tab) setOwner(s *Source) {
	t.mu.Lock()
	t.owner = s
	t.mu.Unlock()
}

func (t *tab) current() *Source {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.owner
}

// close closes the window and gives its place in the browser back. It runs once.
func (t *tab) close() {
	t.once.Do(func() {
		t.mu.Lock()
		if t.expiry != nil {
			t.expiry.Stop()
		}
		t.mu.Unlock()
		t.m.unpark(t)
		t.closeFn()
		t.m.release(t.profile, t.b)
	})
}

// tabKey says which tabs are the same: the same page, allowed the same sites, in the same
// profile, for the same screen.
func tabKey(profile string, spec Spec, allow *Allow, size image.Point) string {
	d := allow.Domains()
	slices.Sort(d)
	return fmt.Sprintf("%s|%s|%s|%dx%d", profile, spec.URL, strings.Join(d, ","), size.X, size.Y)
}

// openTab gives a window for the page: a parked one that shows it already, thawed, or a new one.
// A new one is not yet showing anything: the caller sets it up.
func (m *Manager) openTab(spec Spec, allow *Allow, profile string, size image.Point) (t *tab, reused bool, err error) {
	key := tabKey(profile, spec, allow, size)
	if t := m.takeParked(key); t != nil {
		return t, true, nil
	}
	b, err := m.acquire(profile)
	if err != nil {
		return nil, false, err
	}
	ctx, closeFn, err := b.openTab(size)
	if err != nil {
		m.release(profile, b)
		return nil, false, fmt.Errorf("the browser would not open a window: %w", err)
	}
	u, _ := parseWeb(spec.URL)
	t = &tab{m: m, b: b, profile: profile, key: key, spec: spec, allow: allow, ctx: ctx, closeFn: closeFn,
		log: m.log().With("site", u.Hostname())}
	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *page.EventScreencastFrame:
			if s := t.current(); s != nil {
				go s.onFrame(ctx, e)
			}
		case *fetch.EventRequestPaused:
			go t.onRequest(e)
		}
	})
	// The tab dying (the browser crashed, the window was killed) takes it out of the pool and ends
	// its source, if it has one.
	go func() {
		<-ctx.Done()
		if s := t.current(); s != nil {
			s.fail(fmt.Errorf("the browser tab closed"))
		}
		t.close()
	}()
	return t, false, nil
}

// onRequest decides a main-page navigation: the page may only go where the allowlist says. A
// frame inside the page is its own business; it is the page the Show is looking at that counts.
// It is the tab's, not its source's, so a parked page is held to it too.
func (t *tab) onRequest(e *fetch.EventRequestPaused) {
	top := false
	if c := chromedp.FromContext(t.ctx); c != nil && c.Target != nil {
		top = string(e.FrameID) == string(c.Target.TargetID)
	}
	ok := !top || t.allow.Permits(e.Request.URL)
	_ = chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if ok {
			return fetch.ContinueRequest(e.RequestID).Do(ctx)
		}
		return fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
	}))
	if !ok {
		t.log.Warn("the page tried to leave its allowed sites", "to", hostOf(e.Request.URL), "allowed", t.allow.Domains())
		// Puts back the page a refused navigation replaced with Chrome's error page: the one
		// before it, or the tile's own address if there is none.
		if err := chromedp.Run(t.ctx, chromedp.NavigateBack()); err != nil {
			_ = chromedp.Run(t.ctx, chromedp.Navigate(t.spec.URL))
		}
	}
}

func (m *Manager) warmFor() time.Duration {
	if m.WarmFor > 0 {
		return m.WarmFor
	}
	return defaultWarmFor
}

func (m *Manager) warmCount() int {
	if m.WarmCount != 0 {
		return max(m.WarmCount, 0)
	}
	return defaultWarmCount
}

// takeParked is the parked tab for key, thawed, or nil.
func (m *Manager) takeParked(key string) *tab {
	m.pmu.Lock()
	var t *tab
	for i, p := range m.parked {
		if p.key == key && p.ctx.Err() == nil {
			t = p
			m.parked = append(m.parked[:i], m.parked[i+1:]...)
			break
		}
	}
	m.pmu.Unlock()
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.expiry != nil {
		t.expiry.Stop()
	}
	t.mu.Unlock()
	if err := chromedp.Run(t.ctx, page.SetWebLifecycleState(page.SetWebLifecycleStateStateActive)); err != nil {
		t.close()
		return nil
	}
	return t
}

// park stops a tab's stream, freezes it, and keeps it for warmFor.
func (m *Manager) park(t *tab) {
	t.setOwner(nil)
	if m.closed.Load() || m.warmCount() == 0 {
		t.close()
		return
	}
	if err := chromedp.Run(t.ctx, page.StopScreencast(),
		page.SetWebLifecycleState(page.SetWebLifecycleStateStateFrozen)); err != nil {
		t.close()
		return
	}
	m.pmu.Lock()
	var evict *tab
	if len(m.parked) >= m.warmCount() {
		evict, m.parked = m.parked[0], m.parked[1:]
	}
	m.parked = append(m.parked, t)
	t.mu.Lock()
	t.expiry = time.AfterFunc(m.warmFor(), func() {
		t.log.Info("closing a parked page nobody came back for", "profile", t.profile)
		t.close()
	})
	t.mu.Unlock()
	m.pmu.Unlock()
	if evict != nil {
		evict.close()
	}
}

// unpark takes t out of the parked list, if it is in it.
func (m *Manager) unpark(t *tab) {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	for i, p := range m.parked {
		if p == t {
			m.parked = append(m.parked[:i], m.parked[i+1:]...)
			return
		}
	}
}
