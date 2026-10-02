package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Control is something done to the computer as a whole: media keys, the sound, lock, sleep.
type Control struct {
	Kind  string // an action type: "media.play", "volume.set", "lock"...
	Level int    // volume.set: 0 to 100
	// Mute is for volume.mute and mic.mute: true mutes, false unmutes, nil flips.
	Mute *bool
}

// volumeStep is how much volume.up and volume.down change the volume, in percent.
const volumeStep = 6

// env is what the controls need of the computer, so the commands each OS gets can be tested on
// any OS: the real one runs programs and presses keys, the test's records them.
type env struct {
	goos string
	// run runs a program and returns what it printed.
	run  func(ctx context.Context, name string, args ...string) (string, error)
	have func(name string) bool
	keys func(Combo) error
	home string
	now  func() time.Time
}

func osEnv() env {
	return env{
		goos: runtime.GOOS,
		run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("%s: %w%s", name, err, tail(out))
			}
			return strings.TrimSpace(string(out)), nil
		},
		have: func(name string) bool { _, err := exec.LookPath(name); return err == nil },
		keys: tapKeys,
		home: func() string { h, _ := os.UserHomeDir(); return h }(),
		now:  time.Now,
	}
}

// do carries a control out the way the OS of e does it.
func (e env) do(ctx context.Context, c Control) error {
	switch c.Kind {
	case "lock", "sleep", "screenshot":
		return e.system(ctx, c.Kind)
	case "media.play", "media.next", "media.prev":
		return e.media(ctx, c.Kind)
	}
	switch e.goos {
	case "darwin":
		return e.soundDarwin(ctx, c)
	case "windows":
		return e.soundWindows(c)
	default:
		return e.soundLinux(ctx, c)
	}
}

func (e env) shotPath(dir string) string {
	return filepath.Join(e.home, dir, "Screenshot "+e.now().Format("2006-01-02 15.04.05")+".png")
}

func (e env) system(ctx context.Context, kind string) error {
	switch e.goos {
	case "darwin":
		switch kind {
		case "lock":
			// There is no command that locks the screen: this is the shortcut for it.
			return e.keys(Combo{Key: "q", Mods: []string{"cmd", "ctrl"}})
		case "sleep":
			_, err := e.run(ctx, "pmset", "sleepnow")
			return err
		}
		_, err := e.run(ctx, "screencapture", "-x", e.shotPath("Desktop"))
		return err
	case "windows":
		switch kind {
		case "lock":
			_, err := e.run(ctx, "rundll32.exe", "user32.dll,LockWorkStation")
			return err
		case "sleep":
			_, err := e.run(ctx, "rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0")
			return err
		}
		return e.keys(Combo{Key: "s", Mods: []string{"cmd", "shift"}}) // the Snipping Tool
	}
	switch kind {
	case "lock":
		_, err := e.run(ctx, "loginctl", "lock-session")
		return err
	case "sleep":
		_, err := e.run(ctx, "systemctl", "suspend")
		return err
	}
	file := e.shotPath("Pictures")
	for _, t := range [][]string{{"grim", file}, {"gnome-screenshot", "-f", file}, {"spectacle", "-b", "-n", "-o", file}, {"scrot", file}} {
		if e.have(t[0]) {
			_, err := e.run(ctx, t[0], t[1:]...)
			return err
		}
	}
	return errors.New("no screenshot program found: install grim, gnome-screenshot, spectacle or scrot")
}

func (e env) media(ctx context.Context, kind string) error {
	if e.goos == "linux" {
		verb := map[string]string{"media.play": "play-pause", "media.next": "next", "media.prev": "previous"}[kind]
		if !e.have("playerctl") {
			return errors.New("playerctl is not installed; media keys on Linux use it")
		}
		_, err := e.run(ctx, "playerctl", verb)
		return err
	}
	// The system's own media keys: whichever player is the active one gets them.
	return e.keys(Combo{Key: map[string]string{"media.play": "audio_play", "media.next": "audio_next", "media.prev": "audio_prev"}[kind]})
}

func boolWord(b bool) string { return strconv.FormatBool(b) }

// micBefore is the input volume a Mac had before mic.mute set it to 0: macOS has no mute for the
// input, only its level.
var (
	micMu     sync.Mutex
	micBefore = 75
)

func (e env) soundDarwin(ctx context.Context, c Control) error {
	osa := func(script string) (string, error) { return e.run(ctx, "osascript", "-e", script) }
	level := func(what string) (int, error) {
		out, err := osa(what + " of (get volume settings)")
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(out)
	}
	switch c.Kind {
	case "volume.up", "volume.down":
		cur, err := level("output volume")
		if err != nil {
			return err
		}
		d := volumeStep
		if c.Kind == "volume.down" {
			d = -d
		}
		_, err = osa(fmt.Sprintf("set volume output volume %d", clamp(cur+d)))
		return err
	case "volume.set":
		_, err := osa(fmt.Sprintf("set volume output volume %d", clamp(c.Level)))
		return err
	case "volume.mute":
		mute := false
		if c.Mute != nil {
			mute = *c.Mute
		} else {
			out, err := osa("output muted of (get volume settings)")
			if err != nil {
				return err
			}
			mute = out != "true"
		}
		_, err := osa("set volume output muted " + boolWord(mute))
		return err
	case "mic.mute":
		micMu.Lock()
		defer micMu.Unlock()
		cur, err := level("input volume")
		if err != nil {
			return err
		}
		mute := cur != 0
		if c.Mute != nil {
			mute = *c.Mute
		}
		if mute {
			if cur > 0 {
				micBefore = cur
			}
			_, err = osa("set volume input volume 0")
		} else if cur == 0 {
			_, err = osa(fmt.Sprintf("set volume input volume %d", micBefore))
		}
		return err
	}
	return fmt.Errorf("%s is not supported here", c.Kind)
}

func (e env) soundLinux(ctx context.Context, c Control) error {
	sink, source := "@DEFAULT_AUDIO_SINK@", "@DEFAULT_AUDIO_SOURCE@"
	wp := e.have("wpctl")
	if !wp && !e.have("pactl") {
		return errors.New("neither wpctl nor pactl is installed; sound controls on Linux use one of them")
	}
	mute := func(dev string, m *bool, pa string) error {
		v := "toggle"
		if m != nil {
			v = map[bool]string{true: "1", false: "0"}[*m]
		}
		var err error
		if wp {
			_, err = e.run(ctx, "wpctl", "set-mute", dev, v)
		} else {
			_, err = e.run(ctx, "pactl", pa, map[string]string{"@DEFAULT_AUDIO_SINK@": "@DEFAULT_SINK@", "@DEFAULT_AUDIO_SOURCE@": "@DEFAULT_SOURCE@"}[dev], v)
		}
		return err
	}
	vol := func(wpArg, paArg string) error {
		var err error
		if wp {
			// -l 1.0: never past 100 %, which wpctl would allow.
			_, err = e.run(ctx, "wpctl", "set-volume", "-l", "1.0", sink, wpArg)
		} else {
			_, err = e.run(ctx, "pactl", "set-sink-volume", "@DEFAULT_SINK@", paArg)
		}
		return err
	}
	switch c.Kind {
	case "volume.up":
		return vol(fmt.Sprintf("%d%%+", volumeStep), fmt.Sprintf("+%d%%", volumeStep))
	case "volume.down":
		return vol(fmt.Sprintf("%d%%-", volumeStep), fmt.Sprintf("-%d%%", volumeStep))
	case "volume.set":
		p := fmt.Sprintf("%d%%", clamp(c.Level))
		return vol(p, p)
	case "volume.mute":
		return mute(sink, c.Mute, "set-sink-mute")
	case "mic.mute":
		return mute(source, c.Mute, "set-source-mute")
	}
	return fmt.Errorf("%s is not supported here", c.Kind)
}

// soundWindows uses the volume keys, which need nothing installed. Their step is 2 %, and they
// cannot say what the level or the mute is, so volume.set counts from the bottom and a mute can
// only be flipped. The microphone has no key (it needs the Core Audio API; see docs/actions.md).
func (e env) soundWindows(c Control) error {
	press := func(key string, n int) error {
		for i := 0; i < n; i++ {
			if err := e.keys(Combo{Key: key}); err != nil {
				return err
			}
		}
		return nil
	}
	switch c.Kind {
	case "volume.up":
		return press("audio_vol_up", volumeStep/2)
	case "volume.down":
		return press("audio_vol_down", volumeStep/2)
	case "volume.set":
		if err := press("audio_vol_down", 50); err != nil {
			return err
		}
		return press("audio_vol_up", clamp(c.Level)/2)
	case "volume.mute":
		if c.Mute != nil {
			return errors.New("Windows can only flip the mute: leave Mute on \"flip\"")
		}
		return press("audio_mute", 1)
	case "mic.mute":
		return errors.New("muting the microphone is not supported on Windows yet")
	}
	return fmt.Errorf("%s is not supported here", c.Kind)
}

func clamp(n int) int { return min(100, max(0, n)) }

// The handlers below turn an action's parameters into a Control.

func simple(kind string) Handler {
	return func(ctx context.Context, r *Registry, a *deck.Action) error {
		return r.Sys.Control(ctx, Control{Kind: kind})
	}
}

func muteAction(kind string) Handler {
	return func(ctx context.Context, r *Registry, a *deck.Action) error {
		var p struct {
			Mute *bool `json:"mute"`
		}
		if err := params(a, &p); err != nil {
			return err
		}
		return r.Sys.Control(ctx, Control{Kind: kind, Mute: p.Mute})
	}
}

func volumeSet(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Level *float64 `json:"level"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	if p.Level == nil || *p.Level < 0 || *p.Level > 100 {
		return errors.New("level must be 0 to 100")
	}
	return r.Sys.Control(ctx, Control{Kind: "volume.set", Level: int(*p.Level + 0.5)})
}

func registerControls(r *Registry) {
	for _, k := range []string{"media.play", "media.next", "media.prev", "volume.up", "volume.down", "lock", "sleep", "screenshot"} {
		r.Register(k, simple(k))
	}
	r.Register("volume.set", volumeSet)
	r.Register("volume.mute", muteAction("volume.mute"))
	r.Register("mic.mute", muteAction("mic.mute"))
}

// Muted says whether the sound ("volume.mute") or the microphone ("mic.mute") is muted now, for a
// button that shows it. Windows cannot say: its mute is a key press.
func Muted(ctx context.Context, kind string) (bool, error) { return osEnv().muted(ctx, kind) }

func (e env) muted(ctx context.Context, kind string) (bool, error) {
	if kind != "volume.mute" && kind != "mic.mute" {
		return false, fmt.Errorf("%s has no mute to read", kind)
	}
	switch e.goos {
	case "darwin":
		get := func(what string) (string, error) {
			return e.run(ctx, "osascript", "-e", what+" of (get volume settings)")
		}
		if kind == "volume.mute" {
			out, err := get("output muted")
			return out == "true", err
		}
		// macOS has no input mute: mic.mute sets the level to 0, so 0 is muted.
		out, err := get("input volume")
		return out == "0", err
	case "linux":
		wpDev, paCmd, paDev := "@DEFAULT_AUDIO_SINK@", "get-sink-mute", "@DEFAULT_SINK@"
		if kind == "mic.mute" {
			wpDev, paCmd, paDev = "@DEFAULT_AUDIO_SOURCE@", "get-source-mute", "@DEFAULT_SOURCE@"
		}
		if e.have("wpctl") {
			out, err := e.run(ctx, "wpctl", "get-volume", wpDev) // "Volume: 0.40 [MUTED]"
			return strings.Contains(out, "[MUTED]"), err
		}
		if e.have("pactl") {
			out, err := e.run(ctx, "pactl", paCmd, paDev) // "Mute: yes"
			return strings.Contains(out, "yes"), err
		}
		return false, errors.New("neither wpctl nor pactl is installed")
	}
	return false, errors.New("this OS cannot say if it is muted")
}
