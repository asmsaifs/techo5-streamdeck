package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/asmsaifs/techo5-streamdeck/internal/core"
	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

// Settings is what the settings panel shows: where the deck listens, the key a Show needs, and
// what to type on the Show.
type Settings struct {
	Listen  string // as configured, "0.0.0.0:9555"
	Key     string
	Running bool
	Version string // the release this build was stamped with, "dev" from source
	Address string // for the Show's "Dashboard server" field: this computer's LAN IP and the port
}

// Settings reads them. The key goes to the editor window only: it is never logged.
func (e *Editor) Settings() Settings {
	c := e.core.Store.Config()
	_, port, err := net.SplitHostPort(c.Server.Listen)
	if a := e.core.Addr(); a != nil {
		_, port, err = net.SplitHostPort(a.String())
	}
	if err != nil {
		port = strconv.Itoa(9555)
	}
	return Settings{Listen: c.Server.Listen, Key: c.Server.Key, Running: e.core.Running(), Version: version,
		Address: net.JoinHostPort(core.LANIP(), port)}
}

// cloneConfig is a copy of the saved config to change and save.
func (e *Editor) cloneConfig() (*deck.Config, error) {
	b, err := store.Encode(e.core.Store.Config())
	if err != nil {
		return nil, err
	}
	return store.Parse(b)
}

// SetListen moves the deck to another address and saves it. A bad or busy address is refused with
// the deck still running where it was.
func (e *Editor) SetListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not an address like 0.0.0.0:9555", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q is not a port from 1 to 65535", port)
	}
	if host != "" && net.ParseIP(host) == nil {
		return fmt.Errorf("%q is not an IP address; use 0.0.0.0 for all", host)
	}
	c, err := e.cloneConfig()
	if err != nil {
		return err
	}
	c.Server.Listen = addr
	if err := e.core.Rebind(addr); err != nil {
		return err
	}
	return e.core.Store.Save(c)
}

// RegenerateKey makes a new key, saves it and drops the connected Shows: they will have to be
// given the new one. It returns the key.
func (e *Editor) RegenerateKey() (string, error) {
	c, err := e.cloneConfig()
	if err != nil {
		return "", err
	}
	c.Server.Key = wire.NewKey()
	if err := e.core.Store.Save(c); err != nil {
		return "", err
	}
	if e.core.Running() {
		e.core.Pause()
		if err := e.core.Start(); err != nil {
			return c.Server.Key, errors.New("the new key is saved, but the deck could not start again: " + err.Error())
		}
	}
	return c.Server.Key, nil
}
