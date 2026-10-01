package deck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Problem is one thing wrong with a config, at a path into it written the way the JSON nests:
// "profiles.default.pages.home.buttons.9,0".
type Problem struct {
	Path string
	Msg  string
}

func (p Problem) String() string { return p.Path + ": " + p.Msg }

// Problems is everything wrong with a config, so a hand edit with three mistakes shows all three.
type Problems []Problem

func (ps Problems) Error() string {
	lines := make([]string, len(ps))
	for i, p := range ps {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}

// Grid limits: a cell on a 960 by 480 screen is still big enough to hit at 12 by 8.
const (
	maxCols = 12
	maxRows = 8
)

var colour = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Validate returns everything wrong with c, in path order, or nil. Defaults are expected to be
// filled first (Fill).
func (c *Config) Validate() error {
	var ps Problems
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{path, fmt.Sprintf(format, args...)})
	}

	if c.Version != Version {
		add("version", "is %d; this version reads %d", c.Version, Version)
	}
	if _, port, err := net.SplitHostPort(c.Server.Listen); err != nil {
		add("server.listen", "%q is not host:port", c.Server.Listen)
	} else if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
		add("server.listen", "%q has no usable port", c.Server.Listen)
	}
	if err := wire.CheckKey(c.Server.Key); err != nil {
		add("server.key", "%v", err)
	}

	if in := c.Integrations; in != nil {
		if u := strings.TrimSpace(in.HomeAssistant); u != "" {
			if pu, err := url.Parse(u); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
				add("integrations.homeassistant", "%q is not an http:// or https:// address", u)
			}
		}
		if a := strings.TrimSpace(in.OBS); a != "" {
			if _, port, err := net.SplitHostPort(a); err != nil || port == "" {
				add("integrations.obs", "%q is not host:port, like localhost:4455", a)
			}
		}
	}

	if len(c.Profiles) == 0 {
		add("profiles", "there are none: a deck needs at least the %q profile", DefaultProfile)
	} else if _, ok := c.Profiles[DefaultProfile]; !ok {
		add("profiles", "there is no %q profile, which a Show the config does not name gets", DefaultProfile)
	}
	for name, d := range c.Devices {
		if _, ok := c.Profiles[d.Profile]; !ok {
			add("devices."+name+".profile", "there is no profile %q", d.Profile)
		}
	}
	for name, p := range c.Profiles {
		c.validateProfile("profiles."+name, p, add)
	}
	for combo, t := range c.Hotkeys {
		path := "hotkeys." + combo
		p, ok := c.Profiles[t.Profile]
		if !ok {
			add(path+".profile", "there is no profile %q", t.Profile)
			continue
		}
		pg, ok := p.Pages[t.Page]
		if !ok {
			add(path+".page", "profile %q has no page %q", t.Profile, t.Page)
			continue
		}
		if _, ok := pg.Buttons[t.Button]; !ok {
			add(path+".button", "page %q has no button at %q", t.Page, t.Button)
		}
	}

	if len(ps) == 0 {
		return nil
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Path < ps[j].Path })
	return ps
}

func (c *Config) validateProfile(path string, p *Profile, add func(path, format string, args ...any)) {
	if p == nil {
		add(path, "is empty")
		return
	}
	g := p.Grid
	if g.Cols < 1 || g.Cols > maxCols {
		add(path+".grid.cols", "is %d; it must be 1 to %d", g.Cols, maxCols)
	}
	if g.Rows < 1 || g.Rows > maxRows {
		add(path+".grid.rows", "is %d; it must be 1 to %d", g.Rows, maxRows)
	}
	if g.Gap < 0 || g.Gap > 64 {
		add(path+".grid.gap", "is %d; it must be 0 to 64", g.Gap)
	}
	if g.Radius < 0 || g.Radius > 128 {
		add(path+".grid.radius", "is %d; it must be 0 to 128", g.Radius)
	}
	for field, v := range map[string]string{"bg": p.Theme.BG, "button": p.Theme.Button, "text": p.Theme.Text, "accent": p.Theme.Accent} {
		if !colour.MatchString(v) {
			add(path+".theme."+field, "%q is not a colour like #4f8cff", v)
		}
	}
	if _, ok := p.Pages[HomePage]; !ok {
		add(path+".pages", "there is no %q page, which the deck opens on", HomePage)
	}
	for pname, pg := range p.Pages {
		ppath := path + ".pages." + pname
		if pg == nil {
			add(ppath, "is empty")
			continue
		}
		for key, b := range pg.Buttons {
			bpath := ppath + ".buttons." + key
			cell, err := ParseCell(key)
			if err != nil {
				add(bpath, "%v", err)
			} else if g.Cols > 0 && g.Rows > 0 && !cell.In(g) {
				add(bpath, "outside a %d×%d grid", g.Cols, g.Rows)
			}
			if b == nil {
				add(bpath, "is empty")
				continue
			}
			if b.Action != nil {
				validateAction(bpath+".action", b.Action, p, add)
			}
		}
	}
}

// validateAction checks what the model can know about an action: that it has a type, that a page
// it opens exists, and the same for the two halves of a toggle. Each action's own parameters are
// its code's to check (internal/actions).
func validateAction(path string, a *Action, p *Profile, add func(path, format string, args ...any)) {
	if a.Type == "" {
		add(path+".type", "is missing")
		return
	}
	switch a.Type {
	case "page":
		var v struct {
			Page string `json:"page"`
		}
		if a.Params(&v) != nil || v.Page == "" {
			add(path+".page", "is missing")
		} else if _, ok := p.Pages[v.Page]; !ok {
			add(path+".page", "there is no page %q in this profile", v.Page)
		}
	case "toggle":
		var v struct {
			On  *Action `json:"on"`
			Off *Action `json:"off"`
		}
		if err := a.Params(&v); err != nil {
			add(path, "%v", err)
			return
		}
		for half, sub := range map[string]*Action{"on": v.On, "off": v.Off} {
			if sub == nil {
				add(path+"."+half, "is missing")
			} else {
				validateAction(path+"."+half, sub, p, add)
			}
		}
	}
}

// Decode reads a config from JSON, refusing fields it does not know: in a hand-edited file a
// misspelt field is a mistake, not something to ignore. It does not fill defaults or validate.
func Decode(b []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("something follows the config's closing }")
	}
	return &c, nil
}
