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

	"github.com/asmsaifs/techo5-streamdeck/internal/actions"
	model "github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/tiles"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Runner runs the actions that do something on the computer. Navigation (page, back) is the
// deck's own and never reaches it. internal/actions provides the real one (step 1.5).
type Runner interface {
	Run(ctx context.Context, a *model.Action) error
}

// Toggler is what a Runner that keeps toggle state also provides: whether the toggle on the button
// with that key (actions.ButtonKey) is on, so the deck can ring it.
type Toggler interface {
	On(key string) bool
}

// Tiler reads the values of live tiles; tiles.Cache is the real one. Shows share one, so a tile
// that two of them display is read once.
type Tiler interface {
	Get(ctx context.Context, t *model.Tile) tiles.Value
}

// OnSetter is what a Runner that keeps toggle state also provides: to be told the state is really
// on or off, as a state tile found it.
type OnSetter interface {
	SetOn(key string, on bool)
}

// How long a button shows each look after a tap. The Show sends a tap only when the finger lifts,
// so "pressed" is not held down by the finger: it is a short flash that says the tap was seen.
const (
	pressFor = 80 * time.Millisecond
	flashFor = 150 * time.Millisecond

	// actionTimeout bounds one action at most, so nothing holds its button for ever; the run action
	// has a shorter timeout of its own.
	actionTimeout = 10 * time.Minute
)

// Source is the deck for one connected device.
type Source struct {
	cfg     func() *model.Config // the current config, which changes under hot reload
	profile string               // guarded by mu: SetProfile changes it
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

	// Tiles reads live tiles. Nil means buttons with a tile show no value. Set before Start.
	Tiles Tiler
	kick  chan struct{} // asks the tile loop to look at once, after a page change

	mu    sync.Mutex
	tvals map[string]*tileVal // the last reading of each tile, by "page/cell"
	stack []string            // the pages opened from home; empty on home
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
		frames: make(chan *image.RGBA, 1), state: render.State{}, gen: map[model.Cell]int{},
		kick: make(chan struct{}, 1), tvals: map[string]*tileVal{}}
}

func (s *Source) Start(ctx context.Context, size image.Point) error {
	if s.ctx != nil {
		return errors.New("deck: started twice")
	}
	s.size = size
	s.ctx, s.cancel = context.WithCancel(ctx)
	if s.profileNow() == nil {
		s.cancel()
		return fmt.Errorf("deck: there is no profile %q", s.Profile())
	}
	go func() {
		<-s.ctx.Done()
		s.renderMu.Lock() // not while a frame is being sent
		close(s.frames)
		s.renderMu.Unlock()
	}()
	s.Refresh()
	if s.Tiles != nil {
		go s.tileLoop()
	}
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
	return c.Profiles[s.Profile()]
}

// Profile is the profile being shown.
func (s *Source) Profile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profile
}

// SetProfile shows another profile, from its home page, and redraws. The config does not have to
// have it yet: a profile that is missing leaves the last picture up, as an edit that removes one does.
func (s *Source) SetProfile(name string) {
	s.mu.Lock()
	if s.profile == name {
		s.mu.Unlock()
		return
	}
	s.profile = name
	s.stack = nil
	s.forget()
	s.mu.Unlock()
	s.Refresh()
	s.kickTiles()
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
	prof := s.Profile()
	p := s.profileNow()
	if p == nil {
		return // removed by an edit; the last picture stays up
	}
	pg, name, _ := s.page(p)
	if pg == nil {
		return
	}
	s.mu.Lock()
	st := make(render.State, len(s.state))
	for c, v := range s.state {
		st[c] = v
	}
	s.mu.Unlock()
	// A toggle that is on is ringed, whatever else its button is showing.
	if t, ok := s.run.(Toggler); ok {
		for key, b := range pg.Buttons {
			if b == nil || b.Action == nil || b.Action.Type != "toggle" {
				continue
			}
			if c, err := model.ParseCell(key); err == nil && t.On(actions.ButtonKey(prof, name, c)) {
				v := st[c]
				v.On = true
				st[c] = v
			}
		}
	}
	// A live tile's value is its button's text; a state tile says whether it is on.
	s.mu.Lock()
	for key, b := range pg.Buttons {
		if b == nil || b.Tile == nil {
			continue
		}
		tv := s.tvals[name+"/"+key]
		c, err := model.ParseCell(key)
		if tv == nil || tv.sig != tileSig(b.Tile) || err != nil {
			continue
		}
		v := st[c]
		switch {
		case tv.val.Err != nil:
			v.Text = "—"
		case tv.val.On != nil:
			v.On = *tv.val.On
		default:
			v.Text = tv.val.Text
		}
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
	pg, name, _ := s.page(p)
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
	s.press(cell, b.Action, p, actions.ButtonKey(s.Profile(), name, cell))
}

func (s *Source) press(cell model.Cell, a *model.Action, p *model.Profile, key string) {
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
		s.kickTiles()
		return
	case "back":
		s.mu.Lock()
		if n := len(s.stack); n > 0 {
			s.stack = s.stack[:n-1]
		}
		s.forget()
		s.mu.Unlock()
		s.Refresh()
		s.kickTiles()
		return
	}
	go s.runAction(cell, a, key)
}

// runAction shows the button pressed, runs the action and flashes its result. The action starts
// at once; the look only waits for it so the press is never shorter than it takes to see.
func (s *Source) runAction(cell model.Cell, a *model.Action, key string) {
	n := s.set(cell, render.CellState{Pressed: true})
	s.Refresh()

	done := make(chan error, 1)
	go func() { done <- s.exec(a, key) }()
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

func (s *Source) exec(a *model.Action, key string) error {
	if s.run == nil {
		return fmt.Errorf("no action runner: cannot run %q", a.Type)
	}
	ctx, cancel := context.WithTimeout(actions.WithButton(s.ctx, key), actionTimeout)
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

// tileVal is the last reading of one tile.
type tileVal struct {
	sig  string // what was read, so an edited tile is not shown the old value
	val  tiles.Value
	at   time.Time
	busy bool
}

func tileSig(t *model.Tile) string { return fmt.Sprintf("%+v", *t) }

func (s *Source) kickTiles() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// tileLoop reads the tiles of the page being shown, each when its interval has passed, and redraws
// when a value changed. Pages not shown are not read: a Show on the home page does not poll a
// script that is on a sub-page. A slow reading never holds up the others or the deck.
func (s *Source) tileLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		s.pollTiles()
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		case <-s.kick:
		}
	}
}

func (s *Source) pollTiles() {
	p := s.profileNow()
	if p == nil {
		return
	}
	pg, name, _ := s.page(p)
	if pg == nil {
		return
	}
	for key, b := range pg.Buttons {
		if b == nil || b.Tile == nil {
			continue
		}
		cell, err := model.ParseCell(key)
		if err != nil {
			continue
		}
		tile, k, sig := b.Tile, name+"/"+key, tileSig(b.Tile)
		s.mu.Lock()
		tv := s.tvals[k]
		if tv == nil || tv.sig != sig {
			tv = &tileVal{sig: sig}
			s.tvals[k] = tv
		}
		due := !tv.busy && (tv.at.IsZero() || time.Since(tv.at) >= tiles.Every(tile)-100*time.Millisecond)
		if due {
			tv.busy = true
		}
		s.mu.Unlock()
		if due {
			go s.readTile(tv, tile, name, cell)
		}
	}
}

func (s *Source) readTile(tv *tileVal, tile *model.Tile, page string, cell model.Cell) {
	v := s.Tiles.Get(s.ctx, tile)
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	old, first := tv.val, tv.at.IsZero()
	tv.val, tv.at, tv.busy = v, time.Now(), false
	s.mu.Unlock()

	if v.Err != nil && (first || old.Err == nil || old.Err.Error() != v.Err.Error()) {
		s.log.Warn("tile", "type", tile.Type, "cell", cell.String(), "err", v.Err)
	}
	if v.On != nil {
		if o, ok := s.run.(OnSetter); ok {
			o.SetOn(actions.ButtonKey(s.Profile(), page, cell), *v.On)
		}
	}
	if first || v.Text != old.Text || (v.Err == nil) != (old.Err == nil) || (v.On == nil) != (old.On == nil) || (v.On != nil && *v.On != *old.On) {
		s.Refresh()
	}
}
