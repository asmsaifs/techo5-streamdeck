package sendspin

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
)

// ErrBusy is a Show that is already playing from another server: it keeps the first one to reach
// it and turns the rest away with 409.
var ErrBusy = errors.New("the Show is playing from another source")

const (
	// helloWait bounds the handshake. The Show sends its hello at once.
	helloWait = 5 * time.Second
	// writeWait bounds one message, so a Show that went away is found out.
	writeWait = 2 * time.Second
	// queueChunks is how many chunks may wait for the socket: a second. Past that the oldest goes,
	// being the likeliest to arrive after its time.
	queueChunks = 50
)

// syncRounds and syncWait: the Show measures the clock in a burst of eight as soon as the
// handshake is done, and audio that arrives before the first burst lands is placed against a clock
// it does not know yet. So stream/start waits for the burst, or for syncWait if the player does not
// sync at once. A variable for the tests.
var (
	syncRounds       = 8
	syncWait         = 500 * time.Millisecond
	errNoFormat      = errors.New("the Show offers no format this sender can send (FLAC or PCM at 48 kHz, 16-bit stereo)")
	errNotPlayer     = errors.New("the Show did not offer the player role")
	errUnexpectedEnd = errors.New("the Show hung up")
)

// DialOptions say who is dialing and on what clock.
type DialOptions struct {
	// Name and ID are this computer, as the Show is told.
	Name, ID string
	// Clock is the time in microseconds that chunk stamps are on. The Show learns it from the
	// clock sync this connection answers.
	Clock func() int64
}

// Conn is one Show playing this computer's sound, from stream/start to Close.
type Conn struct {
	ws     *websocket.Conn
	name   string // what the Show calls itself
	codec  string
	clock  func() int64
	ctx    context.Context
	cancel context.CancelFunc

	queue  chan chunkOut
	done   chan struct{} // closed when the connection is finished, by either side
	err    error         // why, set before done closes
	closed sync.Once

	dropped atomic.Int64
	sent    atomic.Int64

	mu          sync.Mutex // guards the last volume sent
	volume      int
	muted       bool
	volumeKnown bool
}

type chunkOut struct {
	stamp int64
	pcm   []byte
}

// Dial connects to a Show's Sendspin player at url ("ws://host:8928/sendspin"), does the
// handshake and starts a stream. A Show already playing from another server is ErrBusy.
func Dial(ctx context.Context, url string, o DialOptions) (*Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, helloWait)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, url, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusConflict {
			return nil, ErrBusy
		}
		return nil, err
	}
	// The hello is small; audio goes the other way. Nothing the Show sends is larger than this.
	ws.SetReadLimit(64 << 10)

	c, err := handshake(dctx, ws, o)
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "")
		return nil, err
	}
	return c, nil
}

func handshake(ctx context.Context, ws *websocket.Conn, o DialOptions) (*Conn, error) {
	var hello clientHello
	if err := readMessage(ctx, ws, "client/hello", &hello); err != nil {
		return nil, err
	}
	if !slices.Contains(hello.SupportedRoles, "player@v1") && !slices.Contains(hello.SupportedRoles, "player") {
		return nil, errNotPlayer
	}
	codec, err := pickCodec(hello.player())
	if err != nil {
		return nil, err
	}
	if err := writeJSON(ctx, ws, "server/hello", serverHello{
		ServerID: o.ID, Name: o.Name, Version: protocolVersion,
		// The player role alone: nothing here has a track to describe or controls to offer.
		ActiveRoles:      []string{"player@v1"},
		ConnectionReason: "playback",
	}); err != nil {
		return nil, err
	}

	cctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		ws: ws, name: hello.Name, codec: codec, clock: o.Clock, ctx: cctx, cancel: cancel,
		queue: make(chan chunkOut, queueChunks),
		done:  make(chan struct{}),
	}
	synced := make(chan struct{})
	go c.read(synced)

	t := time.NewTimer(syncWait)
	defer t.Stop()
	select {
	case <-synced:
	case <-t.C:
	case <-c.done:
		return nil, c.err
	case <-ctx.Done():
		c.finish(ctx.Err())
		return nil, ctx.Err()
	}

	var enc *flacEncoder
	start := streamStartPlayer{Codec: codec, SampleRate: audio.Rate, Channels: audio.Channels, BitDepth: 16}
	if codec == "flac" {
		if enc, err = newFLACEncoder(); err != nil {
			c.finish(err)
			return nil, err
		}
		start.CodecHeader = base64.StdEncoding.EncodeToString(enc.header)
	}
	if err := writeJSON(ctx, ws, "stream/start", streamStart{Player: &start}); err != nil {
		c.finish(err)
		return nil, err
	}
	go c.write(enc)
	return c, nil
}

// pickCodec takes FLAC if the Show lists it at 48 kHz, 16-bit stereo, else PCM in that format.
// Opus is the Show's own fallback and needs cgo to encode, so it is never chosen here.
func pickCodec(p *playerSupport) (string, error) {
	if p == nil {
		return "", errNoFormat
	}
	ok := func(codec string) bool {
		return slices.ContainsFunc(p.SupportedFormats, func(f audioFormat) bool {
			return f.Codec == codec && f.SampleRate == audio.Rate && f.Channels == audio.Channels && f.BitDepth == 16
		})
	}
	switch {
	case ok("flac"):
		return "flac", nil
	case ok("pcm"):
		return "pcm", nil
	}
	return "", errNoFormat
}

// Name is what the Show called itself in its hello.
func (c *Conn) Name() string { return c.name }

// Codec is what the stream is sent as, "flac" or "pcm".
func (c *Conn) Codec() string { return c.codec }

// Done is closed once the connection is over, whichever side ended it; Err says why.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended: nil after Close.
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Stats are the chunks sent and the ones dropped because the socket fell behind.
func (c *Conn) Stats() (sent, dropped int64) { return c.sent.Load(), c.dropped.Load() }

// Write queues one 20 ms chunk of wire PCM (audio.Wire), to be played at stamp, on the clock the
// connection answers with. It never blocks: a full queue drops its oldest chunk.
func (c *Conn) Write(stamp int64, pcm []byte) {
	ch := chunkOut{stamp, pcm}
	for {
		select {
		case <-c.done:
			return
		case c.queue <- ch:
			return
		default:
			select {
			case <-c.queue:
				c.dropped.Add(1)
			default:
			}
		}
	}
}

// Volume sets the Show's volume, 0 to 100, and its mute. Only what changed is sent.
func (c *Conn) Volume(v int, muted bool) {
	v = max(0, min(100, v))
	c.mu.Lock()
	sendV := !c.volumeKnown || v != c.volume
	sendM := !c.volumeKnown || muted != c.muted
	c.volume, c.muted, c.volumeKnown = v, muted, true
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, writeWait)
	defer cancel()
	if sendV {
		if err := writeJSON(ctx, c.ws, "server/command", serverCommand{Player: playerCommand{Command: "volume", Volume: v}}); err != nil {
			slog.Debug("sendspin volume", "show", c.name, "err", err)
		}
	}
	if sendM {
		if err := writeJSON(ctx, c.ws, "server/command", serverCommand{Player: playerCommand{Command: "mute", Mute: muted}}); err != nil {
			slog.Debug("sendspin mute", "show", c.name, "err", err)
		}
	}
}

// Close ends the stream and hangs up, which gives the Show back to whoever wants it next.
func (c *Conn) Close() {
	select {
	case <-c.done:
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeWait)
	defer cancel()
	_ = writeJSON(ctx, c.ws, "stream/end", streamEnd{Roles: []string{"player"}})
	c.finish(nil)
	c.ws.Close(websocket.StatusNormalClosure, "")
}

func (c *Conn) finish(err error) {
	c.closed.Do(func() {
		c.err = err
		close(c.done)
		c.cancel()
	})
}

// read answers the clock sync, which the Show runs in bursts for as long as it plays, and notices
// the Show leaving. synced is closed after the first burst's worth of answers.
func (c *Conn) read(synced chan struct{}) {
	answered := 0
	for {
		typ, b, err := c.ws.Read(c.ctx)
		if err != nil {
			c.finish(fmt.Errorf("%w: %v", errUnexpectedEnd, err))
			return
		}
		received := c.clock()
		if typ != websocket.MessageText {
			continue
		}
		var m envelope
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		switch m.Type {
		case "client/time":
			var t clientTime
			if json.Unmarshal(m.Payload, &t) != nil {
				continue
			}
			// Written here rather than queued behind audio: the time in the queue would count as
			// network to the Show and put its idea of this clock off by half of it.
			ctx, cancel := context.WithTimeout(c.ctx, writeWait)
			err := writeJSON(ctx, c.ws, "server/time", serverTime{
				ClientTransmitted: t.ClientTransmitted, ServerReceived: received, ServerTransmitted: c.clock(),
			})
			cancel()
			if err != nil {
				c.finish(err)
				return
			}
			if answered++; answered == syncRounds {
				close(synced)
			}
		case "client/goodbye":
			c.finish(errUnexpectedEnd)
			c.ws.Close(websocket.StatusNormalClosure, "")
			return
		case "client/state":
			slog.Debug("sendspin client state", "show", c.name, "state", string(m.Payload))
		}
	}
}

// write codes and sends what is queued, in order, until the connection ends.
func (c *Conn) write(enc *flacEncoder) {
	buf := make([]byte, 0, chunkHeader+audio.ChunkFrames*audio.Channels*2)
	for {
		var ch chunkOut
		select {
		case <-c.done:
			return
		case ch = <-c.queue:
		}
		data := ch.pcm
		if enc != nil {
			var err error
			if data, err = enc.encode(ch.pcm); err != nil {
				slog.Warn("sendspin encode", "err", err)
				continue
			}
		}
		buf = append(buf[:0], audioChunkType)
		buf = binary.BigEndian.AppendUint64(buf, uint64(ch.stamp))
		buf = append(buf, data...)
		ctx, cancel := context.WithTimeout(c.ctx, writeWait)
		err := c.ws.Write(ctx, websocket.MessageBinary, buf)
		cancel()
		if err != nil {
			c.finish(err)
			return
		}
		c.sent.Add(1)
	}
}

func writeJSON(ctx context.Context, ws *websocket.Conn, typ string, payload any) error {
	b, err := json.Marshal(message{Type: typ, Payload: payload})
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, b)
}

// readMessage reads one text message and wants it to be of type want.
func readMessage(ctx context.Context, ws *websocket.Conn, want string, v any) error {
	typ, b, err := ws.Read(ctx)
	if err != nil {
		return fmt.Errorf("waiting for %s: %w", want, err)
	}
	var m envelope
	if typ != websocket.MessageText || json.Unmarshal(b, &m) != nil {
		return fmt.Errorf("waiting for %s: got something that is not a message", want)
	}
	if m.Type != want {
		return fmt.Errorf("waiting for %s: got %s", want, m.Type)
	}
	if err := json.Unmarshal(m.Payload, v); err != nil {
		return fmt.Errorf("%s: %w", want, err)
	}
	return nil
}
