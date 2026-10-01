// Spike 0.5.1: Wails v3 as the desktop shell. It checks, on this OS: a tray icon with a menu, a
// window that hides to the tray when closed, start-at-login, and global hotkeys two ways - v3's
// own app.GlobalShortcut and golang.design/x/hotkey registered on the main thread - both inside
// Wails' main loop.
//
//	go run . -quit 20s
package main

import (
	"flag"
	"log"
	"runtime"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
	"golang.design/x/hotkey"
)

func main() {
	quit := flag.Duration("quit", 0, "quit by itself after this long (0: stay)")
	flag.Parse()

	app := application.New(application.Options{
		Name: "deck spike",
		Mac: application.MacOptions{
			// A tray app: no Dock icon.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "deck spike", Width: 640, Height: 400,
		HTML: `<h2>deck spike</h2><p>Close me: I should hide to the tray, not quit.</p>`,
	})
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		log.Print("window closing: hidden to the tray")
		win.Hide()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
	}
	menu := app.NewMenu()
	menu.Add("Open editor").OnClick(func(*application.Context) { win.Show(); win.Focus() })
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	if err := app.GlobalShortcut.Register("CmdOrCtrl+Alt+1", func() {
		log.Print("hotkey fired: v3 GlobalShortcut CmdOrCtrl+Alt+1")
	}); err != nil {
		log.Print("v3 GlobalShortcut: ", err)
	}

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if st, err := app.Autostart.Status(); err != nil {
			log.Print("autostart status: ", err)
		} else {
			log.Printf("autostart: enabled %v, strategy %s, path %s", st.Enabled, st.Strategy, st.Path)
		}
		// golang.design/x/hotkey wants macOS's main thread to register; in Wails that thread is
		// Wails' own, reached with InvokeSync.
		var hk *hotkey.Hotkey
		application.InvokeSync(func() {
			hk = hotkey.New([]hotkey.Modifier{hotkey.ModCmd, hotkey.ModOption}, hotkey.Key2)
			if err := hk.Register(); err != nil {
				log.Print("x/hotkey register: ", err)
				hk = nil
			}
		})
		if hk != nil {
			go func() {
				for range hk.Keydown() {
					log.Print("hotkey fired: x/hotkey Cmd+Alt+2")
				}
			}()
		}
		log.Print("started")
		if *quit > 0 {
			time.AfterFunc(*quit, app.Quit)
		}
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
