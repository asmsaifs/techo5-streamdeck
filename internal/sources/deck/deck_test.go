package deck

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	model "github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/tiles"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

const testKey = "a-long-enough-test-key"

func testConfig(t *testing.T, profiles string) *model.Config {
	t.Helper()
	c, err := model.Decode([]byte(`{"version":1,"server":{"key":"` + testKey + `"},"profiles":` + profiles + `}`))
	if err != nil {
		t.Fatal(err)
	}
	c.Fill()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

const twoPages = `{"default":{"pages":{
	"home":{"buttons":{
		"0,0":{"label":"Say","icon":"lucide:play","action":{"type":"run","command":"say"}},
		"1,0":{"label":"Apps","icon":"lucide:folder","action":{"type":"page","page":"apps"}},
		"2,0":{"label":"Nothing"}}},
	"apps":{"buttons":{"0,0":{"label":"Safari","action":{"type":"open.app","app":"Safari"}}}}}}}`

type fakeRunner struct {
	mu   sync.Mutex
	ran  []string
	err  error
	gate chan struct{} // when set, Run waits for it
}

func (f *fakeRunner) Run(ctx context.Context, a *model.Action) error {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, a.Type)
	return f.err
}

func (f *fakeRunner) ranList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

var size = image.Pt(960, 480)

func start(t *testing.T, c *model.Config, run Runner) (*Source, *render.Renderer) {
	t.Helper()
	r := render.New(t.TempDir())
	s := New(func() *model.Config { return c }, "default", r, run, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx, size); err != nil {
		t.Fatal(err)
	}
	return s, r
}

func next(t *testing.T, s *Source) *image.RGBA {
	t.Helper()
	select {
	case img := <-s.Frames():
		return img
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
		return nil
	}
}

func quiet(t *testing.T, s *Source) {
	t.Helper()
	select {
	case <-s.Frames():
		t.Fatal("an unexpected frame")
	case <-time.After(300 * time.Millisecond):
	}
}

func tapAt(s *Source, c model.Cell, prof *model.Profile) {
	r := render.NewLayout(prof.Grid, size).Rect(c)
	s.Touch(wire.Touch{T: "tap", X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2})
}

func same(a, b *image.RGBA) bool { return string(a.Pix) == string(b.Pix) }

func TestFirstFrameIsHome(t *testing.T) {
	c := testConfig(t, twoPages)
	s, r := start(t, c, nil)
	want := r.Grid(c.Profiles["default"], c.Profiles["default"].Pages["home"], size, nil)
	if !same(next(t, s), want) {
		t.Error("the first frame is not the home page")
	}
}

func TestFolderAndBack(t *testing.T) {
	c := testConfig(t, twoPages)
	p := c.Profiles["default"]
	s, _ := start(t, c, nil)
	home := next(t, s)

	tapAt(s, model.Cell{Col: 1, Row: 0}, p)
	apps := next(t, s)
	if same(apps, home) {
		t.Fatal("the folder did not open")
	}
	// The sub-page got a Back button in its first free cell from the bottom left.
	tapAt(s, model.Cell{Col: 0, Row: p.Grid.Rows - 1}, p)
	if back := next(t, s); !same(back, home) {
		t.Error("Back did not return to home")
	}
	// Back on home does nothing but draw home again; a tap on a gap or an empty cell nothing.
	quiet(t, s)
	s.Touch(wire.Touch{T: "tap", X: 0, Y: 0})
	tapAt(s, model.Cell{Col: 4, Row: 2}, p)
	tapAt(s, model.Cell{Col: 2, Row: 0}, p) // a button with no action
	quiet(t, s)
}

func TestDragsAreIgnored(t *testing.T) {
	c := testConfig(t, twoPages)
	p := c.Profiles["default"]
	r := &fakeRunner{}
	s, _ := start(t, c, r)
	next(t, s)
	cell := render.NewLayout(p.Grid, size).Rect(model.Cell{})
	for _, k := range []string{"down", "move", "up", "hold", ""} {
		s.Touch(wire.Touch{T: k, X: cell.Min.X + 5, Y: cell.Min.Y + 5})
	}
	quiet(t, s)
	if got := r.ranList(); len(got) != 0 {
		t.Errorf("ran %v", got)
	}
}

// A tap on an action shows pressed, runs it, flashes the result, and goes back to plain.
func TestActionPressesFlashesAndClears(t *testing.T) {
	for _, tt := range []struct {
		name  string
		err   error
		flash render.Flash
	}{{"ok", nil, render.FlashOK}, {"err", errors.New("boom"), render.FlashErr}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConfig(t, twoPages)
			p := c.Profiles["default"]
			r := &fakeRunner{err: tt.err}
			s, rd := start(t, c, r)
			home := next(t, s)
			tapAt(s, model.Cell{}, p)
			cell := model.Cell{}
			look := func(f render.CellState) *image.RGBA {
				return rd.Grid(p, p.Pages["home"], size, render.State{cell: f})
			}
			if !same(next(t, s), look(render.CellState{Pressed: true})) {
				t.Error("first frame after the tap is not the pressed look")
			}
			if !same(next(t, s), look(render.CellState{Flash: tt.flash})) {
				t.Error("second frame is not the flash")
			}
			if !same(next(t, s), home) {
				t.Error("third frame is not plain again")
			}
			if got := r.ranList(); len(got) != 1 || got[0] != "run" {
				t.Errorf("ran %v", got)
			}
		})
	}
}

func TestActionWithoutARunnerFlashesError(t *testing.T) {
	c := testConfig(t, twoPages)
	p := c.Profiles["default"]
	s, rd := start(t, c, nil)
	next(t, s)
	tapAt(s, model.Cell{}, p)
	next(t, s) // pressed
	want := rd.Grid(p, p.Pages["home"], size, render.State{{}: {Flash: render.FlashErr}})
	if !same(next(t, s), want) {
		t.Error("no error flash")
	}
}

// A slow action does not stop the deck answering other taps, and its button stays pressed until
// it is done.
func TestSlowActionDoesNotBlockTheDeck(t *testing.T) {
	c := testConfig(t, twoPages)
	p := c.Profiles["default"]
	r := &fakeRunner{gate: make(chan struct{})}
	s, rd := start(t, c, r)
	next(t, s)
	tapAt(s, model.Cell{}, p)
	next(t, s) // pressed
	tapAt(s, model.Cell{Col: 1, Row: 0}, p)
	if got := next(t, s); same(got, rd.Grid(p, p.Pages["home"], size, nil)) {
		t.Error("the folder did not open while an action ran")
	}
	close(r.gate)
}

// Changing the config redraws; losing the page the deck is on returns it to home.
func TestReload(t *testing.T) {
	c := testConfig(t, twoPages)
	p := c.Profiles["default"]
	var mu sync.Mutex
	cur := c
	r := render.New(t.TempDir())
	s := New(func() *model.Config { mu.Lock(); defer mu.Unlock(); return cur }, "default", r, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, size); err != nil {
		t.Fatal(err)
	}
	home := next(t, s)
	tapAt(s, model.Cell{Col: 1, Row: 0}, p)
	next(t, s)

	mu.Lock()
	cur = testConfig(t, `{"default":{"pages":{"home":{"buttons":{"0,0":{"label":"New"}}}}}}`)
	mu.Unlock()
	s.Refresh()
	got := next(t, s)
	if same(got, home) {
		t.Error("the new config was not drawn")
	}
	if !same(got, r.Grid(cur.Profiles["default"], cur.Profiles["default"].Pages["home"], size, nil)) {
		t.Error("with its page gone the deck is not on home")
	}
}

func TestUnknownProfileFailsToStart(t *testing.T) {
	c := testConfig(t, twoPages)
	s := New(func() *model.Config { return c }, "nope", render.New(t.TempDir()), nil, nil)
	if err := s.Start(context.Background(), size); err == nil {
		t.Error("started with no profile")
	}
}

func TestFramesCloseWhenTheSourceStops(t *testing.T) {
	c := testConfig(t, twoPages)
	s, _ := start(t, c, nil)
	next(t, s)
	s.Close()
	select {
	case _, ok := <-s.Frames():
		if ok {
			t.Error("a frame after Close")
		}
	case <-time.After(time.Second):
		t.Error("Frames was not closed")
	}
	s.Refresh() // must not panic on a closed channel
}

func TestAddBack(t *testing.T) {
	c := testConfig(t, `{"default":{"grid":{"cols":2,"rows":2,"gap":8,"radius":14},"pages":{
		"home":{},
		"free":{"buttons":{"0,1":{"label":"x"}}},
		"full":{"buttons":{"0,0":{"label":"a"},"1,0":{"label":"b"},"0,1":{"label":"c"},"1,1":{"label":"d"}}},
		"own":{"buttons":{"1,0":{"action":{"type":"back"}}}}}}}`)
	p := c.Profiles["default"]
	for _, tt := range []struct {
		page string
		want *model.Cell // nil: nothing added
	}{
		{"free", &model.Cell{Col: 1, Row: 1}},
		{"full", &model.Cell{Col: 0, Row: 1}},
		{"own", nil},
		{"home", &model.Cell{Col: 0, Row: 1}},
	} {
		got, cell := addBack(p, p.Pages[tt.page])
		if (cell == nil) != (tt.want == nil) || cell != nil && *cell != *tt.want {
			t.Errorf("%s: back at %v, want %v", tt.page, cell, tt.want)
			continue
		}
		if cell != nil && got.Buttons[cell.String()].Action.Type != "back" {
			t.Errorf("%s: the added button is not Back", tt.page)
		}
		if len(p.Pages[tt.page].Buttons) != len(got.Buttons)-btoi(cell != nil && p.Pages[tt.page].Buttons[cell.String()] == nil) {
			t.Errorf("%s: the page in the config was changed", tt.page)
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// toggleRunner is a Runner that keeps toggle state the way actions.Registry does.
type toggleRunner struct {
	fakeRunner
	on map[string]bool
}

func (r *toggleRunner) On(key string) bool { return r.on[key] }

// A toggle that is on is drawn ringed, and stays so after its press flash has cleared.
func TestToggleIsRinged(t *testing.T) {
	c := testConfig(t, `{"default":{"pages":{"home":{"buttons":{
		"0,0":{"label":"Mute","action":{"type":"toggle","on":{"type":"run"},"off":{"type":"run"}}}}}}}}`)
	p := c.Profiles["default"]
	r := &toggleRunner{on: map[string]bool{}}
	s, rd := start(t, c, r)
	next(t, s)
	r.on["default/home/0,0"] = true
	s.Refresh()
	want := rd.Grid(p, p.Pages["home"], size, render.State{{}: {On: true}})
	if !same(next(t, s), want) {
		t.Error("an on toggle is not ringed")
	}
	// While flashing, the ring is kept.
	tapAt(s, model.Cell{}, p)
	pressed := rd.Grid(p, p.Pages["home"], size, render.State{{}: {Pressed: true, On: true}})
	if !same(next(t, s), pressed) {
		t.Error("the pressed look of an on toggle lost its ring")
	}
	next(t, s) // flash
	if !same(next(t, s), want) {
		t.Error("the ring did not stay after the flash")
	}
}

type fakeTiles struct {
	mu    sync.Mutex
	vals  map[string]tiles.Value // by the tile's command
	reads []string
}

func (f *fakeTiles) Get(_ context.Context, t *model.Tile) tiles.Value {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, t.Command)
	return f.vals[t.Command]
}

func (f *fakeTiles) set(cmd string, v tiles.Value) {
	f.mu.Lock()
	f.vals[cmd] = v
	f.mu.Unlock()
}

func (f *fakeTiles) readList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reads...)
}

type onRunner struct {
	fakeRunner
	mu2 sync.Mutex
	on  map[string]bool
}

func (o *onRunner) SetOn(key string, on bool) {
	o.mu2.Lock()
	defer o.mu2.Unlock()
	o.on[key] = on
}

func startTiles(t *testing.T, c *model.Config, run Runner, tl Tiler) (*Source, *render.Renderer) {
	t.Helper()
	r := render.New(t.TempDir())
	s := New(func() *model.Config { return c }, "default", r, run, nil)
	s.Tiles = tl
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx, size); err != nil {
		t.Fatal(err)
	}
	return s, r
}

const tileDeck = `{"default":{"pages":{
	"home":{"buttons":{
		"0,0":{"label":"Mail","tile":{"type":"script","command":"mail","every":1}},
		"1,0":{"label":"Mic","tile":{"type":"state","command":"micstate","every":1},"action":{"type":"toggle","on":{"type":"delay","ms":0},"off":{"type":"delay","ms":0}}},
		"2,0":{"label":"Apps","action":{"type":"page","page":"apps"}}}},
	"apps":{"buttons":{"0,0":{"label":"Load","tile":{"type":"script","command":"load","every":1}}}}}}}`

func TestTileValueIsDrawnAndUpdated(t *testing.T) {
	c := testConfig(t, tileDeck)
	prof := c.Profiles["default"]
	ft := &fakeTiles{vals: map[string]tiles.Value{"mail": {Text: "3 new"}, "micstate": {On: new(bool)}}}
	s, r := startTiles(t, c, &fakeRunner{}, ft)

	want := func(text string, micOn bool) *image.RGBA {
		return r.Grid(prof, prof.Pages["home"], size, render.State{
			{Col: 0, Row: 0}: {Text: text},
			{Col: 1, Row: 0}: {On: micOn},
		})
	}
	// Frames arrive as readings do; the last one is what counts.
	settle := func(w *image.RGBA) {
		t.Helper()
		deadline := time.After(4 * time.Second)
		for {
			select {
			case img := <-s.Frames():
				if same(img, w) {
					return
				}
			case <-deadline:
				t.Fatal("the deck never showed the expected tiles")
			}
		}
	}
	settle(want("3 new", false))

	ft.set("mail", tiles.Value{Text: "5 new"})
	settle(want("5 new", false)) // read again after its second

	on := true
	ft.set("micstate", tiles.Value{On: &on})
	settle(want("5 new", true))

	ft.set("mail", tiles.Value{Err: errors.New("boom")})
	settle(want("—", true))
}

func TestStateTileTellsTheToggle(t *testing.T) {
	c := testConfig(t, tileDeck)
	on := true
	ft := &fakeTiles{vals: map[string]tiles.Value{"micstate": {On: &on}}}
	run := &onRunner{on: map[string]bool{}}
	startTiles(t, c, run, ft)
	time.Sleep(400 * time.Millisecond)
	run.mu2.Lock()
	defer run.mu2.Unlock()
	if !run.on["default/home/1,0"] {
		t.Errorf("the toggle was told %v, want default/home/1,0 on", run.on)
	}
}

func TestOnlyTheShownPageIsRead(t *testing.T) {
	c := testConfig(t, tileDeck)
	prof := c.Profiles["default"]
	ft := &fakeTiles{vals: map[string]tiles.Value{"load": {Text: "12%"}}}
	s, _ := startTiles(t, c, &fakeRunner{}, ft)
	time.Sleep(400 * time.Millisecond)
	for _, r := range ft.readList() {
		if r == "load" {
			t.Fatal("a tile on a page that is not shown was read")
		}
	}
	tapAt(s, model.Cell{Col: 2, Row: 0}, prof)
	deadline := time.After(2 * time.Second)
	for {
		found := false
		for _, r := range ft.readList() {
			found = found || r == "load"
		}
		if found {
			break
		}
		select {
		case <-deadline:
			t.Fatal("opening the page did not read its tile at once")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestNoTilerShowsNoValue(t *testing.T) {
	c := testConfig(t, tileDeck)
	prof := c.Profiles["default"]
	s, r := start(t, c, nil)
	if !same(next(t, s), r.Grid(prof, prof.Pages["home"], size, nil)) {
		t.Error("tiles with no reader changed the picture")
	}
	quiet(t, s)
}
