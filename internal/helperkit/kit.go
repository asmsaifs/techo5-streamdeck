// Package helperkit is the shared half of the Windows and Linux capture helpers: the framed stdout
// writer, the stdin command loop and the picture scaler. Each OS supplies a Backend; the protocol is
// in docs/helpers.md. The macOS helper is Swift and does not use this.
package helperkit

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"hash/maphash"
	"io"
	"os"
	"sync"
)

// Message kinds, as in docs/helpers.md.
const (
	kindWindows = 1
	kindFrame   = 2
	kindAudio   = 3
	kindEvent   = 4
)

// Window is one entry of the window list.
type Window struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	App   string `json:"app"`
	W     int    `json:"w"`
	H     int    `json:"h"`
}

// Command is one stdin line.
type Command struct {
	Cmd    string `json:"cmd"`
	Window string `json:"window"`
	FPS    int    `json:"fps"`
	MaxW   int    `json:"maxw"`
	Audio  bool   `json:"audio"`
	Kind   string `json:"kind"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	DY     int    `json:"dy"`
}

// StartArgs are the capture settings with defaults filled in.
type StartArgs struct {
	Window string
	FPS    int
	MaxW   int
	Audio  bool
}

// Backend is what an OS implements. Calls arrive one at a time, in order; a backend that captures
// runs its own goroutines and writes through the Out it was given at Start.
type Backend interface {
	// List returns the windows worth putting on a button.
	List() ([]Window, error)
	// Start replaces any running capture. An error is reported as an "internal" event unless it is
	// an *Error, which carries its own code.
	Start(out *Out, a StartArgs) error
	// Input posts mouse input; x,y are pixels of the last frame sent.
	Input(kind string, x, y, dy int)
	// Stop ends the capture. It must be safe to call with none running.
	Stop()
}

// Error is a backend error with a protocol code ("permission", "no_window", "unsupported").
type Error struct{ Code, Msg string }

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// Out writes protocol messages. It is safe for concurrent use.
type Out struct {
	mu   sync.Mutex
	w    io.Writer
	seed maphash.Seed
	last uint64
}

// NewOut makes an Out writing to w.
func NewOut(w io.Writer) *Out { return &Out{w: w, seed: maphash.MakeSeed()} }

func (o *Out) send(kind byte, parts ...[]byte) {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	buf := make([]byte, 5, 5+n)
	binary.LittleEndian.PutUint32(buf, uint32(n))
	buf[4] = kind
	for _, p := range parts {
		buf = append(buf, p...)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// One write per message so a reader never sees half a header from a concurrent sender.
	o.w.Write(buf)
}

// Event sends a status message.
func (o *Out) Event(event, code, msg string) {
	b, _ := json.Marshal(map[string]string{"event": event, "code": code, "msg": msg})
	if code == "" && msg == "" {
		b, _ = json.Marshal(map[string]string{"event": event})
	}
	o.send(kindEvent, b)
}

// Windows sends the window list.
func (o *Out) Windows(list []Window) {
	if list == nil {
		list = []Window{}
	}
	b, _ := json.Marshal(list)
	o.send(kindWindows, b)
}

// Frame sends a BGRA picture (w*h*4 bytes, no padding). A picture identical to the previous one is
// dropped: the protocol says frames mean "new picture", and an idle window should cost nothing.
func (o *Out) Frame(w, h int, bgra []byte) {
	if w <= 0 || h <= 0 || w > 65535 || h > 65535 || len(bgra) != w*h*4 {
		return
	}
	sum := maphash.Bytes(o.seed, bgra)
	o.mu.Lock()
	same := sum == o.last
	o.last = sum
	o.mu.Unlock()
	if same {
		return
	}
	o.send(kindFrame, []byte{byte(w), byte(w >> 8), byte(h), byte(h >> 8)}, bgra)
}

// ResetFrames forgets the last picture, so the next one is sent even if identical. Called at Start:
// a new capture must always produce a first frame.
func (o *Out) ResetFrames() {
	o.mu.Lock()
	o.last = 0
	o.mu.Unlock()
}

// Audio sends S16LE interleaved PCM as the OS gave it.
func (o *Out) Audio(rate, channels int, pcm []byte) {
	if len(pcm) == 0 || channels <= 0 {
		return
	}
	var h [5]byte
	binary.LittleEndian.PutUint32(h[:4], uint32(rate))
	h[4] = byte(channels)
	o.send(kindAudio, h[:], pcm)
}

// Run serves the protocol on stdin/stdout until stdin closes.
func Run(b Backend) {
	serve(b, os.Stdin, NewOut(os.Stdout))
}

func serve(b Backend, in io.Reader, out *Out) {
	out.Event("ready", "", "")
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var c Command
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			out.Event("error", "unsupported", "bad command line")
			continue
		}
		handle(b, out, c)
	}
	b.Stop() // stdin closed: quit
}

func handle(b Backend, out *Out, c Command) {
	switch c.Cmd {
	case "list":
		l, err := b.List()
		if err != nil {
			report(out, err)
			return
		}
		out.Windows(l)
	case "start":
		a := StartArgs{Window: c.Window, FPS: c.FPS, MaxW: c.MaxW, Audio: c.Audio}
		if a.FPS <= 0 {
			a.FPS = 25
		}
		if a.FPS > 60 {
			a.FPS = 60
		}
		if a.MaxW <= 0 {
			a.MaxW = 960
		}
		b.Stop()
		out.ResetFrames()
		if err := b.Start(out, a); err != nil {
			report(out, err)
			return
		}
		out.Event("started", "", c.Window)
	case "stop":
		b.Stop()
		out.Event("stopped", "", "")
	case "input":
		b.Input(c.Kind, c.X, c.Y, c.DY)
	default:
		out.Event("error", "unsupported", "command "+c.Cmd)
	}
}

func report(out *Out, err error) {
	if e, ok := err.(*Error); ok {
		out.Event("error", e.Code, e.Msg)
		return
	}
	out.Event("error", "internal", err.Error())
}
