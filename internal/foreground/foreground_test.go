package foreground

import "testing"

func TestParseLsappinfo(t *testing.T) {
	out := "\"LSDisplayName\"=\"‎WhatsApp\"\n\"CFBundleIdentifier\"=\"net.whatsapp.WhatsApp\"\n"
	a, ok := parseLsappinfo(out)
	if !ok || a.Name != "WhatsApp" || a.ID != "net.whatsapp.WhatsApp" {
		t.Errorf("got %+v %v", a, ok)
	}
	if _, ok := parseLsappinfo("garbage"); ok {
		t.Error("garbage parsed")
	}
}

func TestParseXprop(t *testing.T) {
	for in, want := range map[string]string{
		"_NET_ACTIVE_WINDOW(WINDOW): window id # 0x4a00007\n": "0x4a00007",
		"_NET_ACTIVE_WINDOW(WINDOW): window id # 0x0\n":       "",
		"_NET_ACTIVE_WINDOW:  not found.":                     "",
	} {
		if got, ok := parseActiveWindow(in); got != want || ok != (want != "") {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	a, ok := parseWMClass(`WM_CLASS(STRING) = "navigator", "firefox"` + "\n")
	if !ok || a.Name != "firefox" || a.ID != "navigator" {
		t.Errorf("WM_CLASS: %+v", a)
	}
	if a, ok := parseWMClass(`WM_CLASS(STRING) = "xterm"`); !ok || a.Name != "xterm" {
		t.Errorf("one-part WM_CLASS: %+v", a)
	}
	if _, ok := parseWMClass("WM_CLASS:  not found."); ok {
		t.Error("not found parsed")
	}
}

func TestMatch(t *testing.T) {
	tests := []struct {
		rule  string
		names []string
		want  bool
	}{
		{"Safari", []string{"Safari", "com.apple.Safari"}, true},
		{"safari", []string{"Safari"}, true},
		{" com.apple.safari ", []string{"Safari", "com.apple.Safari"}, true},
		{"chrome", []string{"chrome.exe"}, true},
		{"chrome.exe", []string{"Chrome"}, true},
		{"Safari", []string{"Safari Technology Preview"}, false},
		{"", []string{""}, false},
		{"Safari", nil, false},
	}
	for _, tt := range tests {
		if got := Match(tt.rule, tt.names...); got != tt.want {
			t.Errorf("Match(%q, %v) = %v", tt.rule, tt.names, got)
		}
	}
}

func TestNamesDropEmpty(t *testing.T) {
	if n := (App{Name: " ‎", ID: "x"}).Names(); len(n) != 1 || n[0] != "x" {
		t.Errorf("Names = %q", n)
	}
}
