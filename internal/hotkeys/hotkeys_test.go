package hotkeys

import (
	"errors"
	"testing"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

type fakeReg struct {
	bound  map[string]func()
	refuse map[string]bool
	calls  []string
}

func newFake() *fakeReg { return &fakeReg{bound: map[string]func(){}, refuse: map[string]bool{}} }

func (f *fakeReg) Register(a string, cb func()) error {
	f.calls = append(f.calls, "+"+a)
	if f.refuse[a] {
		return errors.New("taken")
	}
	f.bound[a] = cb
	return nil
}

func (f *fakeReg) Unregister(a string) error {
	f.calls = append(f.calls, "-"+a)
	delete(f.bound, a)
	return nil
}

func want(accels ...string) map[string]deck.HotkeyTarget {
	m := map[string]deck.HotkeyTarget{}
	for _, a := range accels {
		m[a] = deck.HotkeyTarget{Profile: "default", Page: "home", Button: "0,0"}
	}
	return m
}

func TestSyncKeepsRegistrationsEqualToConfig(t *testing.T) {
	f := newFake()
	var pressed []string
	m := New(f, func(a string) { pressed = append(pressed, a) }, nil)

	steps := []struct {
		name  string
		want  map[string]deck.HotkeyTarget
		bound []string
		calls []string
	}{
		{"first", want("Alt+1", "Alt+2"), []string{"Alt+1", "Alt+2"}, []string{"+Alt+1", "+Alt+2"}},
		{"unchanged does nothing", want("Alt+1", "Alt+2"), []string{"Alt+1", "Alt+2"}, nil},
		{"one dropped, one added", want("Alt+2", "Alt+3"), []string{"Alt+2", "Alt+3"}, []string{"-Alt+1", "+Alt+3"}},
		{"all dropped", want(), nil, []string{"-Alt+2", "-Alt+3"}},
	}
	for _, s := range steps {
		f.calls = nil
		if errs := m.Sync(s.want); len(errs) != 0 {
			t.Fatalf("%s: unexpected errors %v", s.name, errs)
		}
		if len(f.bound) != len(s.bound) {
			t.Errorf("%s: bound %d combos, want %v", s.name, len(f.bound), s.bound)
		}
		for _, a := range s.bound {
			if f.bound[a] == nil {
				t.Errorf("%s: %s is not bound", s.name, a)
			}
		}
		if len(f.calls) != len(s.calls) {
			t.Errorf("%s: calls %v, want %v", s.name, f.calls, s.calls)
		}
	}

	m.Sync(want("Alt+9"))
	f.bound["Alt+9"]()
	if len(pressed) != 1 || pressed[0] != "Alt+9" {
		t.Errorf("pressed %v, want [Alt+9]", pressed)
	}
}

func TestRefusedComboIsReportedAndRetried(t *testing.T) {
	f := newFake()
	f.refuse["Alt+1"] = true
	m := New(f, func(string) {}, nil)

	errs := m.Sync(want("Alt+1", "Alt+2"))
	if errs["Alt+1"] != "taken" || len(errs) != 1 {
		t.Fatalf("errors = %v, want only Alt+1: taken", errs)
	}
	if f.bound["Alt+2"] == nil {
		t.Error("one refused combo must not stop the others")
	}
	if got := m.Status(); got["Alt+1"] != "taken" {
		t.Errorf("Status = %v", got)
	}

	delete(f.refuse, "Alt+1")
	if errs := m.Sync(want("Alt+1", "Alt+2")); len(errs) != 0 || f.bound["Alt+1"] == nil {
		t.Errorf("after it was freed: errors %v, bound %v", errs, f.bound)
	}
}

func TestClose(t *testing.T) {
	f := newFake()
	m := New(f, func(string) {}, nil)
	m.Sync(want("Alt+1", "Alt+2"))
	m.Close()
	if len(f.bound) != 0 {
		t.Errorf("still bound after Close: %v", f.bound)
	}
}
