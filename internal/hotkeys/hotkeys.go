// Package hotkeys registers global keyboard combos that press deck buttons.
//
// The combos live in config.json ("hotkeys", by accelerator). The Manager keeps the operating
// system's registrations in step with them whenever the config changes. It does not know how to
// register a combo itself: the Registrar does, and in the desktop app that is Wails'
// GlobalShortcut, which uses no permission on macOS and the portal on Wayland.
package hotkeys

import (
	"log/slog"
	"sort"
	"sync"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Registrar binds combos to callbacks in the operating system.
type Registrar interface {
	Register(accelerator string, callback func()) error
	Unregister(accelerator string) error
}

// Manager keeps the registered combos equal to the ones the config wants.
type Manager struct {
	reg   Registrar
	press func(accelerator string)
	log   *slog.Logger

	mu     sync.Mutex
	active map[string]bool   // combos the OS accepted
	errs   map[string]string // combos it did not, and why
}

// New is a Manager that calls press with the combo whenever one is hit. press looks the button up
// in the config as it is then, so a button retargeted by an edit needs no new registration.
func New(reg Registrar, press func(accelerator string), log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{reg: reg, press: press, log: log, active: map[string]bool{}, errs: map[string]string{}}
}

// Sync registers the combos in want that are not registered yet, drops the ones no longer wanted,
// and tries again the ones the OS refused before (another program may have let go of them). It
// returns what is wrong, by combo, in words fit for the editor to show.
func (m *Manager) Sync(want map[string]deck.HotkeyTarget) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	for accel := range m.active {
		if _, ok := want[accel]; !ok {
			if err := m.reg.Unregister(accel); err != nil {
				m.log.Warn("hotkey: could not release", "hotkey", accel, "err", err)
			}
			delete(m.active, accel)
		}
	}
	m.errs = map[string]string{}
	// In order, so the log and a conflict between two spellings of one combo come out the same
	// every time.
	accels := make([]string, 0, len(want))
	for accel := range want {
		accels = append(accels, accel)
	}
	sort.Strings(accels)
	for _, accel := range accels {
		if m.active[accel] {
			continue
		}
		accel := accel
		if err := m.reg.Register(accel, func() { m.press(accel) }); err != nil {
			m.errs[accel] = err.Error()
			m.log.Warn("hotkey: not registered", "hotkey", accel, "err", err)
			continue
		}
		m.active[accel] = true
	}
	return m.statusLocked()
}

// Status is what Sync last reported.
func (m *Manager) Status() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() map[string]string {
	out := make(map[string]string, len(m.errs))
	for k, v := range m.errs {
		out[k] = v
	}
	return out
}

// Close releases every combo.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for accel := range m.active {
		_ = m.reg.Unregister(accel)
		delete(m.active, accel)
	}
}
