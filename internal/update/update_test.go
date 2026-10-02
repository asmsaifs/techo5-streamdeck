package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sums = "techo5-streamdeck v1.2.0\n" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  app.dmg\n"

func testKey() (seed, pub string) {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = byte(i)
	}
	k := ed25519.NewKeyFromSeed(s)
	return base64.StdEncoding.EncodeToString(s), base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.10.0", "v1.9.0", true},
		{"v1.0.0", "v1.0.1", false},
		{"v1.0.0", "dev", false}, // a build from source is never nagged
		{"junk", "v1.0.0", false},
		{"v1.0.1", "v1.0.0-rc.1", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	r, err := Parse([]byte(sums))
	if err != nil || r.Version != "v1.2.0" || len(r.Files["app.dmg"]) != 64 {
		t.Fatalf("Parse = %+v, %v", r, err)
	}
	for _, bad := range []string{"", "other v1.0.0\n", "techo5-streamdeck 1\n", "techo5-streamdeck v1.0.0\nshort  f\n"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestCheck(t *testing.T) {
	seed, pub := testKey()
	sig, err := Sign([]byte(sums), seed)
	if err != nil {
		t.Fatal(err)
	}
	served := sums
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			w.Write([]byte(served))
		case strings.HasSuffix(r.URL.Path, "/checksums.txt.sig"):
			w.Write([]byte(sig))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r, newer, err := Check(context.Background(), srv.Client(), srv.URL, pub, "v1.1.0")
	if err != nil || !newer || r.Version != "v1.2.0" {
		t.Fatalf("Check = %+v %v %v", r, newer, err)
	}
	if _, newer, err = Check(context.Background(), srv.Client(), srv.URL, pub, "v1.2.0"); err != nil || newer {
		t.Fatalf("current version: newer=%v err=%v", newer, err)
	}
	// A file that was changed after signing, or a different key, must be refused.
	served = strings.Replace(sums, "v1.2.0", "v9.9.9", 1)
	if _, _, err = Check(context.Background(), srv.Client(), srv.URL, pub, "v1.1.0"); err == nil {
		t.Fatal("tampered checksums accepted")
	}
	served = sums
	_, otherPub := func() (string, string) {
		k := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
		return "", base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
	}()
	if _, _, err = Check(context.Background(), srv.Client(), srv.URL, otherPub, "v1.1.0"); err == nil {
		t.Fatal("wrong key accepted")
	}
}
