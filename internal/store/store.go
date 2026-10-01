// Package store loads, saves and migrates config.json, and watches it so a hand edit or the
// editor's save reaches the running deck.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// FileName is the config's name in the store's folder; IconsDir holds icons the user added.
const (
	FileName = "config.json"
	IconsDir = "icons"
)

// Dir is where the store lives: techo5-streamdeck in the OS's per-user config folder.
func Dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "techo5-streamdeck"), nil
}

// Store is the config on disk and the last good copy of it in memory.
type Store struct {
	dir string

	mu   sync.Mutex
	cfg  *deck.Config
	hash [32]byte // of the file as last read or written, so our own save is not taken for an edit
}

// Open opens the store in dir, making the folder and a first config, with a new random key, if
// there are none. A config that is there but broken is an error: it is the user's, and is never
// replaced.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, IconsDir), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	b, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return s, s.Save(deck.New(wire.NewKey()))
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s:\n%w", s.Path(), err)
	}
	s.cfg, s.hash = c, sha256.Sum256(b)
	return s, nil
}

// Path is the config file's.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

// Config is the last good config. Callers must not change it: Save a changed copy.
func (s *Store) Config() *deck.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Save checks c and writes it. A config that does not validate is not written.
func (s *Store) Save(c *deck.Config) error {
	c.Fill()
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := Encode(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeAtomic(s.Path(), b); err != nil {
		return err
	}
	s.cfg, s.hash = c, sha256.Sum256(b)
	return nil
}

// Load reads, migrates, fills and validates the config at path.
func Load(path string) (*deck.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse is Load for a config already read.
func Parse(b []byte) (*deck.Config, error) {
	b, err := migrate(b)
	if err != nil {
		return nil, err
	}
	c, err := deck.Decode(b)
	if err != nil {
		return nil, located(b, err)
	}
	c.Fill()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Encode is the config as it is written: indented, for people to edit, with a final newline.
func Encode(c *deck.Config) ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// migrate brings an older config up to deck.Version. Version 0 is a file with no version at all,
// written by hand from the README: the same shape as 1.
func migrate(b []byte) ([]byte, error) {
	var head struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, located(b, err)
	}
	v := 0
	if head.Version != nil {
		v = *head.Version
	}
	switch {
	case v > deck.Version:
		return nil, fmt.Errorf("version: is %d, written by a newer TECHO5 Stream Deck; this one reads up to %d", v, deck.Version)
	case v < 0:
		return nil, fmt.Errorf("version: is %d", v)
	case v == 0:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, located(b, err)
		}
		m["version"] = json.RawMessage("1")
		return json.Marshal(m)
	}
	return b, nil
}

// located adds the line and column to a JSON error, which on its own gives a byte offset.
func located(b []byte, err error) error {
	var off int64
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off = syn.Offset
	case errors.As(err, &typ):
		off = typ.Offset
	default:
		return err
	}
	before := b[:min(int(off), len(b))]
	line := bytes.Count(before, []byte("\n")) + 1
	col := len(before) - bytes.LastIndexByte(before, '\n')
	return fmt.Errorf("line %d, column %d: %w", line, col, err)
}

// writeAtomic writes b to path by way of a temporary file and a rename, so a crash or a reader at
// the wrong moment never sees half a config. The file holds the key, so only its owner may read it.
func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// settle is how long the file has to be quiet before it is read: editors write in several steps
// (truncate, write, or write a copy and rename it over), and the first of them is not the edit.
const settle = 200 * time.Millisecond

// Watch calls changed with each new good config, or with the error when an edit leaves the file
// broken (the last good config stays in use), until ctx ends. It watches the folder rather than
// the file, because an editor that saves by renaming a new file over the old one would otherwise
// leave it watching a file that is gone.
func (s *Store) Watch(ctx context.Context, changed func(*deck.Config, error)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(s.dir); err != nil {
		w.Close()
		return err
	}
	go func() {
		defer w.Close()
		var timer *time.Timer
		fire := make(chan struct{}, 1)
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != FileName || ev.Op == fsnotify.Chmod {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(settle, func() {
					select {
					case fire <- struct{}{}:
					default:
					}
				})
			case <-fire:
				if c, err, ok := s.reload(); ok {
					changed(c, err)
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

// reload reads the file again. ok is false when there is nothing to tell: the file is what was
// last read or written (our own save), or it is gone for a moment mid-save.
func (s *Store) reload() (*deck.Config, error, bool) {
	b, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false
	}
	if err != nil {
		return nil, err, true
	}
	h := sha256.Sum256(b)
	s.mu.Lock()
	same := h == s.hash
	s.mu.Unlock()
	if same {
		return nil, nil, false
	}
	c, err := Parse(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hash = h // a broken file is reported once, not on every unrelated event
	if err != nil {
		return nil, err, true
	}
	s.cfg = c
	return c, nil, true
}
