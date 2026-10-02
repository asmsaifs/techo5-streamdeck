package update

import "testing"

func TestBundleOf(t *testing.T) {
	for _, c := range []struct {
		exe, want string
		ok        bool
	}{
		{"/Applications/TECHO5 Stream Deck.app/Contents/MacOS/techo5-streamdeck", "/Applications/TECHO5 Stream Deck.app", true},
		{"/Users/u/go/bin/techo5-streamdeck", "", false}, // run from source
		{"/x/Foo.app/Contents/Resources/prog", "", false},
	} {
		got, ok := bundleOf(c.exe)
		if got != c.want || ok != c.ok {
			t.Errorf("bundleOf(%q) = %q, %v", c.exe, got, ok)
		}
	}
}
