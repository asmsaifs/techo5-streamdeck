package audio

import "encoding/binary"

// Wire format of the sound the Show plays: 48 kHz, stereo, S16LE.
const (
	Rate     = 48000
	Channels = 2
)

// Format describes PCM a source delivers: S16LE interleaved, at Rate Hz with Channels channels.
// Capture helpers and Chrome do not all give 48 kHz stereo, so Stream converts.
type Format struct {
	Rate     int
	Channels int
}

// Wire is the format the Show plays.
var Wire = Format{Rate, Channels}

// Valid reports whether Resampler can convert from f.
func (f Format) Valid() bool { return f.Rate >= 8000 && f.Rate <= 192000 && f.Channels >= 1 }

// Resampler converts a stream of PCM to the wire format by linear interpolation: cheap, and good
// enough for the speaker of an Echo Show, whose output stage is far from the limit. It keeps one
// frame and a fractional position between calls, so a stream cut into pieces anywhere gives
// exactly the output of the stream whole. Mono is copied to both channels; of more than two
// channels the first two are kept.
type Resampler struct {
	in   Format
	step float64 // input frames per output frame

	have bool
	prev [2]float64
	frac float64 // where the next output frame falls, in input frames after prev
}

func NewResampler(in Format) *Resampler {
	return &Resampler{in: in, step: float64(in.Rate) / Rate}
}

// Convert takes whole input frames (a trailing part of a frame is ignored) and returns the wire
// bytes they make.
func (r *Resampler) Convert(pcm []byte) []byte {
	frameBytes := 2 * r.in.Channels
	n := len(pcm) / frameBytes
	out := make([]byte, 0, int(float64(n)/r.step+2)*4)
	for i := 0; i < n; i++ {
		f := pcm[i*frameBytes:]
		l := float64(int16(binary.LittleEndian.Uint16(f)))
		rt := l
		if r.in.Channels > 1 {
			rt = float64(int16(binary.LittleEndian.Uint16(f[2:])))
		}
		cur := [2]float64{l, rt}
		if !r.have {
			r.prev, r.have, r.frac = cur, true, 0
			continue
		}
		for r.frac < 1 {
			out = binary.LittleEndian.AppendUint16(out, uint16(lerp(r.prev[0], cur[0], r.frac)))
			out = binary.LittleEndian.AppendUint16(out, uint16(lerp(r.prev[1], cur[1], r.frac)))
			r.frac += r.step
		}
		r.frac--
		r.prev = cur
	}
	return out
}

func lerp(a, b, t float64) int16 {
	v := a + (b-a)*t
	if v >= 0 {
		return int16(v + 0.5)
	}
	return int16(v - 0.5)
}
