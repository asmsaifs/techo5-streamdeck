package speaker

import (
	"errors"
	"testing"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/sendspin"
)

func TestDescribe(t *testing.T) {
	on := deck.Speaker{Enabled: true, Shows: []string{"Kitchen"}}
	tests := []struct {
		name   string
		cfg    deck.Speaker
		tapErr error
		st     sendspin.State
		want   string
	}{
		{"off", deck.Speaker{Shows: []string{"Kitchen"}}, nil, sendspin.State{}, "Off"},
		{"no show", deck.Speaker{Enabled: true}, nil, sendspin.State{}, "No Show picked"},
		{"no device", on, ErrNoDevice, sendspin.State{}, "Not available on this computer yet"},
		{"device failed", on, errors.New("gone"), sendspin.State{}, "Sound device failed"},
		{"waiting", on, nil, sendspin.State{}, "Waiting for sound"},
		{"connecting", on, nil, sendspin.State{Open: true}, "Connecting…"},
		{"one", on, nil, sendspin.State{Open: true, Playing: []string{"Kitchen"}}, "Playing on Kitchen"},
		{"two", on, nil, sendspin.State{Open: true, Playing: []string{"Kitchen", "Office"}}, "Playing on 2 Shows"},
		{"busy", on, nil, sendspin.State{Open: true, Busy: []string{"Kitchen"}}, "Show busy (another source playing)"},
		{"unreachable", on, nil, sendspin.State{Open: true, Failed: map[string]string{"Kitchen": "timeout"}}, "Show not reachable"},
	}
	for _, tt := range tests {
		if got := describe(tt.cfg, tt.tapErr, tt.st); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestToneResult(t *testing.T) {
	shows := []string{"Kitchen", "Office"}
	tests := []struct {
		name string
		st   sendspin.State
		want string
	}{
		{"both played", sendspin.State{Playing: shows}, ""},
		{"one busy", sendspin.State{Playing: []string{"Kitchen"}, Busy: []string{"Office"}},
			"Office is playing from another source (Music Assistant, or this computer)"},
		{"failed and silent", sendspin.State{Failed: map[string]string{"Kitchen": "no route"}},
			"Kitchen: no route; Office did not answer"},
	}
	for _, tt := range tests {
		err := toneResult(shows, tt.st)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

type fakeTap struct {
	sounds  chan Sound
	vols    chan Volume
	stopped chan struct{}
}

func (f *fakeTap) Start() (<-chan Sound, error) { return f.sounds, nil }
func (f *fakeTap) Volume() <-chan Volume        { return f.vols }
func (f *fakeTap) Stop()                        { close(f.sounds); close(f.stopped) }

// Turning the speaker on opens the device, and off closes it.
func TestApply(t *testing.T) {
	tap := &fakeTap{sounds: make(chan Sound), vols: make(chan Volume), stopped: make(chan struct{})}
	defer func(old func() (Tap, error)) { newTap = old }(newTap)
	newTap = func() (Tap, error) { return tap, nil }

	s := New()
	changes := 0
	s.OnChange(func() { changes++ })
	s.Apply(&deck.Speaker{Enabled: true, Shows: []string{"Kitchen"}})
	if st := s.Status(); !st.Enabled || st.Text != "Waiting for sound" {
		t.Errorf("on: %+v", st)
	}
	s.Apply(&deck.Speaker{Enabled: false, Shows: []string{"Kitchen"}})
	select {
	case <-tap.stopped:
	default:
		t.Error("the device was not closed")
	}
	if st := s.Status(); st.Enabled || st.Text != "Off" {
		t.Errorf("off: %+v", st)
	}
	if changes < 2 {
		t.Errorf("%d changes reported, want one per Apply", changes)
	}
}

func TestNoDevice(t *testing.T) {
	s := New()
	defer s.Close()
	s.Apply(&deck.Speaker{Enabled: true, Shows: []string{"Kitchen"}})
	if st := s.Status(); st.Text != "Not available on this computer yet" {
		t.Errorf("%+v", st)
	}
}
