package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

func TestTheExampleLoads(t *testing.T) {
	c, err := Load("testdata/example.json")
	if err != nil {
		t.Fatal(err)
	}
	b := c.Profiles["default"].Pages["home"].Buttons["0,0"]
	if b.Label != "YouTube" || b.Action.Type != "stream.web" {
		t.Errorf("0,0 is %+v", b)
	}
	if c.ProfileFor("Kitchen Show") != "default" {
		t.Error("the kitchen's profile")
	}
	// What is saved reads back the same.
	first, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Encode(again)
	if !bytes.Equal(first, second) {
		t.Errorf("a save and a load changed it:\n%s\n%s", first, second)
	}
}

func TestOpenMakesAFirstConfigAndKeepsIt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := s.Config().Server.Key
	if wire.CheckKey(key) != nil {
		t.Errorf("first key %q", key)
	}
	if st, err := os.Stat(filepath.Join(dir, IconsDir)); err != nil || !st.IsDir() {
		t.Errorf("icons folder: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(s.Path())
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("config mode %v %v: it holds the key", st.Mode().Perm(), err)
		}
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Config().Server.Key != key {
		t.Error("a second open made a new key")
	}
}

// A broken config is the user's: Open reports it, with where, and leaves the file alone.
func TestOpenReportsABrokenConfig(t *testing.T) {
	dir := t.TempDir()
	broken := "{\n  \"version\": 1,\n  \"server\": {,\n}\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "line 3, column") {
		t.Errorf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, FileName)); string(b) != broken {
		t.Error("the broken config was replaced")
	}
}

func TestParse(t *testing.T) {
	const key = `"key":"a-long-enough-test-key"`
	tests := []struct {
		name, json, wantErr string
	}{
		{"current", `{"version":1,"server":{` + key + `},"profiles":{"default":{"pages":{"home":{}}}}}`, ""},
		{"no version is version 0, the same shape", `{"server":{` + key + `},"profiles":{"default":{"pages":{"home":{}}}}}`, ""},
		{"from the future", `{"version":7}`, "newer"},
		{"a wrong type, located", "{\"version\":1,\n\"server\":{\"listen\":5}}", "line 2, column"},
		{"a misspelt field", `{"version":1,"server":{` + key + `},"profile":{}}`, `unknown field "profile"`},
		{"invalid", `{"version":1,"server":{` + key + `},"profiles":{"default":{"pages":{}}}}`, `no "home" page`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse([]byte(tt.json))
			if tt.wantErr == "" {
				if err != nil || c.Version != deck.Version {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSaveRefusesAnInvalidConfig(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.Path())
	bad := deck.New("short")
	if err := s.Save(bad); err == nil {
		t.Fatal("an invalid config was saved")
	}
	if after, _ := os.ReadFile(s.Path()); !bytes.Equal(before, after) {
		t.Error("the file changed")
	}
}

// An edit reaches the watcher; a broken edit is reported and the last good config stays; the
// store's own save is not reported as an edit.
func TestWatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	type change struct {
		c   *deck.Config
		err error
	}
	got := make(chan change, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Watch(ctx, func(c *deck.Config, err error) { got <- change{c, err} }); err != nil {
		t.Fatal(err)
	}
	next := func() change {
		select {
		case ch := <-got:
			return ch
		case <-time.After(5 * time.Second):
			t.Fatal("no change reported")
		}
		return change{}
	}
	quiet := func() {
		select {
		case ch := <-got:
			t.Fatalf("an unexpected change: %+v", ch)
		case <-time.After(3 * settle):
		}
	}

	edited := deck.New(s.Config().Server.Key)
	edited.Profiles["default"].Grid.Cols = 4
	b, _ := Encode(edited)
	if err := os.WriteFile(s.Path(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if ch := next(); ch.err != nil || ch.c.Profiles["default"].Grid.Cols != 4 {
		t.Fatalf("edit: %+v", ch)
	}

	if err := os.WriteFile(s.Path(), []byte("{ oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ch := next(); ch.err == nil {
		t.Fatal("a broken edit was not reported")
	}
	if s.Config().Profiles["default"].Grid.Cols != 4 {
		t.Error("the last good config was dropped")
	}

	edited.Profiles["default"].Grid.Cols = 3
	if err := s.Save(edited); err != nil {
		t.Fatal(err)
	}
	quiet()
}
