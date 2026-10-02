// Package update is the app's check for a newer release. Releases carry a checksums.txt and a
// detached ed25519 signature of it (checksums.txt.sig), the same idea as the techo5 updater: the
// app believes a version only when the release key signed the file that names it. The check only
// tells the user a download exists; nothing is installed from here.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ReleaseKey is the public half of the key releases are signed with (cmd/relsign), base64.
var ReleaseKey = "SEPS3gXIpQE6CoYc1Hv56jt6jMs+P6OLoCBcKiGLEVY="

// LatestURL is where the newest release's files are served from.
const LatestURL = "https://github.com/asmsaifs/techo5-streamdeck/releases/latest/download"

// PageURL is the release page a user is sent to for the download.
const PageURL = "https://github.com/asmsaifs/techo5-streamdeck/releases/latest"

// Release is a verified release.
type Release struct {
	Version string            // "v1.2.3"
	Files   map[string]string // file name -> lower-case hex sha256
}

// Parse reads a checksums file: the first line is "techo5-streamdeck vX.Y.Z", the rest
// "<sha256>  <file>" as sha256sum prints them.
func Parse(b []byte) (Release, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	if !sc.Scan() {
		return Release{}, errors.New("update: empty checksums file")
	}
	name, ver, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
	if !ok || name != "techo5-streamdeck" {
		return Release{}, errors.New("update: the checksums file does not name a version")
	}
	if _, ok := parseVersion(ver); !ok {
		return Release{}, fmt.Errorf("update: %q is not a version", ver)
	}
	r := Release{Version: ver, Files: map[string]string{}}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sum, file, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 {
			return Release{}, fmt.Errorf("update: bad checksum line %q", line)
		}
		r.Files[strings.TrimPrefix(file, "*")] = strings.ToLower(sum)
	}
	return r, sc.Err()
}

// Verify checks sig (base64 of an ed25519 signature) over the checksums file against key.
func Verify(checksums, sig []byte, key string) error {
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("update: no usable release key in this build")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("update: the signature is malformed")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), checksums, raw) {
		return errors.New("update: the checksums are not signed by the release key")
	}
	return nil
}

// Sign returns the signature file's contents for checksums, from a key file's contents (base64
// of the 32-byte seed).
func Sign(checksums []byte, seed string) (string, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(s) != ed25519.SeedSize {
		return "", errors.New("update: the signing key is not a base64 ed25519 seed")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(s), checksums)) + "\n", nil
}

// Newer reports whether version a is newer than b. A version that does not parse (a development
// build) is never older than anything, so nobody is nagged in a build from source.
func Newer(a, b string) bool {
	va, ok := parseVersion(a)
	if !ok {
		return false
	}
	vb, ok := parseVersion(b)
	if !ok {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// parseVersion reads "v1.2.3" (a "-rc.1" style suffix is ignored).
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// Check fetches and verifies the latest release from base and returns it with whether it is newer
// than current.
func Check(ctx context.Context, client *http.Client, base, key, current string) (Release, bool, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	sums, err := get(ctx, client, base+"/checksums.txt")
	if err != nil {
		return Release{}, false, err
	}
	sig, err := get(ctx, client, base+"/checksums.txt.sig")
	if err != nil {
		return Release{}, false, err
	}
	if err := Verify(sums, sig, key); err != nil {
		return Release{}, false, err
	}
	r, err := Parse(sums)
	if err != nil {
		return Release{}, false, err
	}
	return r, Newer(r.Version, current), nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
