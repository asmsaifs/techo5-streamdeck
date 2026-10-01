// Package actions is the action registry and the per-OS implementations of each action.
//
// An action is what a button does, written in config.json as {"type": "...", ...parameters}. The
// deck handles page and back itself; everything else is run here. The device can only press a
// button the config defines: nothing in this package takes an action, a command or a path from the
// network.
package actions

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Handler runs one action.
type Handler func(ctx context.Context, r *Registry, a *deck.Action) error

// Registry maps action types to their handlers and remembers the state of toggles.
type Registry struct {
	Sys System
	Log *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler
	toggles  map[string]bool
	tmu      sync.Mutex // serializes toggles
}

// New is a Registry with every built-in action, acting on sys.
func New(sys System, log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	r := &Registry{Sys: sys, Log: log, handlers: map[string]Handler{}, toggles: map[string]bool{}}
	r.Register("open.url", openURL)
	r.Register("open.app", openApp)
	r.Register("open.file", openFile)
	r.Register("keys", keys)
	r.Register("type", typeAction)
	r.Register("run", runCommand)
	r.Register("delay", delay)
	r.Register("multi", multi)
	r.Register("toggle", toggle)
	registerControls(r)
	return r
}

// Register adds or replaces the handler of an action type.
func (r *Registry) Register(typ string, h Handler) {
	r.mu.Lock()
	r.handlers[typ] = h
	r.mu.Unlock()
}

// Types lists the action types that can be run, sorted.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var t []string
	for k := range r.handlers {
		t = append(t, k)
	}
	sort.Strings(t)
	return t
}

// Run runs a. The error says what went wrong in words fit for a log or a toast.
func (r *Registry) Run(ctx context.Context, a *deck.Action) error {
	r.mu.RLock()
	h := r.handlers[a.Type]
	r.mu.RUnlock()
	if h == nil {
		return fmt.Errorf("there is no action %q", a.Type)
	}
	if err := h(ctx, r, a); err != nil {
		return fmt.Errorf("%s: %w", a.Type, err)
	}
	return nil
}

type buttonKey struct{}

// ButtonKey names a button, for the state of a toggle: the same button is the same toggle on every
// Show that has it.
func ButtonKey(profile, page string, c deck.Cell) string {
	return profile + "/" + page + "/" + c.String()
}

// WithButton marks ctx as running the button with the given key, which toggle needs.
func WithButton(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, buttonKey{}, key)
}

// On reports whether the toggle on the button with key is on.
func (r *Registry) On(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.toggles[key]
}
