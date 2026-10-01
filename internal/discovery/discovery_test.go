package discovery

import (
	"net"
	"testing"
)

func TestTxt(t *testing.T) {
	got := txt("Studio Mac")
	if len(got) != 2 || got[0] != "name=Studio Mac" || got[1] != "v=1" {
		t.Fatalf("txt = %v", got)
	}
}

// A server on the loopback address is not announced: no Show could reach it.
func TestLoopbackIsNotAnnounced(t *testing.T) {
	a, err := Advertise(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9555})
	if a != nil || err != nil {
		t.Fatalf("Advertise on loopback = %v, %v; want nil, nil", a, err)
	}
}
