package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/actions"
	"github.com/asmsaifs/techo5-streamdeck/internal/core"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/foreground"
	"github.com/asmsaifs/techo5-streamdeck/internal/hotkeys"
	"github.com/asmsaifs/techo5-streamdeck/internal/secrets"
	"github.com/asmsaifs/techo5-streamdeck/internal/server"
	"github.com/asmsaifs/techo5-streamdeck/internal/sources/web"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

// Editor is what the editor window can call. It is thin on purpose: the model, the checks and
// the drawing all live in internal/, so the preview is the very picture the Show gets.
type Editor struct {
	core *core.Core
	hot  *hotkeys.Manager
}

// HotkeyProblems is what the system refused of the config's hotkeys, by combo: another program
// owns it, or it is not a combo the system can register. The editor shows these next to the
// button.
func (e *Editor) HotkeyProblems() map[string]string { return e.hot.Status() }

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
	e.core.Reload()
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

// Browsers lists the browsers website tiles run in: their windows, how many are parked, and the
// memory they hold.
func (e *Editor) Browsers() []web.BrowserInfo { return e.core.Server.Web.Stats() }

// Schemas describes the actions the inspector can edit.
func (e *Editor) Schemas() []actions.Schema { return actions.Schemas() }

// TestAction runs an action now, on this computer, as if its button had been pressed, and reports
// what went wrong in words fit to show. Only the editor window can call this; a Show cannot.
func (e *Editor) TestAction(actionJSON string) error {
	var a deck.Action
	if err := json.Unmarshal([]byte(actionJSON), &a); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return e.core.Actions.Run(ctx, &a)
}

// SecretStatus says which secrets are set. The values themselves never come back to the window.
type SecretStatus struct {
	HomeAssistantToken bool
	OBSPassword        bool
}

var secretNames = map[string]string{"homeassistant": secrets.HomeAssistantToken, "obs": secrets.OBSPassword}

// Secrets reports which of the integrations' secrets are in the keychain.
func (e *Editor) Secrets() SecretStatus {
	has := func(n string) bool { _, err := e.core.Secrets.Get(n); return err == nil }
	return SecretStatus{HomeAssistantToken: has(secrets.HomeAssistantToken), OBSPassword: has(secrets.OBSPassword)}
}

// SetSecret puts the token ("homeassistant") or password ("obs") in the keychain; an empty value
// removes it. It takes effect at once and is not part of the config the editor saves.
func (e *Editor) SetSecret(which, value string) error {
	name, ok := secretNames[which]
	if !ok {
		return fmt.Errorf("there is no secret %q", which)
	}
	if value == "" {
		return e.core.Secrets.Delete(name)
	}
	if err := e.core.Secrets.Set(name, value); err != nil {
		return fmt.Errorf("the system keychain refused it: %w", err)
	}
	return nil
}

// FrontApp waits the given seconds, so the user can switch to an application, and says which one
// is in front then: it is how the editor fills in an auto-switch rule's application.
func (e *Editor) FrontApp(waitSeconds int) (foreground.App, error) {
	if waitSeconds < 0 || waitSeconds > 30 {
		return foreground.App{}, errors.New("wait between 0 and 30 seconds")
	}
	time.Sleep(time.Duration(waitSeconds) * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	app, err := foreground.Current(ctx)
	if err != nil {
		return app, err
	}
	if core.IsSelf(app) {
		return foreground.App{}, errors.New("this window was still in front: switch to the application within the wait")
	}
	return app, nil
}
