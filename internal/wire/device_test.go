package wire

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"testing"

	"github.com/flynn/noise"
)

// What follows, down to deviceRead, is the device's side as techo5/echod/internal/feature/dashboard
// has it, copied rather than imported (it is in another module, under internal/). It is the check
// that this server still talks to a real Show: if secure.go drifts from it, these tests fail. Keep
// it in step with the device's secure.go and stream.go when those change.

const (
	devicePrologue  = "techo5-dashcast/1"
	deviceRecordMax = 60000
	deviceWireMax   = deviceRecordMax + 64
	deviceMsgMax    = 4 << 20
)

func devicePSK(key string) []byte {
	sum := sha256.Sum256([]byte("techo5-dashcast psk:" + key))
	return sum[:]
}

func deviceReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > deviceWireMax {
		return nil, fmt.Errorf("secure: a record of %d bytes", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

type deviceConn struct {
	net.Conn
	send, recv *noise.CipherState
	rbuf       []byte
}

func deviceHandshake(c net.Conn, key string) (*deviceConn, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Random:      rand.Reader, Pattern: noise.HandshakeNN, Initiator: true,
		Prologue: []byte(devicePrologue), PresharedKey: devicePSK(key), PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, err
	}
	first, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, first); err != nil {
		return nil, err
	}
	reply, err := deviceReadFrame(c)
	if err != nil {
		return nil, err
	}
	_, toServer, fromServer, err := hs.ReadMessage(nil, reply)
	if err != nil {
		return nil, err
	}
	return &deviceConn{Conn: c, send: toServer, recv: fromServer}, nil
}

func (s *deviceConn) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		n := min(len(p), deviceRecordMax)
		ct, err := s.send.Encrypt(nil, nil, p[:n])
		if err != nil {
			return total - len(p), err
		}
		if err := writeFrame(s.Conn, ct); err != nil {
			return total - len(p), err
		}
		p = p[n:]
	}
	return total, nil
}

func (s *deviceConn) Read(p []byte) (int, error) {
	for len(s.rbuf) == 0 {
		ct, err := deviceReadFrame(s.Conn)
		if err != nil {
			return 0, err
		}
		pt, err := s.recv.Decrypt(nil, nil, ct)
		if err != nil {
			return 0, errors.New("secure: a record that does not check out")
		}
		s.rbuf = pt
	}
	n := copy(p, s.rbuf)
	s.rbuf = s.rbuf[n:]
	return n, nil
}

// deviceRead is the device's message loop, one message of it: stream.go's once.
func deviceRead(r io.Reader) (kind byte, at image.Point, body []byte, err error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, at, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > deviceMsgMax {
		return 0, at, nil, errors.New("dashboard stream: a message of an impossible size")
	}
	msg := make([]byte, n)
	if _, err := io.ReadFull(r, msg); err != nil {
		return 0, at, nil, err
	}
	switch msg[0] {
	case KindPicture, KindHalf:
		at = image.Pt(int(binary.BigEndian.Uint16(msg[1:3])), int(binary.BigEndian.Uint16(msg[3:5])))
		return msg[0], at, msg[5:], nil
	}
	return msg[0], at, msg[1:], nil
}

// The psk is pinned to known bytes as well, so it cannot change by both copies changing at once.
func TestThePSKIsTheDevices(t *testing.T) {
	got := fmt.Sprintf("%x", psk("the key"))
	// sha256("techo5-dashcast psk:the key")
	const want = "a79013a1c543bd9a9d0b0511ae64612d025244a77dc341f13231fe515dfc1b6c"
	if got != want {
		t.Fatalf("psk(\"the key\") = %s", got)
	}
}

// A device, as echod is, connects to this server: it sends its hello and a touch, and gets back a
// problem and a picture bigger than one record, all whole.
func TestADeviceTalksToTheServer(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })

	pic := make([]byte, 70<<10) // 70 KB: more than one record
	rand.Read(pic)
	type result struct {
		h   Hello
		tc  Touch
		err error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() { done <- r }()
		c, err := ServerHandshake(b, "a-long-enough-test-key")
		if err != nil {
			r.err = err
			return
		}
		lines := NewLines(c)
		if r.h, r.err = lines.Hello(); r.err != nil {
			return
		}
		out := NewSender(c)
		if r.err = out.Problem("hello there"); r.err != nil {
			return
		}
		if r.err = out.Picture(image.Pt(12, 34), pic); r.err != nil {
			return
		}
		r.tc, r.err = lines.Touch()
	}()

	dev, err := deviceHandshake(a, "a-long-enough-test-key")
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(dev)
	if err := enc.Encode(map[string]any{"name": "Kitchen", "w": 960, "h": 480, "path": "/lovelace/0"}); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReaderSize(dev, 256<<10)
	kind, _, body, err := deviceRead(r)
	if err != nil || kind != KindProblem || string(body) != "hello there" {
		t.Fatalf("first message: kind %d %q %v", kind, body, err)
	}
	kind, at, body, err := deviceRead(r)
	if err != nil || kind != KindPicture || at != image.Pt(12, 34) || !bytes.Equal(body, pic) {
		t.Fatalf("second message: kind %d at %v, %d bytes, equal %v, %v", kind, at, len(body), bytes.Equal(body, pic), err)
	}
	if err := enc.Encode(map[string]any{"t": "tap", "x": 100, "y": 200}); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.h.Name != "Kitchen" || res.h.W != 960 || res.h.H != 480 || len(res.h.Caps) != 0 {
		t.Errorf("hello: %+v", res.h)
	}
	if res.tc != (Touch{T: "tap", X: 100, Y: 200}) {
		t.Errorf("touch: %+v", res.tc)
	}
}

// A device with the wrong key gets nowhere.
func TestADeviceWithTheWrongKeyFails(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	errs := make(chan error, 1)
	go func() {
		_, err := ServerHandshake(b, "a-long-enough-test-key")
		b.Close()
		errs <- err
	}()
	if _, err := deviceHandshake(a, "another-long-enough-key"); err == nil {
		t.Error("the device got through with the wrong key")
	}
	if err := <-errs; err == nil {
		t.Error("the server let a wrong key through")
	}
}
