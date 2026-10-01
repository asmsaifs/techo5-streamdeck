package wire

import (
	"bytes"
	"encoding/binary"
	"image"
	"strings"
	"testing"
)

func TestHelloAndTouchLines(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		want    Hello
	}{
		{"a Show configured for dashcast", `{"name":"Kitchen","w":960,"h":480,"path":"/lovelace/0","kiosk":true}`, false,
			Hello{Name: "Kitchen", W: 960, H: 480, Path: "/lovelace/0", Kiosk: true}},
		{"with caps", `{"name":"K","w":1280,"h":800,"caps":["audio1"]}`, false,
			Hello{Name: "K", W: 1280, H: 800, Caps: []string{"audio1"}}},
		{"no size", `{"name":"K"}`, true, Hello{}},
		{"an impossible size", `{"name":"K","w":99999,"h":480}`, true, Hello{}},
		{"not JSON", `hello`, true, Hello{}},
		{"too long", `{"name":"` + strings.Repeat("x", LineMax) + `","w":1,"h":1}`, true, Hello{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := NewLines(strings.NewReader(tt.in + "\n")).Hello()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err == nil && (h.Name != tt.want.Name || h.W != tt.want.W || h.H != tt.want.H ||
				h.Path != tt.want.Path || h.Kiosk != tt.want.Kiosk || len(h.Caps) != len(tt.want.Caps)) {
				t.Errorf("got %+v, want %+v", h, tt.want)
			}
		})
	}

	l := NewLines(strings.NewReader("{\"name\":\"K\",\"w\":960,\"h\":480}\n{\"t\":\"down\",\"x\":1,\"y\":2}\n"))
	if _, err := l.Hello(); err != nil {
		t.Fatal(err)
	}
	if tc, err := l.Touch(); err != nil || tc != (Touch{"down", 1, 2}) {
		t.Errorf("touch %+v %v", tc, err)
	}
	if _, err := l.Touch(); err == nil {
		t.Error("a touch read past the end")
	}
}

func TestCaps(t *testing.T) {
	h := Hello{Caps: []string{"audio1"}}
	if !h.Has("audio1") || h.Has("audio2") || (Hello{}).Has("audio1") {
		t.Error("Has is wrong")
	}
}

func TestMessagesRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	s := NewSender(&buf)
	big := bytes.Repeat([]byte{0xab}, 70<<10)
	if err := s.Picture(image.Pt(959, 479), big); err != nil {
		t.Fatal(err)
	}
	if err := s.Half(image.Pt(0, 0), []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Problem("no page"); err != nil {
		t.Fatal(err)
	}
	want := []Msg{
		{KindPicture, image.Pt(959, 479), big},
		{KindHalf, image.Pt(0, 0), []byte{1, 2, 3}},
		{KindProblem, image.Point{}, []byte("no page")},
	}
	for i, w := range want {
		m, err := ReadMsg(&buf)
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if m.Kind != w.Kind || m.At != w.At || !bytes.Equal(m.Data, w.Data) {
			t.Errorf("message %d: kind %d at %v, %d bytes", i, m.Kind, m.At, len(m.Data))
		}
	}
}

func TestReadMsgRefusesBadSizes(t *testing.T) {
	for _, n := range []uint32{0, MessageMax + 1} {
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], n)
		if _, err := ReadMsg(bytes.NewReader(hdr[:])); err == nil {
			t.Errorf("a message of %d bytes was read", n)
		}
	}
	if err := NewSender(&bytes.Buffer{}).Send(KindPicture, make([]byte, MessageMax)); err == nil {
		t.Error("a message over MessageMax was sent")
	}
}

func TestKeys(t *testing.T) {
	k := NewKey()
	if err := CheckKey(k); err != nil {
		t.Errorf("NewKey made %q: %v", k, err)
	}
	if k == NewKey() {
		t.Error("two keys alike")
	}
	if CheckKey("short") == nil {
		t.Error("a short key passed")
	}
}
