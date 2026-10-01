package discovery

import (
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/libp2p/zeroconf/v2"
)

// Service is the name the Show browses for.
const Service = "_techo5deck._tcp"

// Advertiser announces the deck server until Stop. Finding a deck only saves typing its address on
// the Show: the key is still entered there by hand, so nothing heard on the network is trusted.
type Advertiser struct {
	srv *zeroconf.Server
}

// Advertise announces a server listening on addr as this computer's deck. A server bound to a
// loopback address is not announced, since no Show could reach it.
func Advertise(addr net.Addr) (*Advertiser, error) {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return nil, nil
	}
	if tcp.IP.IsLoopback() {
		return nil, nil
	}
	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	if name == "" {
		name = "deck"
	}
	srv, err := zeroconf.Register(name, Service, "local.", tcp.Port, txt(name), nil)
	if err != nil {
		return nil, err
	}
	slog.Info("deck announced on the network", "name", name, "port", tcp.Port)
	return &Advertiser{srv: srv}, nil
}

// txt is the announcement's TXT record: the computer's name for the Show's list, and the version of
// this record's layout.
func txt(name string) []string { return []string{"name=" + name, "v=1"} }

// Stop withdraws the announcement.
func (a *Advertiser) Stop() {
	if a != nil && a.srv != nil {
		a.srv.Shutdown()
	}
}
