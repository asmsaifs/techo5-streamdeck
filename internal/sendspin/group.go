package sendspin

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
)

// Defaults and limits of the two settings (docs/speaker.md, "Settings").
const (
	DefaultLead = 200 * time.Millisecond
	DefaultIdle = 5 * time.Second
	MinLead     = 100 * time.Millisecond
	MaxLead     = time.Second
)

const (
	chunkLen   = 20 * time.Millisecond
	chunkBytes = audio.ChunkFrames * audio.Channels * 2

	// loudLevel is -60 dBFS: below it a chunk counts as silence. A device left on with nothing
	// playing delivers zeros, or dither a few steps above them.
	loudLevel = 33
	// openAfter is how long the sound must be above loudLevel before the Shows are dialed, so a
	// click does not take a Show away from Music Assistant.
	openAfter = 100 * time.Millisecond
	// retryEvery is how often a Show that could not be reached, or dropped out, is dialed again
	// while there is sound. A Show that was busy is not: it is asked again when the sound next
	// starts (docs/speaker.md, "Activity gate").
	retryEvery = 10 * time.Second
	// tick is how often the gate looks for silence and for Shows to dial again.
	tick = 250 * time.Millisecond
)

// Options set up a Group.
type Options struct {
	// Name and ID are this computer, as the Shows are told.
	Name, ID string
	// Lead is how far ahead of now each chunk is stamped: what the network and the Show's buffer
	// get to work with, and how far the sound runs behind the computer. DefaultLead if zero.
	Lead time.Duration
	// Idle is how long the sound must be silent before the Shows are let go. DefaultIdle if zero.
	Idle time.Duration
	// Resolve finds a Show by the name it advertises, as "ws://host:port/path".
	Resolve func(ctx context.Context, show string) (string, error)
	// Forget, if set, is told of a Show that could not be reached or dropped out, so that the
	// next Resolve looks for it again rather than at the address it had.
	Forget func(show string)
	// Changed is called after the State changes, never with a lock held.
	Changed func()
	// Now stands in for the clock in tests. time.Now if nil.
	Now func() time.Time
}

// State is what the Group is doing, for the tray and the editor.
type State struct {
	// Open: there is sound, so the Shows are wanted.
	Open bool
	// Playing are the Shows connected now; Busy the ones playing from another source; Failed the
	// ones that could not be reached, with why. All are by the names they advertise.
	Playing []string
	Busy    []string
	Failed  map[string]string
}

// Group sends one sound to the chosen Shows, all with the same stamps so that they play in step,
// and only while there is sound: it dials them when the sound starts and lets them go after Idle
// of silence, so the Shows are free for Music Assistant the rest of the time.
type Group struct {
	o     Options
	epoch time.Time
	base  int64 // the clock's reading at epoch, in µs

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	shows    []string
	in       audio.Format
	rs       *audio.Resampler
	pending  []byte
	next     int64 // the stamp of the next chunk; 0 until the first
	loudFor  time.Duration
	lastLoud time.Time
	open     bool
	gen      int // counts openings, so a dial from an earlier one is not kept
	conns    map[string]*Conn
	dialing  map[string]bool
	tried    map[string]time.Time
	busy     map[string]bool
	failed   map[string]string
	backlog  []chunkOut // the newest chunks, a lead's worth, for a Show that connects late
	volume   int
	muted    bool
	volSet   bool
}

// NewGroup starts a Group for the given Shows. Close it when done.
func NewGroup(shows []string, o Options) *Group {
	if o.Lead <= 0 {
		o.Lead = DefaultLead
	}
	if o.Idle <= 0 {
		o.Idle = DefaultIdle
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Changed == nil {
		o.Changed = func() {}
	}
	now := o.Now()
	g := &Group{
		o: o, epoch: now,
		// Wall time at the start, then the monotonic clock from there: the stamps are near the
		// Show's own clock even before it has measured ours, and a change to the computer's clock
		// does not move them.
		base:    now.UnixMicro(),
		shows:   slices.Clone(shows),
		conns:   map[string]*Conn{},
		dialing: map[string]bool{},
		tried:   map[string]time.Time{},
		busy:    map[string]bool{},
		failed:  map[string]string{},
	}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	g.wg.Add(1)
	go g.watch()
	return g
}

// Clock is the time chunks are stamped on, in microseconds.
func (g *Group) Clock() int64 { return g.micros(g.o.Now()) }

func (g *Group) micros(t time.Time) int64 { return g.base + t.Sub(g.epoch).Microseconds() }

// Lead is how far ahead chunks are stamped.
func (g *Group) Lead() time.Duration { return g.o.Lead }

// Write takes sound in format f as the computer delivers it: it is converted, cut into 20 ms
// chunks, stamped and sent to every connected Show. It never blocks on the network.
func (g *Group) Write(f audio.Format, pcm []byte) {
	if !f.Valid() || len(pcm) == 0 {
		return
	}
	g.mu.Lock()
	if g.rs == nil || g.in != f {
		g.in, g.rs = f, audio.NewResampler(f)
	}
	if f == audio.Wire {
		g.pending = append(g.pending, pcm[:len(pcm)-len(pcm)%4]...)
	} else {
		g.pending = append(g.pending, g.rs.Convert(pcm)...)
	}
	var dial []string
	for len(g.pending) >= chunkBytes {
		dial = append(dial, g.chunk(g.pending[:chunkBytes:chunkBytes])...)
		g.pending = g.pending[chunkBytes:]
	}
	if len(g.pending) == 0 {
		g.pending = nil
	}
	gen := g.gen
	g.mu.Unlock()
	g.dialAll(gen, dial)
}

// chunk stamps one chunk, feeds the gate and sends it. It returns the Shows to dial if the gate
// just opened. Wants mu.
func (g *Group) chunk(pcm []byte) []string {
	now := g.o.Now()
	nowUs := g.micros(now)
	lead := g.o.Lead.Microseconds()
	// The chunks follow on from each other, 20 ms apart, so the Show plays them without a seam.
	// The computer delivers on the same clock the stamps are on, so they stay near now+lead; if
	// they fall behind by half the lead (the sound stalled), the next starts again at now+lead,
	// and one that would be stamped more than a lead past that (the source delivered faster than
	// real time) is dropped, as the Show would have to hold it too long.
	switch {
	case g.next == 0 || g.next < nowUs+lead/2:
		g.next = nowUs + lead
	case g.next > nowUs+2*lead:
		return nil
	}
	c := chunkOut{stamp: g.next, pcm: append([]byte(nil), pcm...)}
	g.next += chunkLen.Microseconds()

	var dial []string
	if loud(pcm) {
		g.loudFor += chunkLen
		g.lastLoud = now
		if !g.open && g.loudFor >= openAfter && g.ctx.Err() == nil {
			dial = g.openGate(now)
		}
	} else {
		g.loudFor = 0
	}

	if keep := int(g.o.Lead / chunkLen); len(g.backlog) >= keep {
		g.backlog = append(g.backlog[:0], g.backlog[len(g.backlog)-keep+1:]...)
	}
	g.backlog = append(g.backlog, c)
	for _, conn := range g.conns {
		conn.Write(c.stamp, c.pcm)
	}
	return dial
}

// loud reports whether any sample of pcm is above loudLevel.
func loud(pcm []byte) bool {
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(binary.LittleEndian.Uint16(pcm[i:]))
		if v > loudLevel || v < -loudLevel {
			return true
		}
	}
	return false
}

// openGate marks the sound as started and returns every chosen Show to dial. Wants mu.
func (g *Group) openGate(now time.Time) []string {
	g.open = true
	g.gen++
	clear(g.busy)
	clear(g.failed)
	slog.Info("speaker: sound started", "shows", g.shows)
	var dial []string
	for _, s := range g.shows {
		if g.conns[s] == nil && !g.dialing[s] {
			g.dialing[s] = true
			g.tried[s] = now
			dial = append(dial, s)
		}
	}
	return dial
}

func (g *Group) dialAll(gen int, shows []string) {
	if len(shows) == 0 {
		return
	}
	for _, s := range shows {
		g.wg.Add(1)
		go g.dial(gen, s)
	}
	g.o.Changed()
}

// dial connects to one Show and, if the sound is still wanted there, starts sending to it with
// what is held back from before it connected.
func (g *Group) dial(gen int, show string) {
	defer g.wg.Done()
	conn, err := g.connect(show)

	g.mu.Lock()
	delete(g.dialing, show)
	wanted := g.open && g.gen == gen && slices.Contains(g.shows, show) && g.ctx.Err() == nil
	switch {
	case err != nil && !wanted:
	case errors.Is(err, ErrBusy):
		g.busy[show] = true
		slog.Info("speaker: Show busy", "show", show)
	case err != nil:
		g.forget(show)
		g.failed[show] = err.Error()
		slog.Warn("speaker: could not reach the Show", "show", show, "err", err)
	case !wanted:
		g.mu.Unlock()
		conn.Close()
		return
	default:
		g.conns[show] = conn
		// What was stamped before the Show connected still plays if it has half the lead left. The
		// Show's output runs about 85 ms behind what it is handed, and a first chunk with less time
		// than that lands late: measured on a Show 5, the stream then started with a 115 ms skip.
		soon := g.Clock() + g.o.Lead.Microseconds()/2
		for _, c := range g.backlog {
			if c.stamp > soon {
				conn.Write(c.stamp, c.pcm)
			}
		}
		vol, muted, volSet := g.volume, g.muted, g.volSet
		slog.Info("speaker: playing on the Show", "show", show, "codec", conn.Codec())
		g.wg.Add(1)
		go g.follow(show, conn)
		g.mu.Unlock()
		if volSet {
			conn.Volume(vol, muted)
		}
		g.o.Changed()
		return
	}
	g.mu.Unlock()
	g.o.Changed()
}

func (g *Group) forget(show string) {
	if g.o.Forget != nil {
		g.o.Forget(show)
	}
}

func (g *Group) connect(show string) (*Conn, error) {
	ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
	defer cancel()
	url, err := g.o.Resolve(ctx, show)
	if err != nil {
		return nil, err
	}
	return Dial(ctx, url, DialOptions{Name: g.o.Name, ID: g.o.ID, Clock: g.Clock})
}

// follow forgets a connection once it ends.
func (g *Group) follow(show string, conn *Conn) {
	defer g.wg.Done()
	select {
	case <-conn.Done():
	case <-g.ctx.Done():
		return
	}
	g.mu.Lock()
	if g.conns[show] != conn {
		g.mu.Unlock()
		return
	}
	delete(g.conns, show)
	if err := conn.Err(); err != nil && g.open {
		g.forget(show)
		g.failed[show] = err.Error()
		slog.Warn("speaker: the Show dropped out", "show", show, "err", err)
	}
	g.mu.Unlock()
	g.o.Changed()
}

// watch lets the Shows go after Idle of silence, and dials again a Show that could not be reached
// while the sound goes on.
func (g *Group) watch() {
	defer g.wg.Done()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
		}
		now := g.o.Now()
		g.mu.Lock()
		if !g.open {
			g.mu.Unlock()
			continue
		}
		if now.Sub(g.lastLoud) >= g.o.Idle {
			conns := g.closeGate()
			g.mu.Unlock()
			for _, c := range conns {
				c.Close()
			}
			g.o.Changed()
			continue
		}
		var dial []string
		for _, s := range g.shows {
			if g.conns[s] == nil && !g.dialing[s] && !g.busy[s] && now.Sub(g.tried[s]) >= retryEvery {
				g.dialing[s] = true
				g.tried[s] = now
				dial = append(dial, s)
			}
		}
		gen := g.gen
		g.mu.Unlock()
		g.dialAll(gen, dial)
	}
}

// closeGate marks the sound as stopped and returns the connections to close. Wants mu.
func (g *Group) closeGate() []*Conn {
	if g.open {
		slog.Info("speaker: silence, letting the Shows go", "connected", len(g.conns))
	}
	g.open = false
	g.loudFor = 0
	clear(g.busy)
	clear(g.failed)
	var conns []*Conn
	for s, c := range g.conns {
		conns = append(conns, c)
		delete(g.conns, s)
	}
	return conns
}

// SetShows changes which Shows play. One no longer chosen is let go at once; a new one is dialed
// if there is sound now.
func (g *Group) SetShows(shows []string) {
	g.mu.Lock()
	g.shows = slices.Clone(shows)
	var drop []*Conn
	for s, c := range g.conns {
		if !slices.Contains(shows, s) {
			drop = append(drop, c)
			delete(g.conns, s)
		}
	}
	var dial []string
	if g.open {
		now := g.o.Now()
		for _, s := range shows {
			if g.conns[s] == nil && !g.dialing[s] {
				g.dialing[s] = true
				g.tried[s] = now
				dial = append(dial, s)
			}
		}
	}
	gen := g.gen
	g.mu.Unlock()
	for _, c := range drop {
		c.Close()
	}
	g.dialAll(gen, dial)
	if len(drop) > 0 && len(dial) == 0 {
		g.o.Changed()
	}
}

// SetVolume forwards the computer's volume for the device, 0 to 1, and its mute to every Show,
// now and as each connects. Until it is first called the Shows keep their own volume.
func (g *Group) SetVolume(v float64, muted bool) {
	g.mu.Lock()
	g.volume, g.muted, g.volSet = int(max(0, min(1, v))*100+0.5), muted, true
	vol := g.volume
	conns := make([]*Conn, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		c.Volume(vol, muted)
	}
}

// State is what the Group is doing now.
func (g *Group) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := State{Open: g.open, Failed: map[string]string{}}
	for s := range g.conns {
		st.Playing = append(st.Playing, s)
	}
	for s := range g.busy {
		st.Busy = append(st.Busy, s)
	}
	for s, e := range g.failed {
		st.Failed[s] = e
	}
	slices.Sort(st.Playing)
	slices.Sort(st.Busy)
	return st
}

// Close lets every Show go and stops the Group.
func (g *Group) Close() {
	g.cancel()
	g.mu.Lock()
	conns := g.closeGate()
	g.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	g.wg.Wait()
}
