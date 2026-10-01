package main

import (
	"strings"
	"testing"

	"github.com/asmsaifs/techo5-streamdeck/internal/core"
)

func newEditor(t *testing.T) *Editor {
	t.Helper()
	c, err := core.New(core.Options{Dir: t.TempDir(), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &Editor{core: c}
}

func TestEditorSaveAndPreview(t *testing.T) {
	e := newEditor(t)
	cfg, err := e.Config()
	if err != nil {
		t.Fatal(err)
	}

	// A good edit is saved and read back.
	edited := strings.Replace(cfg, `"cols": 5`, `"cols": 6`, 1)
	if edited == cfg {
		t.Fatal("test config has no cols: 5 to edit")
	}
	if err := e.Save(edited); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Config(); !strings.Contains(got, `"cols": 6`) {
		t.Fatal("saved edit not read back")
	}

	// A bad one is refused, names its path, and leaves the saved config alone.
	bad := strings.Replace(edited, `"buttons": {}`, `"buttons": {"9,0": {"label": "x"}}`, 1)
	if bad == edited {
		t.Fatal("test config has no empty buttons to edit")
	}
	err = e.Save(bad)
	if err == nil || !strings.Contains(err.Error(), "9,0") {
		t.Fatalf("want an error naming 9,0, got %v", err)
	}
	if got, _ := e.Config(); !strings.Contains(got, `"cols": 6`) {
		t.Fatal("a refused save changed the config")
	}

	// Previews: unsaved config, saved config, and the refusals.
	for _, tc := range []struct {
		name, cfg, profile, page string
		w, h                     int
		ok                       bool
	}{
		{"saved", "", "default", "home", 960, 480, true},
		{"unsaved", cfg, "default", "home", 480, 240, true},
		{"no profile", "", "nope", "home", 960, 480, false},
		{"no page", "", "default", "nope", 960, 480, false},
		{"bad size", "", "default", "home", 0, 480, false},
		{"huge size", "", "default", "home", 99999, 480, false},
	} {
		got, err := e.Preview(tc.cfg, tc.profile, tc.page, tc.w, tc.h)
		if (err == nil) != tc.ok || tc.ok && !strings.HasPrefix(got, "data:image/png;base64,") {
			t.Errorf("%s: got %.30q, %v", tc.name, got, err)
		}
	}
}
