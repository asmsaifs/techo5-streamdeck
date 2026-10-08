package sendspin

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

// Service is what a Sendspin player advertises: the Show on port 8928, Music Assistant's own
// players, other speakers.
const Service = "_sendspin._tcp"

// Player is one found on the network.
type Player struct {
	Name string // as it advertises itself, the name= of its TXT record
	URL  string // "ws://192.168.1.181:8928/sendspin"
}

// Browse lists the Sendspin players that answer within wait, sorted by name. Everything on the
// network that plays Sendspin answers, not only Shows; the user picks.
func Browse(ctx context.Context, wait time.Duration) ([]Player, error) {
	return browse(ctx, wait, "")
}

// browse is Browse that stops as soon as the player called want answers, if want is not empty.
func browse(ctx context.Context, wait time.Duration, want string) ([]Player, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 16)
	found := map[string]Player{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			if p, ok := player(e); ok {
				found[p.Name] = p
				if p.Name == want {
					cancel()
				}
			}
		}
	}()
	err := zeroconf.Browse(ctx, Service, "local.", entries)
	<-done
	if err != nil && (want == "" || found[want].URL == "") {
		return nil, fmt.Errorf("looking for Sendspin players: %w", err)
	}
	out := make([]Player, 0, len(found))
	for _, p := range found {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Player) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// player reads an entry. An IPv4 address is preferred: it is what a Show listens on.
func player(e *zeroconf.ServiceEntry) (Player, bool) {
	var ip net.IP
	switch {
	case len(e.AddrIPv4) > 0:
		ip = e.AddrIPv4[0]
	case len(e.AddrIPv6) > 0:
		ip = e.AddrIPv6[0]
	default:
		return Player{}, false
	}
	name, path := e.Instance, "/sendspin"
	for _, t := range e.Text {
		k, v, _ := strings.Cut(t, "=")
		switch k {
		case "name":
			if v != "" {
				name = v
			}
		case "path":
			if strings.HasPrefix(v, "/") {
				path = v
			}
		}
	}
	return Player{Name: name, URL: "ws://" + net.JoinHostPort(ip.String(), strconv.Itoa(e.Port)) + path}, true
}

// Finder resolves a Show's name to its address, remembering what it found for a while so that
// the sound starting does not wait on a browse each time.
type Finder struct {
	// Browse stands in for the network in tests: it may stop early once want has answered.
	// The network if nil.
	Browse func(ctx context.Context, wait time.Duration, want string) ([]Player, error)

	mu    sync.Mutex
	known map[string]Player
	at    time.Time
}

// keepFor is how long an address found is trusted. A Show's address rarely changes, and a dial
// that fails is followed by a fresh look anyway.
const keepFor = 10 * time.Minute

// Resolve returns the URL of the Show called name. It browses when it has not seen the Show, or
// not lately.
func (f *Finder) Resolve(ctx context.Context, name string) (string, error) {
	f.mu.Lock()
	p, ok := f.known[name]
	fresh := time.Since(f.at) < keepFor
	f.mu.Unlock()
	if ok && fresh {
		return p.URL, nil
	}
	// The look stops as soon as the Show answers, which is at once for one on the network: the
	// sound waits on this before the Show is dialed.
	list, err := f.look(ctx, 2*time.Second, name)
	if err != nil {
		return "", err
	}
	for _, p := range list {
		if p.Name == name {
			return p.URL, nil
		}
	}
	return "", fmt.Errorf("no Sendspin player called %q answered on the network", name)
}

// Forget drops what is known of a Show, so the next Resolve looks again.
func (f *Finder) Forget(name string) {
	f.mu.Lock()
	delete(f.known, name)
	f.mu.Unlock()
}

// Look browses now and remembers what answered.
func (f *Finder) Look(ctx context.Context, wait time.Duration) ([]Player, error) {
	return f.look(ctx, wait, "")
}

func (f *Finder) look(ctx context.Context, wait time.Duration, want string) ([]Player, error) {
	b := f.Browse
	if b == nil {
		b = browse
	}
	list, err := b(ctx, wait, want)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if want == "" || f.known == nil {
		// A whole look replaces what was known; one cut short only adds to it.
		f.known = map[string]Player{}
	}
	for _, p := range list {
		f.known[p.Name] = p
	}
	f.at = time.Now()
	f.mu.Unlock()
	return list, nil
}
