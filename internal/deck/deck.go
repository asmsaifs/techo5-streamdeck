// Package deck is the model: profiles, pages, buttons and actions, as config.json holds them.
package deck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/asmsaifs/techo5-streamdeck/internal/foreground"
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
	// AutoSwitch gives a Show another profile while an application is in front on this computer.
	// The first rule that matches wins.
	AutoSwitch []AutoRule `json:"autoSwitch,omitempty"`
	// Speaker makes the Shows this computer's speaker (docs/speaker.md).
	Speaker *Speaker `json:"speaker,omitempty"`
}

// Speaker plays this computer's sound on the chosen Shows over Sendspin.
type Speaker struct {
	Enabled bool     `json:"enabled"`
	Shows   []string `json:"shows,omitempty"`   // by the names they advertise
	LeadMs  int      `json:"lead_ms,omitempty"` // how far ahead the sound is stamped; 200 if 0
	IdleS   int      `json:"idle_s,omitempty"`  // silence before the Shows are let go; 5 if 0
}

// Speaker limits, as the editor's sliders have them.
const (
	MinLeadMs = 100
	MaxLeadMs = 1000
	MaxIdleS  = 600
)

// AutoRule switches to Profile while the application App is in front.
type AutoRule struct {
	App     string `json:"app"`              // its name or id, in any letter case: "Safari", "com.apple.Safari", "code.exe"
	Profile string `json:"profile"`          // the profile to show
	Device  string `json:"device,omitempty"` // only this Show; empty means every Show
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
	Label string `json:"label,omitempty"`
	Icon  string `json:"icon,omitempty"` // "lucide:youtube", an emoji, or a file in icons/
	// IconOn is drawn instead of Icon while the button is on: a toggle that is on, a state tile
	// that says yes, or a mute button while the sound (or microphone) is muted.
	IconOn string  `json:"iconOn,omitempty"`
	Action *Action `json:"action,omitempty"`
	// Tile makes the button live: its big text is a value that is read again every few seconds.
	Tile *Tile `json:"tile,omitempty"`
}

// Tile types.
const (
	TileClock  = "clock"    // the time; Format picks how
	TileCPU    = "cpu"      // processor load, in percent
	TileRAM    = "ram"      // memory in use, in percent
	TileHA     = "ha_state" // a Home Assistant entity's state, Entity
	TileScript = "script"   // the first line a command prints
	TileState  = "state"    // no text: a command says whether the button's toggle is really on
	TileMute   = "mute"     // no text: whether the sound (Command "volume.mute") or the microphone ("mic.mute") is muted
)

// ImplicitTile is the tile a button gets without having one: a mute button with an icon for the
// muted state needs to know whether it is muted. Nil for any other button.
func (b *Button) ImplicitTile() *Tile {
	if b.Tile != nil || b.IconOn == "" || b.Action == nil {
		return nil
	}
	if b.Action.Type == "volume.mute" || b.Action.Type == "mic.mute" {
		return &Tile{Type: TileMute, Command: b.Action.Type}
	}
	return nil
}

// Tile is what a live button shows. Only the parameters of its Type are used.
type Tile struct {
	Type    string   `json:"type"`
	Every   float64  `json:"every,omitempty"`   // seconds between readings; each type has a default
	Format  string   `json:"format,omitempty"`  // clock: 24h (default), 12h, 24h-seconds, date
	Entity  string   `json:"entity,omitempty"`  // ha_state: "sensor.living_room_temperature"
	Command string   `json:"command,omitempty"` // script and state
	Args    []string `json:"args,omitempty"`
	Shell   bool     `json:"shell,omitempty"` // Command is a line for the shell
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

// EffectiveProfile is the profile the Show called device shows when the application with the given
// names (foreground.App.Names) is in front: the profile of the first rule that matches, or the
// Show's own. No names, or no match, is the Show's own.
func (c *Config) EffectiveProfile(device string, names ...string) string {
	for _, r := range c.AutoSwitch {
		if r.Device != "" && r.Device != device {
			continue
		}
		if _, ok := c.Profiles[r.Profile]; !ok {
			continue
		}
		if foreground.Match(r.App, names...) {
			return r.Profile
		}
	}
	return c.ProfileFor(device)
}
