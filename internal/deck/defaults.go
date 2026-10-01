package deck

// The defaults fill what a hand-written config leaves out, so a profile can be as short as its
// pages.
var (
	DefaultGrid  = Grid{Cols: 5, Rows: 3, Gap: 8, Radius: 14}
	DefaultTheme = Theme{BG: "#101114", Button: "#1d1f24", Text: "#ffffff", Accent: "#4f8cff"}
)

// DefaultListen is all interfaces on the dashcast port, which is what a Show is set up for.
const DefaultListen = "0.0.0.0:9555"

// New is the config a first run starts with: one empty profile, listening with key.
func New(key string) *Config {
	c := &Config{
		Version:  Version,
		Server:   Server{Listen: DefaultListen, Key: key},
		Profiles: map[string]*Profile{DefaultProfile: {Pages: map[string]*Page{HomePage: {}}}},
	}
	c.Fill()
	return c
}

// Fill puts the defaults into whatever is unset. It never changes what is set.
func (c *Config) Fill() {
	if c.Server.Listen == "" {
		c.Server.Listen = DefaultListen
	}
	for _, p := range c.Profiles {
		if p == nil {
			continue
		}
		// Gap and radius may be 0 on purpose, so they are only filled when no grid was given at all.
		if p.Grid == (Grid{}) {
			p.Grid = DefaultGrid
		}
		p.Grid.Cols = or(p.Grid.Cols, DefaultGrid.Cols)
		p.Grid.Rows = or(p.Grid.Rows, DefaultGrid.Rows)
		t := &p.Theme
		t.BG = orS(t.BG, DefaultTheme.BG)
		t.Button = orS(t.Button, DefaultTheme.Button)
		t.Text = orS(t.Text, DefaultTheme.Text)
		t.Accent = orS(t.Accent, DefaultTheme.Accent)
		if p.Pages == nil {
			p.Pages = map[string]*Page{}
		}
		for _, pg := range p.Pages {
			if pg != nil && pg.Buttons == nil {
				pg.Buttons = map[string]*Button{}
			}
		}
	}
}

func or(n, d int) int {
	if n == 0 {
		return d
	}
	return n
}

func orS(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
