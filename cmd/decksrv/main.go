// Command decksrv is the deck server without the desktop app, for debugging: it serves the deck
// in config.json to every Show that connects, and reloads the file when it is edited.
//
// With -dry-run the actions are logged and not performed, to try a deck out without it opening
// anything or pressing keys.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/asmsaifs/techo5-streamdeck/internal/actions"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/server"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

func main() {
	dir := flag.String("dir", "", "the config folder (default: techo5-streamdeck in the user config folder)")
	listen := flag.String("listen", "", "address to listen on (default: the config's server.listen)")
	dry := flag.Bool("dry-run", false, "log what actions would do instead of doing it")
	flag.Parse()
	if err := run(*dir, *listen, *dry); err != nil {
		fmt.Fprintln(os.Stderr, "decksrv:", err)
		os.Exit(1)
	}
}

func run(dir, listen string, dry bool) error {
	if dir == "" {
		d, err := store.Dir()
		if err != nil {
			return err
		}
		dir = d
	}
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	cfg := st.Config()
	if listen == "" {
		listen = cfg.Server.Listen
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	fmt.Println("config:", st.Path())
	fmt.Println("On the Show: Dashboard server =", lanIP()+":"+port)
	fmt.Println("              key              =", cfg.Server.Key)
	fmt.Println("              Dashboard        = Streamed, then swipe in from the left edge")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var sys actions.System = actions.OS()
	if dry {
		sys = actions.DryRun(slog.Default())
		fmt.Println("dry run: actions are logged, not performed")
	}
	srv := &server.Server{Config: st.Config, Runner: actions.New(sys, nil), Renderer: render.New(filepath.Join(dir, store.IconsDir))}
	err = st.Watch(ctx, func(c *deck.Config, err error) {
		if err != nil {
			slog.Warn("config.json is broken; keeping the last good one", "err", err)
			return
		}
		slog.Info("config.json reloaded")
		srv.Reload()
	})
	if err != nil {
		return err
	}
	return srv.Serve(ctx, ln)
}

// lanIP is this computer's address on the LAN, as a guess for the instructions: the source address
// of a route out. Nothing is sent.
func lanIP() string {
	c, err := net.Dial("udp", "192.0.2.1:9") // TEST-NET-1: never reached, only routed
	if err != nil {
		return "<this computer's IP>"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
