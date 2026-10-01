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
