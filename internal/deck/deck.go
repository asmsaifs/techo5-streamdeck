// Package deck is the model: profiles, pages, buttons and actions, as config.json holds them.
package deck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is the config.json format this build writes. Older files are migrated on load (store).
const Version = 1

// HomePage is the page every profile starts on.
const HomePage = "home"

// Config is all of config.json.
type Config struct {
	Version  int                     `json:"version"`
	Server   Server                  `json:"server"`
	Devices  map[string]Device       `json:"devices,omitempty"` // by the name a Show sends in its hello
	Profiles map[string]*Profile     `json:"profiles"`
	Hotkeys  map[string]HotkeyTarget `json:"hotkeys,omitempty"` // by accelerator, "CmdOrCtrl+Alt+1"
	// Integrations is where the ha.service and obs actions connect. The tokens and passwords are
	// not here: they are in the OS keychain (internal/secrets).
	Integrations *Integrations `json:"integrations,omitempty"`
}

// Integrations are the addresses of the services the actions talk to.
type Integrations struct {
	HomeAssistant string `json:"homeassistant,omitempty"` // base URL, "http://homeassistant.local:8123"
	OBS           string `json:"obs,omitempty"`           // "host:port" of OBS's WebSocket server, usually localhost:4455
}

// Server is where the deck listens and the key a Show must have.
type Server struct {
	Listen string `json:"listen"`
	Key    string `json:"key"`
}

// Device is what a known Show gets. A Show not listed gets DefaultProfile.
type Device struct {
	Profile string `json:"profile"`
}

// DefaultProfile is the profile for a Show the config does not name.
const DefaultProfile = "default"

// HotkeyTarget is the button a global hotkey presses.
type HotkeyTarget struct {
	Profile string `json:"profile"`
	Page    string `json:"page"`
	Button  string `json:"button"` // a cell key, "col,row"
}

// Profile is one deck: its grid, its look and its pages.
type Profile struct {
	Grid  Grid             `json:"grid"`
	Theme Theme            `json:"theme"`
	Pages map[string]*Page `json:"pages"`
}

// Grid is how the screen is divided: Cols by Rows cells, Gap pixels apart, corners rounded by
// Radius. Sizes are for a 960 by 480 screen and scale with the Show's.
type Grid struct {
	Cols   int `json:"cols"`
	Rows   int `json:"rows"`
	Gap    int `json:"gap"`
	Radius int `json:"radius"`
}

// Theme is the deck's colours, as #rgb or #rrggbb.
type Theme struct {
	BG     string `json:"bg"`
	Button string `json:"button"`
	Text   string `json:"text"`
	Accent string `json:"accent"`
}

// Page is a screenful of buttons, by cell key ("col,row"). Empty cells are left out.
type Page struct {
	Buttons map[string]*Button `json:"buttons"`
}

// Button is one cell.
type Button struct {
	Label  string  `json:"label,omitempty"`
	Icon   string  `json:"icon,omitempty"` // "lucide:youtube", an emoji, or a file in icons/
	Action *Action `json:"action,omitempty"`
}

// Action is what a button does: its type, and the rest of its JSON as it was written, for the
// action's own code (internal/actions) to read. The model does not know every action's
// parameters, and does not need to in order to keep them.
type Action struct {
	Type string
	Raw  json.RawMessage // the whole object, type included
}

func (a *Action) UnmarshalJSON(b []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return errors.New("an action is an object with a type")
	}
	a.Type = head.Type
	a.Raw = append(json.RawMessage(nil), b...)
	return nil
}

func (a Action) MarshalJSON() ([]byte, error) {
	if len(a.Raw) == 0 {
		return json.Marshal(map[string]string{"type": a.Type})
	}
	// Compacted, so the file's indentation is the encoder's and not whatever the action had.
	var b bytes.Buffer
	if err := json.Compact(&b, a.Raw); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Params decodes the action's parameters into v.
func (a *Action) Params(v any) error { return json.Unmarshal(a.Raw, v) }

// Cell is a grid position, 0-based from the top left.
type Cell struct{ Col, Row int }

func (c Cell) String() string { return strconv.Itoa(c.Col) + "," + strconv.Itoa(c.Row) }

// ParseCell reads a cell key, "col,row".
func ParseCell(key string) (Cell, error) {
	cs, rs, ok := strings.Cut(key, ",")
	col, err1 := strconv.Atoi(cs)
	row, err2 := strconv.Atoi(rs)
	if !ok || err1 != nil || err2 != nil || col < 0 || row < 0 {
		return Cell{}, fmt.Errorf("%q is not a cell: want col,row like 0,0", key)
	}
	return Cell{col, row}, nil
}

// In reports whether c is on g.
func (c Cell) In(g Grid) bool { return c.Col < g.Cols && c.Row < g.Rows }

// ProfileFor is the name of the profile the Show called name gets.
func (c *Config) ProfileFor(name string) string {
	if d, ok := c.Devices[name]; ok && d.Profile != "" {
		return d.Profile
	}
	return DefaultProfile
}
