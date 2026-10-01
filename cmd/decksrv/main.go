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
	"net"
	"os"
	"os/signal"

	"github.com/asmsaifs/techo5-streamdeck/internal/core"
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
	c, err := core.New(core.Options{Dir: dir, Listen: listen, DryRun: dry})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Start(); err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(c.Addr().String())
	fmt.Println("config:", c.Store.Path())
	fmt.Println("On the Show: Dashboard server =", core.LANIP()+":"+port)
	fmt.Println("              key              =", c.Store.Config().Server.Key)
	fmt.Println("              Dashboard        = Streamed, then swipe in from the left edge")
	if dry {
		fmt.Println("dry run: actions are logged, not performed")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	return nil
}
