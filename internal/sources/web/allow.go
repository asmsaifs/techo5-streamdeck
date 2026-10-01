package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Allow is the sites a tile may be on. The Show's finger drives a browser that may be signed in to
// the user's accounts, so a link on the page must not be able to take it anywhere at all: the
// main page may only be at the tile's own domain, and at the ones the tile names beside it.
type Allow struct {
	domains []string
}

// NewAllow allows the registrable domain of start (youtube.com for https://www.youtube.com/tv)
// and each of extra. A domain allows itself and everything under it. An address that has no
// registrable domain, an IP address or localhost, is allowed as itself.
func NewAllow(start string, extra []string) (*Allow, error) {
	u, err := parseWeb(start)
	if err != nil {
		return nil, err
	}
	a := &Allow{domains: []string{registrable(u.Hostname())}}
	for _, e := range extra {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		e = strings.TrimPrefix(e, "*.")
		// An address with a scheme or a path is a natural thing to paste; the host is what counts.
		if strings.Contains(e, "/") {
			eu, err := parseWeb(e)
			if err != nil {
				return nil, fmt.Errorf("allowed site %q: %w", e, err)
			}
			e = eu.Hostname()
		}
		if e == "" || strings.ContainsAny(e, " \t:*") {
			return nil, fmt.Errorf("allowed site %q is not a domain name", e)
		}
		// A bare "com" would allow every .com there is.
		if suffix, _ := publicsuffix.PublicSuffix(e); e == suffix {
			return nil, fmt.Errorf("allowed site %q is a whole top-level domain", e)
		}
		a.domains = append(a.domains, e)
	}
	return a, nil
}

// Domains lists what is allowed, for the log.
func (a *Allow) Domains() []string { return append([]string(nil), a.domains...) }

// Permits is whether the main page may be at raw: an http or https address on an allowed
// domain, or the blank page a tab starts on.
func (a *Allow) Permits(raw string) bool {
	if raw == "about:blank" {
		return true
	}
	u, err := parseWeb(raw)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, d := range a.domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// parseWeb reads an http or https address that has a host.
func parseWeb(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%q is not an http:// or https:// address", raw)
	}
	if u.Hostname() == "" {
		return nil, errors.New("the address has no host")
	}
	return u, nil
}

// registrable is the domain a host's owner registered: example.com for www.example.com.
func registrable(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if net.ParseIP(host) != nil {
		return host
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// ValidateURL checks a tile's address without opening it, for the config's validation.
func ValidateURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("is missing")
	}
	if !strings.Contains(raw, "://") {
		return errors.New("needs a scheme, like https://example.com")
	}
	_, err := parseWeb(raw)
	return err
}

// guardScript runs in every page before its own code. A tap on a link to a site that is not
// allowed does nothing, rather than load Chrome's error page over the page the Show is looking
// at. It is a courtesy and not the guard: scripts, forms and redirects get past it, and the
// browser's own check (Source.onRequest) is what keeps the page on its sites.
func (a *Allow) guardScript() string {
	list, _ := json.Marshal(a.domains)
	return `(() => {
  const allowed = ` + string(list) + `;
  const ok = (h) => { h = h.toLowerCase().replace(/\.$/, ""); return allowed.some((d) => h === d || h.endsWith("." + d)); };
  addEventListener("click", (e) => {
    const a = e.target && e.target.closest ? e.target.closest("a[href]") : null;
    if (!a) return;
    let u;
    try { u = new URL(a.href, location.href); } catch (err) { return; }
    if ((u.protocol === "http:" || u.protocol === "https:") && !ok(u.hostname)) { e.preventDefault(); e.stopImmediatePropagation(); }
  }, true);
})();`
}
