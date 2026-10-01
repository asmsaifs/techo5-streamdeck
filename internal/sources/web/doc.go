// Package web is a website in headless Chrome, ported from dashcast: the page is streamed to the
// Show as pictures and the Show's touches are replayed on it.
//
// Where dashcast served one Home Assistant, this shows any site. What keeps a Show from wandering
// off to arbitrary pages is a per-tile domain allowlist (allow.go), not a list of panels.
package web
