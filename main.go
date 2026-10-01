// Command techo5-streamdeck is the desktop app: it serves the deck to every connected Show and
// lives in the tray. Closing the editor window hides it; Quit in the tray menu ends the app.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"

	"github.com/asmsaifs/techo5-streamdeck/internal/control"
	"github.com/asmsaifs/techo5-streamdeck/internal/core"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/hotkeys"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

// The editor UI, built by "npm run build" in frontend/. dist/ holds only a .gitkeep until then, so
// the Go code still builds from a fresh checkout; the window is just empty.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if len(os.Args) > 1 && os.Args[1] == "trigger" {
		os.Exit(trigger(os.Args[2:]))
	}
	dir := flag.String("dir", "", "the config folder (default: techo5-streamdeck in the user config folder)")
	listen := flag.String("listen", "", "address to listen on (default: the config's server.listen)")
	dry := flag.Bool("dry-run", false, "log what actions would do instead of doing it")
	hidden := flag.Bool("hidden", false, "start in the tray without opening the editor (used at login)")
	quit := flag.Duration("quit", 0, "quit by itself after this long, for smoke tests (0: stay)")
	flag.Parse()
	if err := run(*dir, *listen, *dry, *hidden, *quit); err != nil {
		fmt.Fprintln(os.Stderr, "techo5-streamdeck:", err)
		os.Exit(1)
	}
}

func run(dir, listen string, dry, hidden bool, quit time.Duration) error {
	ui, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	c, err := core.New(core.Options{Dir: dir, Listen: listen, DryRun: dry})
	if err != nil {
		return err
	}
	defer c.Close()
	// A busy port must not stop the app from opening: the tray and the editor are how the user
	// fixes the listen address, so report it and carry on paused.
	if err := c.Start(); err != nil {
		slog.Error("the deck server could not start", "err", err)
	}

	// The hotkeys are registered with the system once the app runs (Wails binds them then), and
	// kept in step with the config after that. press finds the button in the config as it is when
	// the key is hit.
	var hot *hotkeys.Manager
	press := func(accel string) {
		if err := c.Trigger(context.Background(), accel); err != nil {
			slog.Warn("hotkey", "hotkey", accel, "err", err)
		}
	}
	editor := &Editor{core: c}

	app := application.New(application.Options{
		Name:     "TECHO5 Stream Deck",
		Services: []application.Service{application.NewService(editor)},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(ui)},
		Mac: application.MacOptions{
			// A tray app: no Dock icon.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	hot = hotkeys.New(app.GlobalShortcut, press, nil)
	editor.hot = hot
	defer hot.Close()

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "TECHO5 Stream Deck",
		Width:  1280,
		Height: 800,
		Hidden: hidden,
		URL:    "/",
	})
	// Closing the editor must not end the deck: hide to the tray instead.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		win.Hide()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
	}
	tray.OnClick(func() { showEditor(win) })

	menu := app.NewMenu()
	menu.Add("Open editor").OnClick(func(*application.Context) { showEditor(win) })
	pause := menu.AddCheckbox("Pause deck", !c.Running())
	pause.OnClick(func(*application.Context) {
		if pause.Checked() {
			c.Pause()
			return
		}
		if err := c.Start(); err != nil {
			slog.Error("the deck server could not start", "err", err)
			pause.SetChecked(true)
		}
	})
	login := menu.AddCheckbox("Start at login", false)
	login.OnClick(func(*application.Context) {
		var err error
		if login.Checked() {
			// --hidden: at login the deck should serve, not pop a window open.
			err = app.Autostart.EnableWithOptions(application.AutostartOptions{Arguments: loginArgs(dir, listen, dry)})
		} else {
			err = app.Autostart.Disable()
		}
		if err != nil {
			slog.Error("start at login", "err", err)
			on, _ := app.Autostart.IsEnabled()
			login.SetChecked(on)
		}
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if on, err := app.Autostart.IsEnabled(); err == nil {
			login.SetChecked(on)
		}
		// Off this goroutine: registering asks the main thread, which may be the one running us.
		go c.OnReload(func(cfg *deck.Config) { hot.Sync(cfg.Hotkeys) })
		slog.Info("started", "config", c.Store.Path(), "listening", c.Running())
		if quit > 0 {
			time.AfterFunc(quit, app.Quit)
		}
	})

	if err := app.Run(); err != nil {
		log.Print(err)
		return err
	}
	return nil
}

// trigger is "techo5-streamdeck trigger [-dir d] <id>": it presses a button of the running app. The
// id is a hotkey of the config, "Alt+1", or "profile/page/col,row". It returns the exit status.
func trigger(args []string) int {
	fs := flag.NewFlagSet("trigger", flag.ContinueOnError)
	dir := fs.String("dir", "", "the config folder of the running app (default: techo5-streamdeck in the user config folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: techo5-streamdeck trigger [-dir folder] <hotkey | profile/page/col,row>")
		return 2
	}
	d := *dir
	if d == "" {
		var err error
		if d, err = store.Dir(); err != nil {
			fmt.Fprintln(os.Stderr, "techo5-streamdeck:", err)
			return 1
		}
	}
	if err := control.Send(d, strings.Join(fs.Args(), " ")); err != nil {
		fmt.Fprintln(os.Stderr, "techo5-streamdeck:", err)
		return 1
	}
	return 0
}

func showEditor(win *application.WebviewWindow) {
	win.Show()
	win.Focus()
}

// loginArgs are the flags the login entry starts the app with: the same choices as now, hidden.
func loginArgs(dir, listen string, dry bool) []string {
	args := []string{"-hidden"}
	if dir != "" {
		args = append(args, "-dir", dir)
	}
	if listen != "" {
		args = append(args, "-listen", listen)
	}
	if dry {
		args = append(args, "-dry-run")
	}
	return args
}
