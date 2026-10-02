package render

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

var update = flag.Bool("update", false, "rewrite the golden PNGs")

func TestParseColour(t *testing.T) {
	tests := []struct {
		in      string
		want    color.RGBA
		wantErr bool
	}{
		{"#4f8cff", color.RGBA{0x4f, 0x8c, 0xff, 255}, false},
		{"#FFF", color.RGBA{255, 255, 255, 255}, false},
		{"#f80", color.RGBA{0xff, 0x88, 0, 255}, false},
		{"black", color.RGBA{}, true},
		{"#12345", color.RGBA{}, true},
		{"#gg0000", color.RGBA{}, true},
		{"", color.RGBA{}, true},
	}
	for _, tt := range tests {
		got, err := ParseColour(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseColour(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestLayout(t *testing.T) {
	for _, size := range []image.Point{{960, 480}, {1280, 800}, {1000, 333}} {
		g := deck.Grid{Cols: 5, Rows: 3, Gap: 8, Radius: 14}
		l := NewLayout(g, size)
		var rects []image.Rectangle
		for row := 0; row < g.Rows; row++ {
			for col := 0; col < g.Cols; col++ {
				c := deck.Cell{Col: col, Row: row}
				r := l.Rect(c)
				if r.Empty() || !r.In(image.Rectangle{Max: size}) {
					t.Fatalf("%v: cell %v is %v", size, c, r)
				}
				for _, o := range rects {
					if r.Overlaps(o) {
						t.Errorf("%v: %v overlaps %v", size, r, o)
					}
				}
				rects = append(rects, r)
				// The middle of a cell is on that cell, and its corner pixels too.
				for _, p := range []image.Point{r.Min.Add(r.Size().Div(2)), r.Min, r.Max.Sub(image.Pt(1, 1))} {
					if got, ok := l.CellAt(p); !ok || got != c {
						t.Errorf("%v: CellAt(%v) = %v, %v; want %v", size, p, got, ok, c)
					}
				}
			}
		}
	}
	l := NewLayout(deck.DefaultGrid, image.Pt(960, 480))
	// The gap between two cells and the margin are on no cell.
	r0, r1 := l.Rect(deck.Cell{Col: 0, Row: 0}), l.Rect(deck.Cell{Col: 1, Row: 0})
	for _, p := range []image.Point{{r0.Max.X + 1, 10}, {r1.Min.X - 1, 10}, {0, 0}, {959, 479}, {-1, 5}} {
		if c, ok := l.CellAt(p); ok {
			t.Errorf("CellAt(%v) = %v; want no cell", p, c)
		}
	}
	// Gaps are even to the pixel.
	if gap := r1.Min.X - r0.Max.X; gap != 8 {
		t.Errorf("gap %d", gap)
	}
	if !l.Rect(deck.Cell{Col: 5, Row: 0}).Empty() || !l.Rect(deck.Cell{Col: 0, Row: 3}).Empty() {
		t.Error("a cell off the grid has a rectangle")
	}
	// A taller screen scales by the smaller ratio.
	if s := NewLayout(deck.DefaultGrid, image.Pt(1280, 800)).Scale; s < 1.33 || s > 1.34 {
		t.Errorf("scale %v", s)
	}
}

func TestWrapLabel(t *testing.T) {
	// One pixel a rune, so a width is a character count.
	measure := func(s string) float64 { return float64(len([]rune(s))) }
	tests := []struct {
		name      string
		text      string
		width     float64
		maxLines  int
		want      []string
		wantWhole bool
	}{
		{"fits", "Mute", 10, 2, []string{"Mute"}, true},
		{"wraps at a space", "Home Assistant", 10, 2, []string{"Home", "Assistant"}, true},
		{"one line only", "Home Assistant", 20, 1, []string{"Home Assistant"}, true},
		{"cut with an ellipsis", "One two three four", 8, 2, []string{"One two", "three…"}, false},
		{"one line cut", "Home Assistant", 10, 1, []string{"Home…"}, false},
		{"a long word is broken", "Supercalifragilistic", 10, 2, []string{"Supercalif", "ragilistic"}, true},
		{"a long word is cut", "Supercalifragilisticexpialidocious", 10, 2, []string{"Supercalif", "ragilisti…"}, false},
		{"runes are not split", "ありがとうございます", 4, 3, []string{"ありがと", "うございま", "す"}[:0], true},
		{"spaces are collapsed", "  a   b  ", 10, 1, []string{"a b"}, true},
		{"empty", "   ", 10, 2, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, whole := wrapLabel(tt.text, tt.width, tt.maxLines, measure)
			if tt.name == "runes are not split" {
				// Only that no line is wider than allowed and nothing is lost.
				if strings.Join(got, "") != tt.text || !whole {
					t.Errorf("got %q %v", got, whole)
				}
				for _, l := range got {
					if measure(l) > tt.width {
						t.Errorf("%q is too wide", l)
					}
				}
				return
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") || whole != tt.wantWhole {
				t.Errorf("got %q %v; want %q %v", got, whole, tt.want, tt.wantWhole)
			}
		})
	}
}

// sample is a profile that shows every look a button has.
func sample(t *testing.T, iconsDir string) (*deck.Profile, *deck.Page, State) {
	t.Helper()
	// A 64 by 32 PNG, to be drawn as an image icon and letterboxed into a square.
	pic := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			pic.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 8), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, pic); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(iconsDir, "wide.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	b := func(label, icon string) *deck.Button { return &deck.Button{Label: label, Icon: icon} }
	p := &deck.Profile{Grid: deck.DefaultGrid, Theme: deck.DefaultTheme}
	pg := &deck.Page{Buttons: map[string]*deck.Button{
		"0,0": b("Mute", "lucide:mic-off"),
		"1,0": b("Apps", "lucide:folder"),
		"2,0": b("", "lucide:house"),
		"3,0": b("Just a label", ""),
		"4,0": b("A very long label that cannot fit on two lines at all", "lucide:globe"),
		"0,1": b("Pressed", "lucide:play"),
		"1,1": b("Toggle on", "lucide:lock"),
		"2,1": b("Flash ok", "lucide:rocket"),
		"3,1": b("Flash err", "lucide:terminal"),
		"4,1": b("Disabled", "lucide:sun"),
		"0,2": b("Image", "wide.png"),
		"1,2": b("No icon yet", "lucide:no-such-icon"),
		"2,2": b("Emoji", "👋"),
		"3,2": b("Home Assistant", "lucide:keyboard"),
		"4,2": b("../etc/passwd", "../../etc/passwd.png"),
	}}
	st := State{
		{Col: 0, Row: 1}: {Pressed: true},
		{Col: 1, Row: 1}: {On: true},
		{Col: 2, Row: 1}: {Flash: FlashOK},
		{Col: 3, Row: 1}: {Flash: FlashErr},
		{Col: 4, Row: 1}: {Disabled: true},
	}
	return p, pg, st
}

func TestGoldens(t *testing.T) {
	skipGoldensOffMac(t)
	dir := t.TempDir()
	r := New(dir)
	p, pg, st := sample(t, dir)
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		name := "grid-" + itoa(size.X) + "x" + itoa(size.Y) + ".png"
		golden(t, name, r.Grid(p, pg, size, st))
	}
}

// A cell drawn alone is the same pixels as that cell in the whole grid, which is what lets the
// deck repaint one cell and send only its rectangle.
func TestCellMatchesGrid(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	p, pg, st := sample(t, dir)
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		full := r.Grid(p, pg, size, st)
		l := NewLayout(p.Grid, size)
		for key, b := range pg.Buttons {
			c, _ := deck.ParseCell(key)
			got := r.Cell(p, b, c, size, st[c])
			want := full.SubImage(l.Rect(c)).(*image.RGBA)
			if got.Bounds().Size() != want.Bounds().Size() {
				t.Fatalf("%v %v: size %v want %v", size, key, got.Bounds().Size(), want.Bounds().Size())
			}
			// The rasterizer rounds an antialiased edge by a level or so differently when the
			// shape sits at another offset, which no eye can see; more than that is a bug.
			for y := 0; y < got.Bounds().Dy(); y++ {
				for x := 0; x < got.Bounds().Dx(); x++ {
					g, w := got.RGBAAt(x, y), want.RGBAAt(want.Bounds().Min.X+x, want.Bounds().Min.Y+y)
					if absDiff(g.R, w.R) > 2 || absDiff(g.G, w.G) > 2 || absDiff(g.B, w.B) > 2 {
						t.Fatalf("%v: cell %v differs from the grid at %d,%d: %v, %v", size, key, x, y, g, w)
					}
				}
			}
		}
	}
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestCellOfNothingIsBackground(t *testing.T) {
	r := New(t.TempDir())
	p := &deck.Profile{Grid: deck.DefaultGrid, Theme: deck.DefaultTheme}
	img := r.Cell(p, nil, deck.Cell{}, image.Pt(960, 480), CellState{})
	want, _ := ParseColour(deck.DefaultTheme.BG)
	for i := 0; i < len(img.Pix); i += 4 {
		if (color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}) != want {
			t.Fatalf("pixel %d is not the background", i/4)
		}
	}
}

// Each state must look different from a plain button, or the Show gives no feedback.
func TestStatesDiffer(t *testing.T) {
	r := New(t.TempDir())
	p := &deck.Profile{Grid: deck.DefaultGrid, Theme: deck.DefaultTheme}
	b := &deck.Button{Label: "Mute", Icon: "lucide:mic-off"}
	size := image.Pt(960, 480)
	plain := r.Cell(p, b, deck.Cell{}, size, CellState{})
	for name, st := range map[string]CellState{
		"pressed":  {Pressed: true},
		"on":       {On: true},
		"ok":       {Flash: FlashOK},
		"err":      {Flash: FlashErr},
		"disabled": {Disabled: true},
	} {
		if bytes.Equal(plain.Pix, r.Cell(p, b, deck.Cell{}, size, st).Pix) {
			t.Errorf("%s looks like a plain button", name)
		}
	}
}

func TestIconsNeverEscapeTheIconsDir(t *testing.T) {
	for _, name := range []string{"../x.png", "/etc/x.png", `a\b.png`, "sub/x.png", "x.txt", ""} {
		if isImageName(name) {
			t.Errorf("%q was taken for an icon file", name)
		}
	}
	if !isImageName("mine.PNG") || !isImageName("a.jpeg") {
		t.Error("a plain image name was refused")
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(appendInt(nil, n))) }

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}

// golden compares img with testdata/name, or writes it when -update is given.
func golden(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if want.Bounds() != img.Bounds() {
		t.Fatalf("%s: size %v, golden %v", name, img.Bounds(), want.Bounds())
	}
	bad := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			wr, wg, wb, _ := want.At(x, y).RGBA()
			g := img.RGBAAt(x, y)
			if uint32(g.R)*257 != wr || uint32(g.G)*257 != wg || uint32(g.B)*257 != wb {
				bad++
			}
		}
	}
	if bad > 0 {
		out := filepath.Join(t.TempDir(), name)
		var buf bytes.Buffer
		png.Encode(&buf, img)
		os.WriteFile(out, buf.Bytes(), 0o644)
		t.Errorf("%s: %d pixels differ from the golden; got is at %s", name, bad, out)
	}
}

// A live tile's value is drawn big in place of the icon, over the label when there is one.
func TestTileGolden(t *testing.T) {
	skipGoldensOffMac(t)
	r := New(t.TempDir())
	p := &deck.Profile{Grid: deck.DefaultGrid, Theme: deck.DefaultTheme}
	pg := &deck.Page{Buttons: map[string]*deck.Button{
		"0,0": {Label: "Time", Icon: "lucide:clock"},
		"1,0": {Label: "CPU"},
		"2,0": {},
		"3,0": {Label: "Living room", Icon: "lucide:sun"},
		"4,0": {Label: "Build"},
	}}
	st := State{
		{Col: 0, Row: 0}: {Text: "14:05"},
		{Col: 1, Row: 0}: {Text: "37%"},
		{Col: 2, Row: 0}: {Text: "Mon 2 Jan"},
		{Col: 3, Row: 0}: {Text: "21.5 °C"},
		{Col: 4, Row: 0}: {Text: "passing, 12 min ago on main", On: true},
	}
	golden(t, "tiles-960x480.png", r.Grid(p, pg, image.Pt(960, 480), st))
	// The text replaces the icon: the same button without it looks different.
	with := r.Cell(p, pg.Buttons["0,0"], deck.Cell{}, image.Pt(960, 480), CellState{Text: "14:05"})
	without := r.Cell(p, pg.Buttons["0,0"], deck.Cell{}, image.Pt(960, 480), CellState{})
	if bytes.Equal(with.Pix, without.Pix) {
		t.Error("Text changed nothing")
	}
}

// The goldens are rasterised text, and Windows and Linux draw the same glyphs a few pixels
// differently.
func skipGoldensOffMac(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("goldens are made on macOS; text rasterises differently elsewhere")
	}
}

func TestIconOn(t *testing.T) {
	r := New(t.TempDir())
	p := &deck.Profile{Grid: deck.DefaultGrid, Theme: deck.DefaultTheme}
	size := image.Pt(160, 160)
	c := deck.Cell{}
	draw := func(b *deck.Button, on bool) []byte {
		return r.Cell(p, b, c, size, CellState{On: on}).Pix
	}
	two := &deck.Button{Icon: "lucide:mic", IconOn: "lucide:mic-off"}
	one := &deck.Button{Icon: "lucide:mic"}
	if bytes.Equal(draw(two, false), draw(two, true)) {
		t.Error("a button with an on icon looks the same on and off")
	}
	// Off, it is the plain icon; on without an on icon, the ring is all that changes.
	if !bytes.Equal(draw(two, false), draw(one, false)) {
		t.Error("off, a button with an on icon differs from one without")
	}
}
