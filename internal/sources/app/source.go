package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
	"github.com/asmsaifs/techo5-streamdeck/internal/capture"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Spec says which window to show.
type Spec struct {
	// App matches the owning application's name, case-insensitively, as a substring.
	App string
	// Title is a regular expression the window's title must match. Empty: any title.
	Title string
	// Launch starts App when no window matches, then waits for one.
	Launch bool
	// Sound sends the app's sound to the Show (if the source has somewhere to send it, SetSound).
	Sound bool
	// Scroll turns vertical drags into wheel events instead of mouse drags: a Show has one finger,
	// and a list is scrolled far more often than something is dragged.
	Scroll bool
	// FPS caps the capture. Default 25.
	FPS int
}

// Manager makes sources. The zero value finds the helper itself.
type Manager struct {
	// Helper is the capture helper executable. Empty: HelperPath finds it.
	Helper string
	// Launch starts an application by name; the Core wires it to the open.app action. Without it
	// Spec.Launch fails.
	Launch func(ctx context.Context, app string) error
}

// helperName is the executable the current OS ships.
func helperName() string {
	switch runtime.GOOS {
	case "darwin":
		return "deckcap-mac"
	case "windows":
		return "deckcap-win.exe"
	}
	return "deckcap-linux"
}

// HelperPath finds the capture helper: $TECHO5_DECKCAP, then next to this program (where the app
// bundle and the installers put it), then the build output of the repository, for development.
func HelperPath() (string, error) {
	if p := os.Getenv("TECHO5_DECKCAP"); p != "" {
		return p, nil
	}
	name := helperName()
	var try []string
	if exe, err := os.Executable(); err == nil {
		try = append(try, filepath.Join(filepath.Dir(exe), name))
	}
	for _, rel := range []string{"helpers/mac/.build/release", "helpers/win/target/release", "helpers/linux"} {
		try = append(try, filepath.Join(rel, name))
	}
	for _, p := range try {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("the capture helper %s is not installed", name)
}

// ErrNoHelper says streaming apps is not available here; the message is for the user.
var ErrNoHelper = errors.New("streaming apps needs the capture helper, which was not found")

// startTimeout is how long the helper and the window may take to produce a first picture.
const startTimeout = 20 * time.Second

// launchTimeout is how long a launched app has to show a window.
const launchTimeout = 20 * time.Second

// Source is one application window shown to one Show.
type Source struct {
	m    *Manager
	spec Spec
	re   *regexp.Regexp

	snd func(f audio.Format, pcm []byte)
	// seen, if set, is told every helper event, for tests.
	seen func(capture.Event)

	ctx    context.Context
	cancel context.CancelFunc
	size   image.Point
	cl     *capture.Client

	fmu    sync.Mutex // guards frames' closing and err
	frames chan *image.RGBA
	closed bool
	err    error

	// The letterbox of the picture on the Show's screen, and the frame inside it, in the helper's
	// pixels. Touches are mapped through it.
	gmu    sync.Mutex
	dst    image.Rectangle
	fw, fh int
	// Where the finger was last inside the window, and whether it is down: scroll mode needs the
	// last y, and a finger that leaves the picture must still lift where it was.
	lastX, lastY int
	down         bool
}

// New is the source for spec. It opens nothing until Start.
func (m *Manager) New(spec Spec) (*Source, error) {
	if strings.TrimSpace(spec.App) == "" && strings.TrimSpace(spec.Title) == "" {
		return nil, errors.New("name an app or a window title")
	}
	s := &Source{m: m, spec: spec, frames: make(chan *image.RGBA, 1)}
	if spec.Title != "" {
		re, err := regexp.Compile("(?i)" + spec.Title)
		if err != nil {
			return nil, fmt.Errorf("the title %q is not a regular expression: %w", spec.Title, err)
		}
		s.re = re
	}
	return s, nil
}

// SetSound gives the source somewhere to send the app's sound. Call it before Start.
func (s *Source) SetSound(f func(f audio.Format, pcm []byte)) { s.snd = f }

// Pick chooses the window a spec means from a helper's list: the largest that matches.
func (s *Source) Pick(list []capture.Window) (capture.Window, bool) {
	var best capture.Window
	found := false
	app := strings.ToLower(strings.TrimSpace(s.spec.App))
	for _, w := range list {
		if app != "" && !strings.Contains(strings.ToLower(w.App), app) {
			continue
		}
		if s.re != nil && !s.re.MatchString(w.Title) {
			continue
		}
		if !found || w.W*w.H > best.W*best.H {
			best, found = w, true
		}
	}
	return best, found
}

func (s *Source) Start(ctx context.Context, size image.Point) error {
	if s.ctx != nil {
		return errors.New("app: started twice")
	}
	path := ""
	if s.m != nil {
		path = s.m.Helper
	}
	if path == "" {
		p, err := HelperPath()
		if err != nil {
			return fmt.Errorf("%w (%v)", ErrNoHelper, err)
		}
		path = p
	}
	s.size = size
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.cl = capture.New(path)
	go func() {
		err := s.cl.Run(s.ctx)
		if err != nil {
			s.fail(err)
		}
	}()
	go s.run()
	return nil
}

func (s *Source) Frames() <-chan *image.RGBA { return s.frames }

// Err is why the source stopped by itself, once Frames is closed: no helper, no permission, no such
// window, the window closed.
func (s *Source) Err() error {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	return s.err
}

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

// waitReady waits for the helper to say it can take commands.
func (s *Source) waitReady(ctx context.Context) error {
	for {
		select {
		case e := <-s.cl.Events():
			switch e.Event {
			case "ready":
				return nil
			case "error":
				return eventErr(e)
			}
		case <-ctx.Done():
			return errors.New("the capture helper did not start")
		}
	}
}

func eventErr(e capture.Event) error {
	if e.Code == "permission" {
		return fmt.Errorf("permission: %s", e.Msg)
	}
	if e.Msg == "" {
		return fmt.Errorf("the capture helper reports %s", e.Code)
	}
	return errors.New(e.Msg)
}

func (s *Source) find(ctx context.Context) (capture.Window, error) {
	launched := false
	deadline := time.Now().Add(launchTimeout)
	for {
		list, err := s.cl.List(ctx)
		if err != nil {
			return capture.Window{}, err
		}
		if w, ok := s.Pick(list); ok {
			return w, nil
		}
		if !s.spec.Launch || s.spec.App == "" {
			return capture.Window{}, fmt.Errorf("there is no window of %s", s.describe())
		}
		if !launched {
			if s.m == nil || s.m.Launch == nil {
				return capture.Window{}, errors.New("this build cannot start apps")
			}
			if err := s.m.Launch(ctx, s.spec.App); err != nil {
				return capture.Window{}, err
			}
			launched = true
		}
		if time.Now().After(deadline) {
			return capture.Window{}, fmt.Errorf("%s started but showed no window", s.spec.App)
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return capture.Window{}, ctx.Err()
		}
	}
}

func (s *Source) describe() string {
	switch {
	case s.spec.App != "" && s.spec.Title != "":
		return fmt.Sprintf("%s titled %q", s.spec.App, s.spec.Title)
	case s.spec.App != "":
		return s.spec.App
	}
	return fmt.Sprintf("a window titled %q", s.spec.Title)
}

func (s *Source) run() {
	defer s.finish()
	sctx, cancel := context.WithTimeout(s.ctx, startTimeout+launchTimeout)
	if err := s.waitReady(sctx); err != nil {
		cancel()
		s.fail(err)
		return
	}
	w, err := s.find(sctx)
	cancel()
	if err != nil {
		s.fail(err)
		return
	}
	// The helper scales by width. Ask for the width at which the window fills the screen one way,
	// so that the letterbox needs no scaling here.
	maxW := s.size.X
	if w.W > 0 && w.H > 0 {
		maxW = min(maxW, max(2, s.size.Y*w.W/w.H))
	}
	fps := s.spec.FPS
	if fps <= 0 {
		fps = 25
	}
	if err := s.cl.Start(w.ID, fps, maxW, s.spec.Sound && s.snd != nil); err != nil {
		s.fail(err)
		return
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case f := <-s.cl.Frames():
			s.push(s.letterbox(f))
		case a := <-s.cl.Audio():
			if s.spec.Sound && s.snd != nil {
				s.snd(audio.Format{Rate: a.Rate, Channels: a.Channels}, a.PCM)
			}
		case e := <-s.cl.Events():
			if s.seen != nil {
				s.seen(e)
			}
			if e.Event == "error" {
				switch e.Code {
				case "no_window":
					s.fail(errors.New("the window was closed"))
					return
				case "permission", "unsupported":
					s.fail(eventErr(e))
					return
				default:
					// Input that could not be posted and the like: the picture goes on.
				}
			}
		}
	}
}

// letterbox puts a frame on a black screen-sized picture, centred, scaled down to fit if need be,
// and notes where it went for touches.
func (s *Source) letterbox(f capture.Frame) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, s.size.X, s.size.Y))
	draw.Draw(out, out.Rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
	src := image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	// The helper sends BGRA.
	for i := 0; i+3 < len(f.BGRA); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = f.BGRA[i+2], f.BGRA[i+1], f.BGRA[i], 255
	}
	w, h := f.W, f.H
	if w > s.size.X || h > s.size.Y {
		k := min(float64(s.size.X)/float64(w), float64(s.size.Y)/float64(h))
		w, h = max(1, int(float64(w)*k)), max(1, int(float64(h)*k))
	}
	dst := image.Rect((s.size.X-w)/2, (s.size.Y-h)/2, (s.size.X-w)/2+w, (s.size.Y-h)/2+h)
	if w == f.W && h == f.H {
		draw.Draw(out, dst, src, image.Point{}, draw.Src)
	} else {
		xdraw.ApproxBiLinear.Scale(out, dst, src, src.Bounds(), xdraw.Src, nil)
	}
	s.gmu.Lock()
	s.dst, s.fw, s.fh = dst, f.W, f.H
	s.gmu.Unlock()
	return out
}

// toFrame maps a point on the Show's screen to the frame's pixels. ok is false in the bars.
func (s *Source) toFrame(x, y int) (fx, fy int, ok bool) {
	s.gmu.Lock()
	defer s.gmu.Unlock()
	if s.dst.Empty() || !image.Pt(x, y).In(s.dst) {
		return 0, 0, false
	}
	fx = (x - s.dst.Min.X) * s.fw / s.dst.Dx()
	fy = (y - s.dst.Min.Y) * s.fh / s.dst.Dy()
	return fx, fy, true
}

// Touch passes a touch to the window. Touches on the black bars do nothing; the helper clips the
// rest to the window, so nothing outside it can be clicked.
func (s *Source) Touch(t wire.Touch) {
	if s.cl == nil || s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	fx, fy, ok := s.toFrame(t.X, t.Y)
	if !ok {
		if t.T == "up" {
			s.release()
		}
		return
	}
	s.gmu.Lock()
	s.lastX = fx
	s.gmu.Unlock()
	if s.spec.Scroll && t.T != "tap" {
		s.scroll(t.T, fx, fy)
		return
	}
	s.gmu.Lock()
	s.lastY = fy
	s.down = t.T == "down" || (t.T == "move" && s.down)
	s.gmu.Unlock()
	_ = s.cl.Input(t.T, fx, fy)
}

// scroll turns a drag into wheel events: the content follows the finger.
func (s *Source) scroll(kind string, x, y int) {
	s.gmu.Lock()
	prev, was := s.lastY, s.down
	switch kind {
	case "down":
		s.lastY, s.down = y, true
	case "move":
		s.lastY = y
	case "up":
		s.down = false
	}
	s.gmu.Unlock()
	if kind == "move" && was && prev != y {
		_ = s.cl.Wheel(x, y, prev-y)
	}
}

// release ends a drag whose finger lifted outside the picture.
func (s *Source) release() {
	s.gmu.Lock()
	x, y, was := s.lastX, s.lastY, s.down
	s.down = false
	s.gmu.Unlock()
	if was && !s.spec.Scroll {
		_ = s.cl.Input("up", x, y)
	}
}

// Windows asks the helper for the windows that can be shown, for the editor's picker. It runs a
// helper of its own for the call.
func (m *Manager) Windows(ctx context.Context) ([]capture.Window, error) {
	path := ""
	if m != nil {
		path = m.Helper
	}
	if path == "" {
		p, err := HelperPath()
		if err != nil {
			return nil, fmt.Errorf("%w (%v)", ErrNoHelper, err)
		}
		path = p
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	cl := capture.New(path)
	go cl.Run(ctx)
	s := &Source{cl: cl}
	if err := s.waitReady(ctx); err != nil {
		return nil, err
	}
	return cl.List(ctx)
}
