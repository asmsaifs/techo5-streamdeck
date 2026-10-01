package web

import "testing"

func TestAllow(t *testing.T) {
	tests := []struct {
		name  string
		start string
		extra []string
		url   string
		want  bool
	}{
		{"same host", "https://youtube.com", nil, "https://youtube.com/watch?v=1", true},
		{"subdomain of the registrable domain", "https://www.youtube.com/tv", nil, "https://m.youtube.com/", true},
		{"start on a subdomain still allows the domain", "https://music.youtube.com", nil, "https://www.youtube.com/", true},
		{"look-alike suffix", "https://youtube.com", nil, "https://evilyoutube.com/", false},
		{"look-alike prefix", "https://youtube.com", nil, "https://youtube.com.evil.example/", false},
		{"userinfo trick", "https://youtube.com", nil, "https://youtube.com@evil.example/", false},
		{"other site", "https://youtube.com", nil, "https://google.com/", false},
		{"extra domain", "https://youtube.com", []string{"google.com"}, "https://accounts.google.com/signin", true},
		{"extra given as an address", "https://youtube.com", []string{"https://accounts.google.com/x"}, "https://accounts.google.com/", true},
		{"extra with wildcard", "https://youtube.com", []string{"*.google.com"}, "https://accounts.google.com/", true},
		{"extra does not widen to its parent", "https://youtube.com", []string{"accounts.google.com"}, "https://www.google.com/", false},
		{"multi-part suffix", "https://www.bbc.co.uk", nil, "https://news.bbc.co.uk/", true},
		{"multi-part suffix is not all of co.uk", "https://www.bbc.co.uk", nil, "https://example.co.uk/", false},
		{"ip address", "http://192.168.1.5:8123", nil, "http://192.168.1.5:8123/lovelace", true},
		{"other ip", "http://192.168.1.5:8123", nil, "http://192.168.1.6/", false},
		{"localhost", "http://localhost:3000", nil, "http://localhost:3000/a", true},
		{"file scheme", "https://youtube.com", nil, "file:///etc/passwd", false},
		{"javascript scheme", "https://youtube.com", nil, "javascript:alert(1)", false},
		{"data scheme", "https://youtube.com", nil, "data:text/html,hi", false},
		{"blank page", "https://youtube.com", nil, "about:blank", true},
		{"trailing dot", "https://youtube.com", nil, "https://youtube.com./", true},
		{"upper case", "https://YouTube.com", nil, "https://WWW.YOUTUBE.COM/", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewAllow(tt.start, tt.extra)
			if err != nil {
				t.Fatal(err)
			}
			if got := a.Permits(tt.url); got != tt.want {
				t.Errorf("Permits(%q) = %v, want %v (allowed %v)", tt.url, got, tt.want, a.Domains())
			}
		})
	}
}

func TestNewAllowRefuses(t *testing.T) {
	tests := []struct {
		name  string
		start string
		extra []string
	}{
		{"no host", "https://", nil},
		{"not http", "ftp://example.com", nil},
		{"a whole top-level domain", "https://example.com", []string{"com"}},
		{"a whole multi-part suffix", "https://example.com", []string{"co.uk"}},
		{"spaces", "https://example.com", []string{"a b.com"}},
		{"a port", "https://example.com", []string{"example.org:8080"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if a, err := NewAllow(tt.start, tt.extra); err == nil {
				t.Errorf("accepted: %v", a.Domains())
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://example.com": true, "http://192.168.1.2:8123/x": true,
		"": false, "example.com": false, "ftp://example.com": false, "https://": false, "javascript:alert(1)": false,
	} {
		if err := ValidateURL(raw); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
}
