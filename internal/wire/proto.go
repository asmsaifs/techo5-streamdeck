package wire

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"sync"
	"time"
)

// The conversation inside the encrypted connection, as dashcast/serve.go describes it: the device
// opens with one line of JSON, a hello; after that it sends a line per touch. What comes back is
// messages framed as a 4-byte big-endian length, then a kind byte and its payload:
//
//	KindPicture  2-byte x, 2-byte y, then a JPEG to draw with its top left there
//	KindProblem  a sentence for the screen to show
//	KindHalf     2-byte x, 2-byte y, then a JPEG at half size, to draw doubled with its top left there
//
// Every Show that speaks dashcast knows these three. Later kinds are only sent to a device whose
// hello lists the capability that names them (docs/protocol.md):
//
//	KindAudio  8-byte stamp (µs), then S16LE 48 kHz stereo PCM            needs CapAudio
//	KindClock  8-byte stamp: this server's clock now                      needs CapAudio
//	KindSetup  JSON {"latency_ms": n}: how long after its stamp to play   needs CapAudio
const (
	KindPicture = 1
	KindProblem = 2
	KindHalf    = 3
	KindAudio   = 4
	KindClock   = 5
	KindSetup   = 6
)

// CapAudio is the hello capability that lets the server send kinds 4 to 6.
const CapAudio = "audio1"

// ErrNoCap is what Send returns for a kind the device did not advertise.
var ErrNoCap = errors.New("wire: the device did not advertise the capability for that message")

const (
	// LineMax is the longest line a device may send: a hello or a touch is far shorter, and the limit
	// keeps a device from sending one that never ends.
	LineMax = 4096

	// MessageMax is the largest message a device accepts (echod's messageMax): a full-screen picture
	// is well under 1 MB.
	MessageMax = 4 << 20

	// KeyMin is the shortest key the server will run with. The key is all that stands between the
	// network and running commands on this computer.
	KeyMin = 16
)

// Hello is the first line a device sends.
type Hello struct {
	Name string `json:"name"`
	W    int    `json:"w"`
	H    int    `json:"h"`

	// Path and Kiosk are what a Show configured for Home Assistant's dashcast sends; the deck
	// ignores them, and they are kept so a hello reads back as it was sent.
	Path  string `json:"path,omitempty"`
	Kiosk bool   `json:"kiosk,omitempty"`

	// Caps are the message kinds beyond the first three the device understands. Today's devices send
	// none.
	Caps []string `json:"caps,omitempty"`
}

// Has reports whether the device advertised the capability.
func (h Hello) Has(c string) bool {
	for _, x := range h.Caps {
		if x == c {
			return true
		}
	}
	return false
}

// Touch is one touch line: T is tap, down, move or up, in device pixels.
type Touch struct {
	T string `json:"t"`
	X int    `json:"x"`
	Y int    `json:"y"`
}

// Lines reads a device's lines, the hello and then touches, each at most LineMax bytes.
type Lines struct{ s *bufio.Scanner }

func NewLines(r io.Reader) *Lines {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 1024), LineMax)
	return &Lines{s: s}
}

func (l *Lines) next(v any) error {
	if !l.s.Scan() {
		if err := l.s.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return json.Unmarshal(l.s.Bytes(), v)
}

// Hello reads the hello and checks the screen size is one a device could have.
func (l *Lines) Hello() (Hello, error) {
	var h Hello
	if err := l.next(&h); err != nil {
		return h, err
	}
	if h.W <= 0 || h.H <= 0 || h.W > 4096 || h.H > 4096 {
		return h, fmt.Errorf("wire: a hello with a screen of %dx%d", h.W, h.H)
	}
	return h, nil
}

// Touch reads the next touch line.
func (l *Lines) Touch() (Touch, error) {
	var t Touch
	err := l.next(&t)
	return t, err
}

// Sender writes messages to one device. Pictures come from several goroutines, so writes are
// serialized, and each has a deadline: a device that stopped reading must not hang the server.
type Sender struct {
	mu    sync.Mutex
	w     io.Writer
	audio bool
}

// NewSender sends to w. Until SetCaps says what the device understands, only kinds 1 to 3 go.
func NewSender(w io.Writer) *Sender { return &Sender{w: w} }

// SetCaps records what the device's hello advertised. Call it before the first Send.
func (s *Sender) SetCaps(h Hello) {
	s.mu.Lock()
	s.audio = h.Has(CapAudio)
	s.mu.Unlock()
}

type deadliner interface{ SetWriteDeadline(time.Time) error }

// Send writes one message of kind, its payload the parts one after another.
func (s *Sender) Send(kind byte, payload ...[]byte) error {
	if kind >= KindAudio {
		s.mu.Lock()
		ok := s.audio && kind <= KindSetup
		s.mu.Unlock()
		if !ok {
			return ErrNoCap
		}
	}
	n := 1
	for _, p := range payload {
		n += len(p)
	}
	if n > MessageMax {
		return fmt.Errorf("wire: a message of %d bytes, more than a device takes", n)
	}
	msg := make([]byte, 4, 4+n)
	binary.BigEndian.PutUint32(msg, uint32(n))
	msg = append(msg, kind)
	for _, p := range payload {
		msg = append(msg, p...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.w.(deadliner); ok {
		_ = d.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	_, err := s.w.Write(msg)
	return err
}

// Picture sends a JPEG to draw with its top left at at; Half one at half size, to draw doubled.
func (s *Sender) Picture(at image.Point, jpg []byte) error { return s.Send(KindPicture, pos(at), jpg) }
func (s *Sender) Half(at image.Point, jpg []byte) error    { return s.Send(KindHalf, pos(at), jpg) }

// Audio sends one chunk of 48 kHz stereo S16LE PCM, to be heard at stamp µs on this server's clock.
func (s *Sender) Audio(stampUs int64, pcm []byte) error {
	return s.Send(KindAudio, stamp(stampUs), pcm)
}

// Clock sends this server's clock, in µs, for the device to work out the difference from.
func (s *Sender) Clock(nowUs int64) error { return s.Send(KindClock, stamp(nowUs)) }

// Setup tells the device how long after its stamp to play a chunk.
func (s *Sender) Setup(latencyMs int) error {
	b, _ := json.Marshal(struct {
		LatencyMs int `json:"latency_ms"`
	}{latencyMs})
	return s.Send(KindSetup, b)
}

func stamp(us int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(us))
	return b[:]
}

// Problem sends a sentence for the screen to show.
func (s *Sender) Problem(text string) error { return s.Send(KindProblem, []byte(text)) }

func pos(at image.Point) []byte {
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:], uint16(at.X))
	binary.BigEndian.PutUint16(b[2:], uint16(at.Y))
	return b[:]
}

// Msg is one message as a device reads it.
type Msg struct {
	Kind byte
	At   image.Point // for KindPicture and KindHalf
	Data []byte      // the JPEG, or the problem's text
}

// ReadMsg reads one message, as a device does: for cmd/fakeshow and the tests. r should be
// buffered.
func ReadMsg(r io.Reader) (Msg, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Msg{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MessageMax {
		return Msg{}, errors.New("wire: a message of an impossible size")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return Msg{}, err
	}
	m := Msg{Kind: b[0], Data: b[1:]}
	if m.Kind == KindPicture || m.Kind == KindHalf {
		if len(b) < 5 {
			return Msg{}, errors.New("wire: a picture with no place")
		}
		m.At = image.Pt(int(binary.BigEndian.Uint16(b[1:3])), int(binary.BigEndian.Uint16(b[3:5])))
		m.Data = b[5:]
	}
	return m, nil
}

// NewKey makes a random key: 24 bytes, 32 characters, comfortably over KeyMin and easy to type on
// the Show's setup page.
func NewKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// CheckKey refuses a key too short to protect a computer that runs commands on a button press.
func CheckKey(key string) error {
	if len(key) < KeyMin {
		return fmt.Errorf("the key must be at least %d characters", KeyMin)
	}
	return nil
}
