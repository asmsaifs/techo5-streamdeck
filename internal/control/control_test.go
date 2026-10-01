package control

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

// The socket path is limited to about 100 bytes, so the test folder is a short one.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestSendReachesHandler(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got []string
	err := Listen(ctx, dir, func(_ context.Context, id string) error {
		got = append(got, id)
		if id == "bad" {
			return errors.New("there is\nno such button")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Send(dir, "Alt+1"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	err = Send(dir, "bad")
	if err == nil || err.Error() != "there is no such button" {
		t.Errorf("Send(bad) = %v, want the handler's words on one line", err)
	}
	if len(got) != 2 || got[0] != "Alt+1" {
		t.Errorf("handler saw %v", got)
	}
}

func TestSecondListenerIsRefusedAndStaleSocketReplaced(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	h := func(context.Context, string) error { return nil }
	if err := Listen(ctx, dir, h); err != nil {
		t.Fatal(err)
	}
	if err := Listen(context.Background(), dir, h); !errors.Is(err, ErrRunning) {
		t.Errorf("second Listen = %v, want ErrRunning", err)
	}
	cancel()

	// A socket file nobody listens on, as an app that was killed leaves behind.
	dir = shortDir(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: Path(dir), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if err := Listen(ctx2, dir, h); err != nil {
		t.Errorf("Listen over a stale socket: %v", err)
	}
}

func TestSendWithoutApp(t *testing.T) {
	err := Send(shortDir(t), "x")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("Send = %v, want 'not running'", err)
	}
}
