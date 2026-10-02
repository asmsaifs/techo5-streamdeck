package web

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// ChromeEnv names a browser to use instead of the one found.
const ChromeEnv = "TECHO5_CHROME"

// ErrNoBrowser is what a tile says when there is no Chromium-based browser to run it in.
var ErrNoBrowser = errors.New("no Chrome, Edge or Chromium was found; install one, or set " + ChromeEnv + " to its program")

// FindChrome is the browser to run: the one named, or ChromeEnv's, or the first installed.
func FindChrome(named string) (string, error) {
	if named == "" {
		named = os.Getenv(ChromeEnv)
	}
	if named != "" {
		if _, err := os.Stat(named); err != nil {
			return "", fmt.Errorf("the browser %q is not there: %w", named, err)
		}
		return named, nil
	}
	for _, p := range chromeCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBrowser
}

// Manager runs the browsers the tiles are in. A site's logins live in its browser's profile, so
// there is one browser process per profile: every tile of the same profile is a tab of it, and it
// goes when its last tab does. Profiles are folders under Dir, separate from the user's own browser.
type Manager struct {
	Dir    string // where the profiles are: chrome-profiles in the config folder
	Chrome string // the browser's program; empty to look for one
	Log    *slog.Logger

	// WarmFor and WarmCount say how long and how many parked pages are kept (tab.go); zero is the
	// default, and a WarmCount below zero keeps none.
	WarmFor   time.Duration
	WarmCount int

	closed atomic.Bool
	pmu    sync.Mutex
	parked []*tab // oldest first

	mu       sync.Mutex
	root     context.Context
	stop     context.CancelFunc
	browsers map[string]*browser
}

// browser is one running Chrome, with the tabs that are using it.
type browser struct {
	ctx    context.Context // a tab is a child of it
	cancel func()
	tabs   int
	once   sync.Once // chromedp.Cancel panics when it is called twice
}

// NewManager is a Manager whose profiles live in dir.
func NewManager(dir string) *Manager {
	m := &Manager{Dir: dir, browsers: map[string]*browser{}}
	m.root, m.stop = context.WithCancel(context.Background())
	return m
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Close stops every browser and waits for them to be gone, so a profile folder is not still
// being written to when the caller goes on to use or remove it.
func (m *Manager) Close() {
	m.closed.Store(true) // nothing is parked from here on
	m.pmu.Lock()
	parked := m.parked
	m.parked = nil
	m.pmu.Unlock()
	for _, t := range parked {
		t.close()
	}
	m.mu.Lock()
	all := m.browsers
	m.browsers = map[string]*browser{}
	m.mu.Unlock()
	for _, b := range all {
		b.quit()
	}
	m.stop()
}

// quit stops the browser and waits for its process to exit.
func (b *browser) quit() {
	b.once.Do(func() {
		_ = chromedp.Cancel(b.ctx)
		b.cancel()
	})
}

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)

// acquire gives a tab's place in the browser of profile, starting it if it is not running.
func (m *Manager) acquire(profile string) (*browser, error) {
	if !profileName.MatchString(profile) {
		return nil, fmt.Errorf("%q is not a profile name: use lower case letters, digits, - and _", profile)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root.Err() != nil {
		return nil, errors.New("the browser manager is closed")
	}
	if b := m.browsers[profile]; b != nil && b.ctx.Err() == nil {
		b.tabs++
		return b, nil
	}
	path, err := FindChrome(m.Chrome)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(m.Dir, profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	reclaimProfile(dir)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(path),
		chromedp.UserDataDir(dir),
		chromedp.DisableGPU,
		chromedp.Flag("hide-scrollbars", true),
		// A tab that is being captured gets a "sharing this tab" bar, which takes 57 px off the page
		// area and so off every frame, and the Show's frames must be exactly its size.
		chromedp.Flag("disable-infobars", true),
		// A tab's sound is captured by the page itself (sound.go) with no one to accept the prompt.
		// Not --use-fake-ui-for-media-stream: with it Chrome offers the screen, not the tab.
		chromedp.Flag("auto-accept-this-tab-capture", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
	)
	// chromedp's defaults include mute-audio, which has to be undone: a muted tab's sound is silent
	// to the capture too. What keeps a page off the computer's speakers is the capture itself, which
	// asks for local playback to be suppressed (sound.go); only a tile that says "desktop" is not
	// captured.
	opts = append(opts, chromedp.Flag("mute-audio", false))
	actx, acancel := chromedp.NewExecAllocator(m.root, opts...)
	bctx, bcancel := chromedp.NewContext(actx, chromedp.WithErrorf(quiet))
	// Started now rather than with the first tab, so a browser that cannot start says so at once.
	if err := chromedp.Run(bctx); err != nil {
		bcancel()
		acancel()
		return nil, fmt.Errorf("the browser would not start: %w", err)
	}
	b := &browser{ctx: bctx, cancel: func() { bcancel(); acancel() }, tabs: 1}
	m.browsers[profile] = b
	m.log().Info("browser started", "profile", profile, "program", path)
	return b, nil
}

// release gives a tab's place back; the browser goes with the last one.
func (m *Manager) release(profile string, b *browser) {
	m.mu.Lock()
	b.tabs--
	last := b.tabs <= 0
	if last && m.browsers[profile] == b {
		delete(m.browsers, profile)
	}
	m.mu.Unlock()
	if last {
		b.quit() // outside the lock: waiting for a process to exit must not hold up another tile
	}
}

// quiet is where chromedp's own complaints go: events from a newer Chrome than it knows the names
// of ("unhandled node event"), which say nothing about us. The rest goes to the log at debug.
func quiet(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "unhandled") {
		return
	}
	slog.Debug("chromedp", "said", msg)
}

// openTab opens a window of its own and returns a context to drive it by and a function that closes
// it. A window of its own because Chrome draws only the page in front of a window, and tabs
// side by side in one would starve each other of frames; and a hidden page gives no frames at all.
func (b *browser) openTab(size image.Point) (context.Context, func(), error) {
	var id target.ID
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		id, err = target.CreateTarget("about:blank").WithNewWindow(true).
			// Big enough for the screen to fit in it, which a window's default size is not.
			WithWidth(int64(size.X)).WithHeight(int64(size.Y)).
			Do(cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Browser))
		return err
	})); err != nil {
		return nil, nil, err
	}
	// No options: a tab of a browser already running takes none of the browser's, and chromedp
	// panics if it is given one.
	tab, cancel := chromedp.NewContext(b.ctx, chromedp.WithTargetID(id))
	if err := fitWindow(tab, size); err != nil {
		cancel()
		return nil, nil, err
	}
	return tab, func() {
		cancel()
		// A target that was attached to rather than created by chromedp is left open by cancel.
		_ = chromedp.Run(b.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return target.CloseTarget(id).Do(cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Browser))
		}))
	}, nil
}

// fitWindow sizes the tab's window so the page area inside it is exactly size. The frames Chrome
// streams are of the page area of the real window, whatever a device-metrics override says, and a
// window's own frame (its title bar, its borders) takes some of the size it is made with.
func fitWindow(tab context.Context, size image.Point) error {
	var m struct{ IW, IH, OW, OH int }
	return chromedp.Run(tab,
		chromedp.Evaluate(`({iw: innerWidth, ih: innerHeight, ow: outerWidth, oh: outerHeight})`, &m),
		chromedp.ActionFunc(func(ctx context.Context) error {
			id, _, err := cdpbrowser.GetWindowForTarget().Do(ctx)
			if err != nil {
				return err
			}
			return cdpbrowser.SetWindowBounds(id, &cdpbrowser.Bounds{
				Width: int64(size.X + m.OW - m.IW), Height: int64(size.Y + m.OH - m.IH)}).Do(ctx)
		}),
	)
}
