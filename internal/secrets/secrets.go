// Package secrets keeps the tokens and passwords of the integrations (Home Assistant, OBS) in the
// operating system's keychain, and never in config.json: the config is a file people copy, back up
// and paste into bug reports.
package secrets

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// Names of the secrets.
const (
	HomeAssistantToken = "homeassistant-token"
	OBSPassword        = "obs-password"
)

// ErrNotFound means there is no secret of that name.
var ErrNotFound = errors.New("secret not set")

const service = "techo5-streamdeck"

// Store is a place secrets are kept.
type Store interface {
	Get(name string) (string, error) // ErrNotFound if there is none
	Set(name, value string) error
	Delete(name string) error
}

// Keyring is the OS keychain: Keychain on macOS, Credential Manager on Windows, the Secret
// Service (GNOME Keyring, KWallet) on Linux.
func Keyring() Store { return keychain{} }

type keychain struct{}

func (keychain) Get(name string) (string, error) {
	v, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (keychain) Set(name, value string) error { return keyring.Set(service, name, value) }

func (keychain) Delete(name string) error {
	err := keyring.Delete(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is a Store that forgets at exit, for tests.
func Memory() Store { return &memory{m: map[string]string{}} }

type memory struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memory) Get(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *memory) Set(name, value string) error {
	s.mu.Lock()
	s.m[name] = value
	s.mu.Unlock()
	return nil
}

func (s *memory) Delete(name string) error {
	s.mu.Lock()
	delete(s.m, name)
	s.mu.Unlock()
	return nil
}
