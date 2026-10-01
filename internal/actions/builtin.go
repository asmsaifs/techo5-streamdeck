package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// params decodes an action's parameters.
func params(a *deck.Action, v any) error {
	if err := a.Params(v); err != nil {
		return fmt.Errorf("bad parameters: %w", err)
	}
	return nil
}

// target checks something that is handed to an opener. One that starts with "-" would be read by
// the opener as an option, not as the thing to open.
func target(what, s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", fmt.Errorf("%s is missing", what)
	case strings.HasPrefix(s, "-"):
		return "", fmt.Errorf("%s %q starts with \"-\"", what, s)
	}
	return s, nil
}

func openURL(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		URL string `json:"url"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	u, err := target("url", p.URL)
	if err != nil {
		return err
	}
	if !strings.Contains(u, ":") {
		return fmt.Errorf("url %q has no scheme, like https://", u)
	}
	return r.Sys.Open(ctx, OpenURL, u)
}

func openApp(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		App string `json:"app"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	app, err := target("app", p.App)
	if err != nil {
		return err
	}
	return r.Sys.Open(ctx, OpenApp, app)
}

func openFile(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Path string `json:"path"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	path, err := target("path", p.Path)
	if err != nil {
		return err
	}
	return r.Sys.Open(ctx, OpenFile, expand(path))
}

func keys(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Keys string `json:"keys"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	c, err := ParseCombo(p.Keys)
	if err != nil {
		return err
	}
	return r.Sys.Keys(ctx, c)
}

func typeAction(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Text string `json:"text"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	if p.Text == "" {
		return errors.New("text is missing")
	}
	return r.Sys.Type(ctx, p.Text)
}

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 10 * time.Minute
)

// runCommand runs a program with arguments, not a shell line, so nothing in an argument is
// reinterpreted. "shell": true runs "command" as a shell line instead, for pipes and the like.
func runCommand(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Command  string   `json:"command"`
		Args     []string `json:"args"`
		Cwd      string   `json:"cwd"`
		TimeoutS float64  `json:"timeout"` // seconds, 30 by default
		Shell    bool     `json:"shell"`
		Detach   bool     `json:"detach"` // start it and do not wait: for a program that stays open
	}
	if err := params(a, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Command) == "" {
		return errors.New("command is missing")
	}
	timeout := defaultTimeout
	if p.TimeoutS > 0 {
		timeout = min(time.Duration(p.TimeoutS*float64(time.Second)), maxTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return r.Sys.Exec(ctx, ExecSpec{Command: p.Command, Args: p.Args, Dir: expand(p.Cwd), Shell: p.Shell, Wait: !p.Detach})
}

func delay(ctx context.Context, _ *Registry, a *deck.Action) error {
	var p struct {
		MS int `json:"ms"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	if p.MS < 0 || p.MS > 60_000 {
		return fmt.Errorf("ms is %d; it must be 0 to 60000", p.MS)
	}
	select {
	case <-time.After(time.Duration(p.MS) * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// multi runs its steps in order and stops at the first that fails.
func multi(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Steps []*deck.Action `json:"steps"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	if len(p.Steps) == 0 {
		return errors.New("steps is empty")
	}
	for i, s := range p.Steps {
		if s == nil {
			return fmt.Errorf("step %d is empty", i+1)
		}
		if err := r.Run(ctx, s); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return nil
}

// toggle runs "on" when it is off and "off" when it is on, and flips only if that worked. The
// state is the deck's own memory: it starts off, and does not look at the computer, so a
// toggle that is changed some other way will be out of step until live tiles (3.4) can ask.
func toggle(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		On  *deck.Action `json:"on"`
		Off *deck.Action `json:"off"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	if p.On == nil || p.Off == nil {
		return errors.New("a toggle needs both on and off")
	}
	r.tmu.Lock() // one press at a time, or two quick taps would both flip from the same state
	defer r.tmu.Unlock()
	key, _ := ctx.Value(buttonKey{}).(string)
	if key == "" {
		return errors.New("a toggle can only be run from a button")
	}
	next := !r.On(key)
	step := p.Off
	if next {
		step = p.On
	}
	if err := r.Run(ctx, step); err != nil {
		return err
	}
	r.mu.Lock()
	r.toggles[key] = next
	r.mu.Unlock()
	return nil
}
