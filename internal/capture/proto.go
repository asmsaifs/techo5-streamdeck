package capture

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Message kinds a helper sends, see docs/helpers.md.
const (
	KindWindows = 1
	KindFrame   = 2
	KindAudio   = 3
	KindEvent   = 4
)

// maxPayload bounds one message so a corrupt length cannot make the core allocate gigabytes.
const maxPayload = 16 << 20

// Window is one capturable window in a helper's list.
type Window struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	App   string `json:"app"`
	W     int    `json:"w"`
	H     int    `json:"h"`
}

// Event is a status message from the helper.
type Event struct {
	Event string `json:"event"`
	Code  string `json:"code,omitempty"`
	Msg   string `json:"msg,omitempty"`
}

// Frame is a BGRA picture.
type Frame struct {
	W, H int
	BGRA []byte
}

// Audio is S16LE interleaved PCM at whatever rate the OS gave.
type Audio struct {
	Rate     int
	Channels int
	PCM      []byte
}

// ErrProtocol marks a stream the core cannot trust any more; the helper is restarted.
var ErrProtocol = errors.New("capture: protocol error")

// readMsg reads one framed message.
func readMsg(r io.Reader) (kind byte, payload []byte, err error) {
	var h [5]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.LittleEndian.Uint32(h[:4])
	if n > maxPayload {
		return 0, nil, fmt.Errorf("%w: message of %d bytes", ErrProtocol, n)
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return h[4], payload, nil
}

// writeMsg is the helper's side of the framing; the core uses it only in tests and in the fake helper.
func writeMsg(w io.Writer, kind byte, payload []byte) error {
	var h [5]byte
	binary.LittleEndian.PutUint32(h[:4], uint32(len(payload)))
	h[4] = kind
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func decodeFrame(p []byte) (Frame, error) {
	if len(p) < 4 {
		return Frame{}, fmt.Errorf("%w: short frame header", ErrProtocol)
	}
	w, h := int(binary.LittleEndian.Uint16(p)), int(binary.LittleEndian.Uint16(p[2:]))
	if w == 0 || h == 0 || len(p)-4 != w*h*4 {
		return Frame{}, fmt.Errorf("%w: frame %dx%d with %d pixel bytes", ErrProtocol, w, h, len(p)-4)
	}
	return Frame{W: w, H: h, BGRA: p[4:]}, nil
}

func decodeAudio(p []byte) (Audio, error) {
	if len(p) < 5 {
		return Audio{}, fmt.Errorf("%w: short audio header", ErrProtocol)
	}
	a := Audio{Rate: int(binary.LittleEndian.Uint32(p)), Channels: int(p[4]), PCM: p[5:]}
	if a.Channels == 0 || len(a.PCM)%(2*a.Channels) != 0 {
		return Audio{}, fmt.Errorf("%w: audio of %d bytes in %d channels", ErrProtocol, len(a.PCM), a.Channels)
	}
	return a, nil
}

// Command is one stdin line to the helper.
type Command struct {
	Cmd    string `json:"cmd"`
	Window string `json:"window,omitempty"`
	FPS    int    `json:"fps,omitempty"`
	MaxW   int    `json:"maxw,omitempty"`
	Audio  bool   `json:"audio,omitempty"`
	Kind   string `json:"kind,omitempty"`
	X      int    `json:"x,omitempty"`
	Y      int    `json:"y,omitempty"`
	DY     int    `json:"dy,omitempty"`
}

func encodeCommand(c Command) []byte {
	b, _ := json.Marshal(c) // only strings and ints
	return append(b, '\n')
}
