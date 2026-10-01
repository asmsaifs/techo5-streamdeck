// Package control is the way another process on this computer presses a button of the running
// app: "techo5-streamdeck trigger <id>". A desktop that cannot give the app a global hotkey
// (Wayland without the portal) can call that from its own shortcut settings, and scripts can too.
//
// It listens on a Unix socket in the config folder, which is private to the user (0700) and which
// the socket's own mode keeps that way. The only thing it understands is "trigger <id>", where
// the id names a button the config already has: like the Show, a caller can press configured
// buttons and nothing else.
package control

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SocketName is the socket's name in the config folder.
const SocketName = "control.sock"

// Path is the socket of the app whose config folder is dir.
func Path(dir string) string { return filepath.Join(dir, SocketName) }

// Handler presses the button id names and reports how it went.
type Handler func(ctx context.Context, id string) error

// ErrRunning is returned by Listen when another app already owns the socket.
var ErrRunning = errors.New("another instance is already listening")

// Listen opens the socket in dir and serves it until ctx ends. A socket left behind by an app
// that died is replaced; one that still answers is not.
func Listen(ctx context.Context, dir string, h Handler) error {
	path := Path(dir)
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return ErrRunning
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
		_ = os.Remove(path)
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(ctx, c, h)
		}
	}()
	return nil
}

func serve(ctx context.Context, c net.Conn, h Handler) {
	defer c.Close()
	// A caller that connects and says nothing must not hold a goroutine for ever.
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(c, 4096)).ReadString('\n')
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	verb, id, _ := strings.Cut(strings.TrimSpace(line), " ")
	if verb != "trigger" || strings.TrimSpace(id) == "" {
		fmt.Fprintln(c, "error: want: trigger <id>")
		return
	}
	if err := h(ctx, strings.TrimSpace(id)); err != nil {
		fmt.Fprintln(c, "error:", oneLine(err.Error()))
		return
	}
	fmt.Fprintln(c, "ok")
}

// Send asks the app running with config folder dir to press the button id names, and returns what
// went wrong in its words. It waits for the action to finish, as the Show's button does.
func Send(dir, id string) error {
	c, err := net.DialTimeout("unix", Path(dir), 2*time.Second)
	if err != nil {
		return fmt.Errorf("the app is not running (nothing answers on %s)", Path(dir))
	}
	defer c.Close()
	if _, err := fmt.Fprintf(c, "trigger %s\n", oneLine(id)); err != nil {
		return err
	}
	reply, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return fmt.Errorf("the app closed the connection without an answer")
	}
	reply = strings.TrimSpace(reply)
	if reply == "ok" {
		return nil
	}
	return errors.New(strings.TrimSpace(strings.TrimPrefix(reply, "error:")))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
