package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"

	"github.com/asmsaifs/techo5-streamdeck/internal/core"
	"github.com/asmsaifs/techo5-streamdeck/internal/server"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

// Editor is what the editor window can call. It is thin on purpose: the model, the checks and
// the drawing all live in internal/, so the preview is the very picture the Show gets.
type Editor struct{ core *core.Core }

// Config is config.json as the editor shows it: the last good config, as JSON text.
func (e *Editor) Config() (string, error) {
	b, err := store.Encode(e.core.Store.Config())
	return string(b), err
}

// Save checks the config the editor sends and writes it. The error text names the offending path
// ("profiles.default.pages.home.buttons.9,0: ..."), for the editor to show as it is. The decks
// on connected Shows are redrawn at once.
func (e *Editor) Save(configJSON string) error {
	c, err := store.Parse([]byte(configJSON))
	if err != nil {
		return err
	}
	if err := e.core.Store.Save(c); err != nil {
		return err
	}
	// Save marks the file as already seen, so the watcher stays quiet about our own write.
	e.core.Server.Reload()
	return nil
}

// Preview draws a page of a profile the way a Show of w by h pixels gets it, as a PNG data URL.
// configJSON is the editor's unsaved config, so the preview follows every edit; an empty string
// means the saved one.
func (e *Editor) Preview(configJSON, profile, page string, w, h int) (string, error) {
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return "", fmt.Errorf("preview: %dx%d is not a screen size", w, h)
	}
	c := e.core.Store.Config()
	if configJSON != "" {
		var err error
		if c, err = store.Parse([]byte(configJSON)); err != nil {
			return "", err
		}
	}
	p := c.Profiles[profile]
	if p == nil {
		return "", errors.New("preview: no profile " + profile)
	}
	pg := p.Pages[page]
	if pg == nil {
		return "", errors.New("preview: no page " + page)
	}
	img := e.core.Server.Renderer.Grid(p, pg, image.Pt(w, h), nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Devices lists the Shows connected now.
func (e *Editor) Devices() []server.Info { return e.core.Server.Sessions() }
