package actions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// rec is an env that records the programs run and the keys pressed, as lines.
type rec struct {
	lines []string
	have  map[string]bool
	out   map[string]string // what a program prints, by its command line
	fail  string            // a command line that fails
}

func (r *rec) env(goos string) env {
	return env{
		goos: goos,
		run: func(_ context.Context, name string, args ...string) (string, error) {
			line := strings.Join(append([]string{name}, args...), " ")
			r.lines = append(r.lines, line)
			if line == r.fail {
				return "", errors.New("failed")
			}
			return r.out[line], nil
		},
		have: func(n string) bool { return r.have[n] },
		keys: func(c Combo) error {
			r.lines = append(r.lines, "key "+strings.Join(append(append([]string{}, c.Mods...), c.Key), "+"))
			return nil
		},
		home: "/home/u",
		now:  func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
}

func yes(b bool) *bool { return &b }

func TestControlCommandsPerOS(t *testing.T) {
	tests := []struct {
		name string
		goos string
		have []string
		out  map[string]string
		c    Control
		want []string
	}{
		{"mac play uses the media key", "darwin", nil, nil, Control{Kind: "media.play"}, []string{"key audio_play"}},
		{"mac volume up reads then sets", "darwin", nil,
			map[string]string{"osascript -e output volume of (get volume settings)": "50"},
			Control{Kind: "volume.up"}, []string{"osascript -e output volume of (get volume settings)", "osascript -e set volume output volume 56"}},
		{"mac volume up stops at 100", "darwin", nil,
			map[string]string{"osascript -e output volume of (get volume settings)": "98"},
			Control{Kind: "volume.up"}, []string{"osascript -e output volume of (get volume settings)", "osascript -e set volume output volume 100"}},
		{"mac volume set", "darwin", nil, nil, Control{Kind: "volume.set", Level: 30}, []string{"osascript -e set volume output volume 30"}},
		{"mac mute flips what is", "darwin", nil,
			map[string]string{"osascript -e output muted of (get volume settings)": "true"},
			Control{Kind: "volume.mute"}, []string{"osascript -e output muted of (get volume settings)", "osascript -e set volume output muted false"}},
		{"mac mute true needs no read", "darwin", nil, nil, Control{Kind: "volume.mute", Mute: yes(true)}, []string{"osascript -e set volume output muted true"}},
		{"mac mic mute sets the input to 0", "darwin", nil,
			map[string]string{"osascript -e input volume of (get volume settings)": "60"},
			Control{Kind: "mic.mute", Mute: yes(true)}, []string{"osascript -e input volume of (get volume settings)", "osascript -e set volume input volume 0"}},
		{"mac mic unmute restores it", "darwin", nil,
			map[string]string{"osascript -e input volume of (get volume settings)": "0"},
			Control{Kind: "mic.mute", Mute: yes(false)}, []string{"osascript -e input volume of (get volume settings)", "osascript -e set volume input volume 60"}},
		{"mac lock", "darwin", nil, nil, Control{Kind: "lock"}, []string{"key cmd+ctrl+q"}},
		{"mac sleep", "darwin", nil, nil, Control{Kind: "sleep"}, []string{"pmset sleepnow"}},
		{"mac screenshot", "darwin", nil, nil, Control{Kind: "screenshot"}, []string{"screencapture -x /home/u/Desktop/Screenshot 2026-01-02 03.04.05.png"}},

		{"linux play", "linux", []string{"playerctl"}, nil, Control{Kind: "media.play"}, []string{"playerctl play-pause"}},
		{"linux prev", "linux", []string{"playerctl"}, nil, Control{Kind: "media.prev"}, []string{"playerctl previous"}},
		{"linux wpctl up", "linux", []string{"wpctl", "pactl"}, nil, Control{Kind: "volume.up"}, []string{"wpctl set-volume -l 1.0 @DEFAULT_AUDIO_SINK@ 6%+"}},
		{"linux wpctl set", "linux", []string{"wpctl"}, nil, Control{Kind: "volume.set", Level: 120}, []string{"wpctl set-volume -l 1.0 @DEFAULT_AUDIO_SINK@ 100%"}},
		{"linux wpctl mic flip", "linux", []string{"wpctl"}, nil, Control{Kind: "mic.mute"}, []string{"wpctl set-mute @DEFAULT_AUDIO_SOURCE@ toggle"}},
		{"linux wpctl unmute", "linux", []string{"wpctl"}, nil, Control{Kind: "volume.mute", Mute: yes(false)}, []string{"wpctl set-mute @DEFAULT_AUDIO_SINK@ 0"}},
		{"linux pactl down", "linux", []string{"pactl"}, nil, Control{Kind: "volume.down"}, []string{"pactl set-sink-volume @DEFAULT_SINK@ -6%"}},
		{"linux pactl mic", "linux", []string{"pactl"}, nil, Control{Kind: "mic.mute", Mute: yes(true)}, []string{"pactl set-source-mute @DEFAULT_SOURCE@ 1"}},
		{"linux lock", "linux", nil, nil, Control{Kind: "lock"}, []string{"loginctl lock-session"}},
		{"linux sleep", "linux", nil, nil, Control{Kind: "sleep"}, []string{"systemctl suspend"}},
		{"linux screenshot picks the first it has", "linux", []string{"scrot", "gnome-screenshot"}, nil, Control{Kind: "screenshot"},
			[]string{"gnome-screenshot -f /home/u/Pictures/Screenshot 2026-01-02 03.04.05.png"}},

		{"windows play", "windows", nil, nil, Control{Kind: "media.next"}, []string{"key audio_next"}},
		{"windows volume up is 3 steps", "windows", nil, nil, Control{Kind: "volume.up"}, []string{"key audio_vol_up", "key audio_vol_up", "key audio_vol_up"}},
		{"windows mute flips", "windows", nil, nil, Control{Kind: "volume.mute"}, []string{"key audio_mute"}},
		{"windows lock", "windows", nil, nil, Control{Kind: "lock"}, []string{"rundll32.exe user32.dll,LockWorkStation"}},
		{"windows sleep", "windows", nil, nil, Control{Kind: "sleep"}, []string{"rundll32.exe powrprof.dll,SetSuspendState 0,1,0"}},
		{"windows screenshot", "windows", nil, nil, Control{Kind: "screenshot"}, []string{"key cmd+shift+s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			micBefore = 75
			if strings.Contains(tt.name, "restores") {
				micBefore = 60
			}
			r := &rec{have: map[string]bool{}, out: tt.out}
			for _, h := range tt.have {
				r.have[h] = true
			}
			if err := r.env(tt.goos).do(context.Background(), tt.c); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.lines, tt.want) {
				t.Errorf("ran\n  %s\nwant\n  %s", strings.Join(r.lines, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

func TestControlRemembersMicLevelAcrossMute(t *testing.T) {
	micBefore = 75
	r := &rec{have: map[string]bool{}, out: map[string]string{"osascript -e input volume of (get volume settings)": "40"}}
	e := r.env("darwin")
	if err := e.do(context.Background(), Control{Kind: "mic.mute"}); err != nil { // flips: 40 is on, so mute
		t.Fatal(err)
	}
	if micBefore != 40 || r.lines[len(r.lines)-1] != "osascript -e set volume input volume 0" {
		t.Errorf("micBefore=%d lines=%v", micBefore, r.lines)
	}
}

func TestControlProblemsAreSaidInWords(t *testing.T) {
	r := func() *rec { return &rec{have: map[string]bool{}} }
	tests := []struct {
		name string
		goos string
		c    Control
		want string
	}{
		{"no playerctl", "linux", Control{Kind: "media.play"}, "playerctl"},
		{"no mixer", "linux", Control{Kind: "volume.up"}, "wpctl nor pactl"},
		{"no screenshot tool", "linux", Control{Kind: "screenshot"}, "no screenshot program"},
		{"windows mic", "windows", Control{Kind: "mic.mute"}, "not supported on Windows"},
		{"windows cannot set the mute", "windows", Control{Kind: "volume.mute", Mute: yes(true)}, "only flip"},
	}
	for _, tt := range tests {
		err := r().env(tt.goos).do(context.Background(), tt.c)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %v, want one with %q", tt.name, err, tt.want)
		}
	}
	bad := &rec{have: map[string]bool{"wpctl": true}, fail: "wpctl set-volume -l 1.0 @DEFAULT_AUDIO_SINK@ 6%+"}
	if err := bad.env("linux").do(context.Background(), Control{Kind: "volume.up"}); err == nil {
		t.Error("a failing program was not reported")
	}
}

func TestControlHandlers(t *testing.T) {
	f := &fake{}
	reg := New(f, nil)
	run := func(js string) error { return reg.Run(context.Background(), act(t, js)) }

	if err := run(`{"type":"volume.set","level":42.4}`); err != nil {
		t.Fatal(err)
	}
	if err := run(`{"type":"mic.mute","mute":false}`); err != nil {
		t.Fatal(err)
	}
	if err := run(`{"type":"volume.mute"}`); err != nil {
		t.Fatal(err)
	}
	if err := run(`{"type":"lock"}`); err != nil {
		t.Fatal(err)
	}
	c := f.controls
	if len(c) != 4 || c[0].Level != 42 || c[1].Mute == nil || *c[1].Mute || c[2].Mute != nil || c[3].Kind != "lock" {
		t.Errorf("controls = %+v", c)
	}
	for _, js := range []string{`{"type":"volume.set"}`, `{"type":"volume.set","level":101}`, `{"type":"volume.set","level":-1}`, `{"type":"mic.mute","mute":"yes"}`} {
		if err := run(js); err == nil {
			t.Errorf("%s was accepted", js)
		}
	}
	if len(f.controls) != 4 {
		t.Errorf("a bad action reached the system: %+v", f.controls)
	}
}
