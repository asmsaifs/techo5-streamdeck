package core

import (
	"net"
	"testing"
)

func TestPauseAndResume(t *testing.T) {
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if c.Running() {
		t.Fatal("running before Start")
	}
	for round := 0; round < 2; round++ {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		if err := c.Start(); err != nil { // no-op
			t.Fatal(err)
		}
		if !c.Running() {
			t.Fatal("not running after Start")
		}
		conn, err := net.Dial("tcp", c.Addr().String())
		if err != nil {
			t.Fatalf("round %d: dial: %v", round, err)
		}
		conn.Close()
		c.Pause()
		if c.Running() || c.Addr() != nil {
			t.Fatal("still running after Pause")
		}
	}
	c.Close()
	if err := c.Start(); err == nil {
		t.Fatal("Start after Close should fail")
	}
}

func TestRebind(t *testing.T) {
	c, err := New(Options{Dir: t.TempDir(), Listen: "127.0.0.1:0", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	// An address already in use is refused, and the deck is still up where it was.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := c.Rebind(busy.Addr().String()); err == nil {
		t.Fatal("rebind onto a busy port worked")
	}
	if !c.Running() {
		t.Fatal("deck is down after a failed rebind")
	}
	// A free one is taken.
	if err := c.Rebind("127.0.0.1:0"); err != nil || !c.Running() {
		t.Fatalf("rebind: %v running=%v", err, c.Running())
	}
}
