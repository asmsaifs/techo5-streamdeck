package capture

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// maxFailures is how many helper runs in a row may end without reaching "ready" before Run gives up.
const maxFailures = 5

// Client runs a capture helper, keeps it alive and exposes what it sends. Run does the work;
// the other methods may be called from any goroutine.
type Client struct {
	Path string
	Args []string
	// Backoff is the wait before restart number n (1-based). Default 1 s doubling to 10 s.
	Backoff func(n int) time.Duration

	frames  chan Frame
	audio   chan Audio
	events  chan Event
	windows chan []Window

	mu     sync.Mutex
	stdin  io.Writer // nil while no helper is up
	active *Command  // the last start, replayed after a restart
}

// New returns a Client for the helper executable at path.
func New(path string, args ...string) *Client {
	return &Client{
		Path: path, Args: args,
		frames:  make(chan Frame, 1),
		audio:   make(chan Audio, 64),
		events:  make(chan Event, 16),
		windows: make(chan []Window, 1),
	}
}

// Frames holds the latest picture: a new frame replaces an unread older one, like sources.Source.
func (c *Client) Frames() <-chan Frame { return c.frames }

// Audio delivers PCM in order. When the reader falls behind, the oldest chunks are dropped: stale
// sound is worth less than keeping up.
func (c *Client) Audio() <-chan Audio { return c.audio }

// Events delivers status messages and restarts as {"event":"restart"}. A reader that falls behind loses the oldest.
func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) backoff(n int) time.Duration {
	if c.Backoff != nil {
		return c.Backoff(n)
	}
	d := time.Second << (n - 1)
	if d > 10*time.Second || d <= 0 {
		d = 10 * time.Second
	}
	return d
}

// send writes a command to the running helper.
func (c *Client) send(cmd Command) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return errors.New("capture: helper not running")
	}
	_, err := c.stdin.Write(encodeCommand(cmd))
	return err
}

// List asks the helper for its capturable windows.
func (c *Client) List(ctx context.Context) ([]Window, error) {
	select { // drop an answer nobody collected
	case <-c.windows:
	default:
	}
	if err := c.send(Command{Cmd: "list"}); err != nil {
		return nil, err
	}
	select {
	case w := <-c.windows:
		return w, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Start captures a window. The command is remembered and sent again if the helper restarts.
func (c *Client) Start(window string, fps, maxW int, audio bool) error {
	cmd := Command{Cmd: "start", Window: window, FPS: fps, MaxW: maxW, Audio: audio}
	c.mu.Lock()
	c.active = &cmd
	c.mu.Unlock()
	return c.send(cmd)
}

// Stop ends the capture; the helper keeps running.
func (c *Client) Stop() error {
	c.mu.Lock()
	c.active = nil
	c.mu.Unlock()
	return c.send(Command{Cmd: "stop"})
}

// Input injects a mouse event; x and y are in the pixels of the last frame.
func (c *Client) Input(kind string, x, y int) error {
	return c.send(Command{Cmd: "input", Kind: kind, X: x, Y: y})
}

// Wheel scrolls the captured window by dy pixels, positive down, at x,y.
func (c *Client) Wheel(x, y, dy int) error {
	return c.send(Command{Cmd: "input", Kind: "wheel", X: x, Y: y, DY: dy})
}

// Run starts the helper and restarts it when it dies, until ctx ends or it fails maxFailures times
// in a row without becoming ready. It returns the reason.
func (c *Client) Run(ctx context.Context) error {
	failures := 0
	for {
		ready, err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ready {
			failures = 0
		}
		failures++
		if failures >= maxFailures {
			return fmt.Errorf("capture: helper keeps failing: %w", err)
		}
		c.emit(Event{Event: "restart", Msg: fmt.Sprint(err)})
		select {
		case <-time.After(c.backoff(failures)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runOnce runs one helper process to its end. ready says whether it got as far as "ready".
func (c *Client) runOnce(ctx context.Context) (ready bool, err error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return false, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	go func() {
		sc := bufio.NewScanner(errPipe)
		for sc.Scan() {
			log.Printf("capture helper: %s", sc.Text())
		}
	}()

	c.mu.Lock()
	c.stdin = in
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.stdin = nil
		c.mu.Unlock()
		in.Close()
		cmd.Wait()
	}()

	r := bufio.NewReaderSize(out, 1<<20)
	for {
		kind, p, err := readMsg(r)
		if err != nil {
			return ready, err
		}
		switch kind {
		case KindWindows:
			var w []Window
			if err := json.Unmarshal(p, &w); err != nil {
				return ready, fmt.Errorf("%w: window list: %v", ErrProtocol, err)
			}
			select {
			case <-c.windows:
			default:
			}
			c.windows <- w
		case KindFrame:
			f, err := decodeFrame(p)
			if err != nil {
				return ready, err
			}
			select {
			case <-c.frames:
			default:
			}
			c.frames <- f
		case KindAudio:
			a, err := decodeAudio(p)
			if err != nil {
				return ready, err
			}
			for {
				select {
				case c.audio <- a:
				default:
					select {
					case <-c.audio:
						continue
					default:
					}
				}
				break
			}
		case KindEvent:
			var e Event
			if err := json.Unmarshal(p, &e); err != nil {
				return ready, fmt.Errorf("%w: event: %v", ErrProtocol, err)
			}
			if e.Event == "ready" && !ready {
				ready = true
				c.mu.Lock()
				a := c.active
				c.mu.Unlock()
				if a != nil {
					if err := c.send(*a); err != nil {
						return ready, err
					}
				}
			}
			c.emit(e)
		default:
			return ready, fmt.Errorf("%w: unknown kind %d", ErrProtocol, kind)
		}
	}
}

// emit queues an event, dropping the oldest when nobody reads.
func (c *Client) emit(e Event) {
	for {
		select {
		case c.events <- e:
			return
		default:
			select {
			case <-c.events:
			default:
			}
		}
	}
}
