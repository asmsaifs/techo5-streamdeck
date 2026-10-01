package deck

import (
	"encoding/json"
	"strings"
	"testing"
)

const key = "a-long-enough-test-key"

func TestCells(t *testing.T) {
	tests := []struct {
		in      string
		want    Cell
		wantErr bool
	}{
		{"0,0", Cell{0, 0}, false},
		{"4,2", Cell{4, 2}, false},
		{"12,7", Cell{12, 7}, false},
		{"-1,0", Cell{}, true},
		{"1", Cell{}, true},
		{"a,b", Cell{}, true},
		{"1,2,3", Cell{}, true},
		{"", Cell{}, true},
	}
	for _, tt := range tests {
		got, err := ParseCell(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseCell(%q) = %v, %v", tt.in, got, err)
		}
		if err == nil && got.String() != tt.in {
			t.Errorf("%v.String() = %q", got, got.String())
		}
	}
	g := Grid{Cols: 5, Rows: 3}
	if !(Cell{4, 2}).In(g) || (Cell{5, 0}).In(g) || (Cell{0, 3}).In(g) {
		t.Error("In is wrong")
	}
}

// An action keeps every parameter it was written with, including ones the model knows nothing of,
// through a load and a save.
func TestActionsKeepTheirParameters(t *testing.T) {
	in := `{"type":"stream.web","url":"https://youtube.com","sound":"show","extra":{"a":[1,2]}}`
	var a Action
	if err := json.Unmarshal([]byte(in), &a); err != nil {
		t.Fatal(err)
	}
	if a.Type != "stream.web" {
		t.Errorf("type %q", a.Type)
	}
	var p struct {
		URL string `json:"url"`
	}
	if err := a.Params(&p); err != nil || p.URL != "https://youtube.com" {
		t.Errorf("params %+v %v", p, err)
	}
	out, err := json.Marshal(a)
	if err != nil || string(out) != in {
		t.Errorf("marshal = %s %v", out, err)
	}
	if out, _ := json.Marshal(Action{Type: "back"}); string(out) != `{"type":"back"}` {
		t.Errorf("a bare action = %s", out)
	}
	if json.Unmarshal([]byte(`"page"`), &a) == nil {
		t.Error("a string was taken for an action")
	}
}

func TestNewIsValid(t *testing.T) {
	c := New(key)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Profiles[DefaultProfile].Grid != DefaultGrid {
		t.Errorf("grid %+v", c.Profiles[DefaultProfile].Grid)
	}
}

func TestFill(t *testing.T) {
	c, err := Decode([]byte(`{"version":1,"server":{"key":"` + key + `"},"profiles":{
		"default":{"pages":{"home":{}}},
		"flat":{"grid":{"cols":4,"rows":2},"theme":{"accent":"#f00"},"pages":{"home":{"buttons":{}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	c.Fill()
	if c.Server.Listen != DefaultListen {
		t.Errorf("listen %q", c.Server.Listen)
	}
	if d := c.Profiles["default"]; d.Grid != DefaultGrid || d.Theme != DefaultTheme || d.Pages["home"].Buttons == nil {
		t.Errorf("default profile %+v", d)
	}
	// A grid given in part keeps its 0 gap and radius: they may be meant.
	f := c.Profiles["flat"]
	if f.Grid != (Grid{Cols: 4, Rows: 2}) {
		t.Errorf("flat grid %+v", f.Grid)
	}
	if f.Theme.Accent != "#f00" || f.Theme.BG != DefaultTheme.BG {
		t.Errorf("flat theme %+v", f.Theme)
	}
	if err := c.Validate(); err != nil {
		t.Error(err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		json string // the profiles object, or a whole config when it starts with {"version"
		want []string
	}{
		{"fine", `{"default":{"pages":{"home":{"buttons":{
			"0,0":{"label":"Apps","action":{"type":"page","page":"apps"}},
			"1,0":{"action":{"type":"toggle","on":{"type":"mic.mute"},"off":{"type":"mic.mute"}}}}},
			"apps":{"buttons":{"0,0":{"action":{"type":"back"}}}}}}}`, nil},
		{"a button off the grid", `{"default":{"pages":{"home":{"buttons":{"9,0":{"label":"x"}}}}}}`,
			[]string{"profiles.default.pages.home.buttons.9,0: outside a 5×3 grid"}},
		{"a bad cell key", `{"default":{"pages":{"home":{"buttons":{"top":{}}}}}}`,
			[]string{`profiles.default.pages.home.buttons.top: "top" is not a cell: want col,row like 0,0`}},
		{"no default profile", `{"other":{"pages":{"home":{}}}}`,
			[]string{`profiles: there is no "default" profile, which a Show the config does not name gets`}},
		{"no home page", `{"default":{"pages":{"apps":{}}}}`,
			[]string{`profiles.default.pages: there is no "home" page, which the deck opens on`}},
		{"bad grid and colour", `{"default":{"grid":{"cols":20,"rows":3},"theme":{"bg":"black"},"pages":{"home":{}}}}`,
			[]string{"profiles.default.grid.cols: is 20; it must be 1 to 12",
				`profiles.default.theme.bg: "black" is not a colour like #4f8cff`}},
		{"actions", `{"default":{"pages":{"home":{"buttons":{
			"0,0":{"action":{"page":"x"}},
			"1,0":{"action":{"type":"page","page":"nowhere"}},
			"2,0":{"action":{"type":"toggle","on":{"type":"page"}}}}}}}}`,
			[]string{"profiles.default.pages.home.buttons.0,0.action.type: is missing",
				`profiles.default.pages.home.buttons.1,0.action.page: there is no page "nowhere" in this profile`,
				"profiles.default.pages.home.buttons.2,0.action.off: is missing",
				"profiles.default.pages.home.buttons.2,0.action.on.page: is missing"}},
		{"server, devices and hotkeys", `{"version":2,"server":{"listen":"9555","key":"short"},
			"devices":{"Kitchen":{"profile":"nope"}},
			"profiles":{"default":{"pages":{"home":{"buttons":{"0,0":{}}}}}},
			"hotkeys":{"Alt+1":{"profile":"default","page":"home","button":"1,0"},
			           "Alt+2":{"profile":"default","page":"home","button":"0,0"},
			           "Alt+3":{"profile":"default","page":"gone","button":"0,0"}}}`,
			[]string{`devices.Kitchen.profile: there is no profile "nope"`,
				`hotkeys.Alt+1.button: page "home" has no button at "1,0"`,
				`hotkeys.Alt+3.page: profile "default" has no page "gone"`,
				`server.key: the key must be at least 16 characters`,
				`server.listen: "9555" is not host:port`,
				"version: is 2; this version reads 1"}},
		{"integrations", `{"version":1,"server":{"listen":"0.0.0.0:9555","key":"0123456789abcdef"},
			"integrations":{"homeassistant":"ftp://ha","obs":"localhost"},
			"profiles":{"default":{"pages":{"home":{}}}}}`,
			[]string{`integrations.homeassistant: "ftp://ha" is not an http:// or https:// address`,
				`integrations.obs: "localhost" is not host:port, like localhost:4455`}},
		{"websites", `{"default":{"pages":{"home":{"buttons":{
			"0,0":{"action":{"type":"stream.web","url":"https://example.com"}},
			"1,0":{"action":{"type":"stream.web"}},
			"2,0":{"action":{"type":"stream.web","url":"example.com"}},
			"3,0":{"action":{"type":"stream.web","url":"file:///etc/passwd"}}}}}}}`,
			[]string{"profiles.default.pages.home.buttons.1,0.action.url: is missing",
				`profiles.default.pages.home.buttons.2,0.action.url: "example.com" is not an http:// or https:// address`,
				`profiles.default.pages.home.buttons.3,0.action.url: "file:///etc/passwd" is not an http:// or https:// address`}},
		{"app windows", `{"default":{"pages":{"home":{"buttons":{
			"0,0":{"action":{"type":"stream.app","app":"Spotify"}},
			"1,0":{"action":{"type":"stream.app"}},
			"2,0":{"action":{"type":"stream.app","title":"("}},
			"3,0":{"action":{"type":"stream.app","app":"x","sound":"desktop"}}}}}}}`,
			[]string{"profiles.default.pages.home.buttons.1,0.action.app: name an app or a window title",
				`profiles.default.pages.home.buttons.2,0.action.title: "(" is not a regular expression`,
				`profiles.default.pages.home.buttons.3,0.action.sound: "desktop" is not show or off`}},
		{"tiles", `{"default":{"pages":{"home":{"buttons":{
			"0,0":{"tile":{"type":"clock","format":"sundial"}},
			"1,0":{"tile":{"type":"ha_state","entity":"Sensor Kitchen"}},
			"2,0":{"tile":{"type":"script"}},
			"3,0":{"tile":{"type":"weather"}},
			"4,0":{"tile":{"type":"cpu","every":0.2}},
			"0,1":{"tile":{"type":"ram","every":5}},
			"1,1":{"tile":{"type":"state","command":"x"}}}}}}}`,
			[]string{`profiles.default.pages.home.buttons.0,0.tile.format: "sundial" is not 24h, 12h, 24h-seconds or date`,
				`profiles.default.pages.home.buttons.1,0.tile.entity: "Sensor Kitchen" is not an entity like sensor.kitchen_temperature`,
				"profiles.default.pages.home.buttons.2,0.tile.command: is missing",
				`profiles.default.pages.home.buttons.3,0.tile.type: "weather" is not clock, cpu, ram, ha_state, script or state`,
				"profiles.default.pages.home.buttons.4,0.tile.every: is 0.2; it must be 1 to 3600 seconds"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.json
			if !strings.HasPrefix(doc, `{"version"`) {
				doc = `{"version":1,"server":{"key":"` + key + `"},"profiles":` + doc + `}`
			}
			c, err := Decode([]byte(doc))
			if err != nil {
				t.Fatal(err)
			}
			c.Fill()
			err = c.Validate()
			var got []string
			if ps, ok := err.(Problems); ok {
				for _, p := range ps {
					got = append(got, p.String())
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

func TestDecodeRefusesUnknownFieldsAndTrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{"version":1,"profiles":{"default":{"pages":{"home":{"buttons":{"0,0":{"lable":"x"}}}}}}}`)); err == nil || !strings.Contains(err.Error(), "lable") {
		t.Errorf("a misspelt field: %v", err)
	}
	if _, err := Decode([]byte(`{"version":1} {}`)); err == nil {
		t.Error("trailing data was accepted")
	}
}

func TestProfileFor(t *testing.T) {
	c := &Config{Devices: map[string]Device{"Kitchen": {Profile: "kitchen"}}}
	if c.ProfileFor("Kitchen") != "kitchen" || c.ProfileFor("Bedroom") != DefaultProfile {
		t.Error("ProfileFor is wrong")
	}
}
