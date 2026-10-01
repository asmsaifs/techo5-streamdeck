// Package deck is the button grid drawn in Go: a Source whose frames are the current page of a
// profile, and whose touches press its buttons.
//
// The package is called deck as its directory is; the model, internal/deck, is imported as model.
package deck

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	model "github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Runner runs the actions that do something on the computer. Navigation (page, back) is the
// deck's own and never reaches it. internal/actions provides the real one (step 1.5).
type Runner interface {
	Run(ctx context.Context, a *model.Action) error
}

// How long a button shows each look after a tap. The Show sends a tap only when the finger lifts,
// so "pressed" is not held down by the finger: it is a short flash that says the tap was seen.
const (
	pressFor = 80 * time.Millisecond
	flashFor = 150 * time.Millisecond

	// actionTimeout bounds one action, so a hung script does not hold its button for ever.
	actionTimeout = 30 * time.Second
)

// Source is the deck for one connected device.
type Source struct {
	cfg     func() *model.Config // the current config, which changes under hot reload
	profile string
	r       *render.Renderer
	run     Runner
	log     *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	size   image.Point
	frames chan *image.RGBA

	// renderMu makes snapshot, render and send one step, so that a frame drawn earlier can never
	// arrive after one drawn later.
	renderMu sync.Mutex

	mu    sync.Mutex
	stack []string // the pages opened from home; empty on home
	state render.State
	gen   map[model.Cell]int // which tap last set a cell's state, so an old timer cannot clear a new one
}

// New is the deck of profile for a device. run may be nil, and then actions other than page and
// back fail visibly.
func New(cfg func() *model.Config, profile string, r *render.Renderer, run Runner, log *slog.Logger) *Source {
	if log == nil {
		log = slog.Default()
	}
	return &Source{cfg: cfg, profile: profile, r: r, run: run, log: log,
		frames: make(chan *image.RGBA, 1), state: render.State{}, gen: map[model.Cell]int{}}
}

func (s *Source) Start(ctx context.Context, size image.Point) error {
	if s.ctx != nil {
		return errors.New("deck: started twice")
	}
	s.size = size
	s.ctx, s.cancel = context.WithCancel(ctx)
	if s.profileNow() == nil {
		s.cancel()
		return fmt.Errorf("deck: there is no profile %q", s.profile)
	}
	go func() {
		<-s.ctx.Done()
		s.renderMu.Lock() // not while a frame is being sent
		close(s.frames)
		s.renderMu.Unlock()
	}()
	s.Refresh()
	return nil
}

func (s *Source) Frames() <-chan *image.RGBA { return s.frames }

func (s *Source) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *Source) profileNow() *model.Profile {
	c := s.cfg()
	if c == nil {
		return nil
	}
	return c.Profiles[s.profile]
}

// page is the page being shown, and with it the cell of a Back button the deck added, if it did.
// A page the config no longer has (it was edited while the deck was on it) is replaced by home.
func (s *Source) page(p *model.Profile) (pg *model.Page, name string, back *model.Cell) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.stack) > 0 {
		if pg := p.Pages[s.stack[len(s.stack)-1]]; pg != nil {
			name = s.stack[len(s.stack)-1]
			withBack, cell := addBack(p, pg)
			return withBack, name, cell
		}
		s.stack = s.stack[:len(s.stack)-1]
	}
	return p.Pages[model.HomePage], model.HomePage, nil
}

// addBack is pg with a Back button added, so a sub-page always has a way out, unless the page
// has one of its own. It takes the first free cell from the bottom left; cell is nil when it
// added nothing.
func addBack(p *model.Profile, pg *model.Page) (*model.Page, *model.Cell) {
	for _, b := range pg.Buttons {
		if b != nil && b.Action != nil && b.Action.Type == "back" {
			return pg, nil
		}
	}
	free := model.Cell{Col: 0, Row: p.Grid.Rows - 1} // taken over if the page has no free cell
	for row := p.Grid.Rows - 1; row >= 0; row-- {
		for col := 0; col < p.Grid.Cols; col++ {
			c := model.Cell{Col: col, Row: row}
			if pg.Buttons[c.String()] == nil {
				free = c
				row = -1
				break
			}
		}
	}
	buttons := make(map[string]*model.Button, len(pg.Buttons)+1)
	for k, b := range pg.Buttons {
		buttons[k] = b
	}
	buttons[free.String()] = &model.Button{Label: "Back", Icon: "lucide:arrow-left", Action: &model.Action{Type: "back"}}
	return &model.Page{Buttons: buttons}, &free
}

// Refresh draws the deck again and sends the picture: after a state change, and by the server
// when the config was reloaded.
func (s *Source) Refresh() {
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	s.renderMu.Lock()
	defer s.renderMu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	p := s.profileNow()
	if p == nil {
		return // removed by an edit; the last picture stays up
	}
	pg, _, _ := s.page(p)
	if pg == nil {
		return
	}
	s.mu.Lock()
	st := make(render.State, len(s.state))
	for c, v := range s.state {
		st[c] = v
	}
	s.mu.Unlock()
	img := s.r.Grid(p, pg, s.size, st)
	// Latest wins: replace a frame the encoder has not taken yet.
	select {
	case <-s.frames:
	default:
	}
	s.frames <- img
}

// Touch presses the button under a tap. The Show sends nothing else the deck can use: down, move
// and up are drags, which mean nothing on a grid.
func (s *Source) Touch(t wire.Touch) {
	if t.T != "tap" || s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	p := s.profileNow()
	if p == nil {
		return
	}
	pg, _, _ := s.page(p)
	if pg == nil {
		return
	}
	cell, ok := render.NewLayout(p.Grid, s.size).CellAt(image.Pt(t.X, t.Y))
	if !ok {
		return
	}
	b := pg.Buttons[cell.String()]
	if b == nil || b.Action == nil {
		return
	}
	s.press(cell, b.Action, p)
}

func (s *Source) press(cell model.Cell, a *model.Action, p *model.Profile) {
	switch a.Type {
	case "page":
		var v struct {
			Page string `json:"page"`
		}
		if err := a.Params(&v); err != nil || p.Pages[v.Page] == nil {
			s.flashOnly(cell, render.FlashErr)
			return
		}
		s.mu.Lock()
		s.stack = append(s.stack, v.Page)
		s.forget()
		s.mu.Unlock()
		s.Refresh()
		return
	case "back":
		s.mu.Lock()
		if n := len(s.stack); n > 0 {
			s.stack = s.stack[:n-1]
		}
		s.forget()
		s.mu.Unlock()
		s.Refresh()
		return
	}
	go s.runAction(cell, a)
}

// runAction shows the button pressed, runs the action and flashes its result. The action starts
// at once; the look only waits for it so the press is never shorter than it takes to see.
func (s *Source) runAction(cell model.Cell, a *model.Action) {
	n := s.set(cell, render.CellState{Pressed: true})
	s.Refresh()

	done := make(chan error, 1)
	go func() { done <- s.exec(a) }()
	pressed := time.NewTimer(pressFor)
	defer pressed.Stop()
	var err error
	select {
	case err = <-done:
		<-pressed.C
	case <-s.ctx.Done():
		return
	}
	if err != nil {
		s.log.Warn("action failed", "type", a.Type, "err", err)
	}
	f := render.FlashOK
	if err != nil {
		f = render.FlashErr
	}
	if s.setIf(cell, n, render.CellState{Flash: f}) {
		s.Refresh()
		s.after(flashFor, cell, n)
	}
}

func (s *Source) exec(a *model.Action) error {
	if s.run == nil {
		return fmt.Errorf("no action runner: cannot run %q", a.Type)
	}
	ctx, cancel := context.WithTimeout(s.ctx, actionTimeout)
	defer cancel()
	return s.run.Run(ctx, a)
}

// flashOnly shows a result with no action behind it: a button that points nowhere.
func (s *Source) flashOnly(cell model.Cell, f render.Flash) {
	n := s.set(cell, render.CellState{Flash: f})
	s.Refresh()
	go s.after(flashFor, cell, n)
}

// after clears the cell's look when d has passed, if no later tap has changed it since.
func (s *Source) after(d time.Duration, cell model.Cell, n int) {
	select {
	case <-time.After(d):
	case <-s.ctx.Done():
		return
	}
	if s.setIf(cell, n, render.CellState{}) {
		s.Refresh()
	}
}

// set gives a cell a look, and returns the number that says it is this tap's.
func (s *Source) set(c model.Cell, v render.CellState) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen[c]++
	s.put(c, v)
	return s.gen[c]
}

// setIf changes the look only if tap n is still the last to have touched the cell.
func (s *Source) setIf(c model.Cell, n int, v render.CellState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen[c] != n {
		return false
	}
	s.put(c, v)
	return true
}

func (s *Source) put(c model.Cell, v render.CellState) {
	if v == (render.CellState{}) {
		delete(s.state, c)
	} else {
		s.state[c] = v
	}
}

// forget drops every cell's look, and with it the right of a pending timer to change one: the
// old page's looks do not carry over to the new. The caller holds mu.
func (s *Source) forget() {
	s.state = render.State{}
	s.gen = map[model.Cell]int{}
}
