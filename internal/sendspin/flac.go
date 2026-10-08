package sendspin

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
)

// flacEncoder makes one FLAC frame of each 20 ms chunk. FLAC rather than PCM because the Show
// asks for it first: it is a third of the bytes, on a radio the Show shares with Bluetooth.
//
// A frame carries its number, so each connection has its own encoder and its frames count from
// zero after its own stream/start.
type flacEncoder struct {
	enc    *flac.Encoder
	buf    bytes.Buffer
	header []byte
	ch     [audio.Channels][]int32
}

func newFLACEncoder() (*flacEncoder, error) {
	e := &flacEncoder{}
	info := &meta.StreamInfo{
		BlockSizeMin:  audio.ChunkFrames,
		BlockSizeMax:  audio.ChunkFrames,
		SampleRate:    audio.Rate,
		NChannels:     audio.Channels,
		BitsPerSample: 16,
	}
	enc, err := flac.NewEncoder(&e.buf, info)
	if err != nil {
		return nil, fmt.Errorf("flac encoder: %w", err)
	}
	e.enc = enc
	// The encoder writes "fLaC" and the STREAMINFO block at once: that is the codec header the
	// Show puts in front of the frames to make them a stream.
	e.header = bytes.Clone(e.buf.Bytes())
	e.buf.Reset()
	for i := range e.ch {
		e.ch[i] = make([]int32, audio.ChunkFrames)
	}
	return e, nil
}

// encode takes one chunk of wire PCM, exactly audio.ChunkFrames frames, and returns its frame.
// The slice is valid until the next call.
func (e *flacEncoder) encode(pcm []byte) ([]byte, error) {
	if len(pcm) != audio.ChunkFrames*audio.Channels*2 {
		return nil, fmt.Errorf("flac: a chunk is %d bytes, not %d", audio.ChunkFrames*audio.Channels*2, len(pcm))
	}
	for i := range audio.ChunkFrames {
		for c := range audio.Channels {
			e.ch[c][i] = int32(int16(binary.LittleEndian.Uint16(pcm[(i*audio.Channels+c)*2:])))
		}
	}
	sub := make([]*frame.Subframe, audio.Channels)
	for c := range sub {
		// Verbatim asks the encoder to find the best predictor itself, which is where the saving is.
		sub[c] = &frame.Subframe{SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, Samples: e.ch[c], NSamples: audio.ChunkFrames}
	}
	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         audio.ChunkFrames,
			SampleRate:        audio.Rate,
			Channels:          frame.ChannelsLR,
			BitsPerSample:     16,
		},
		Subframes: sub,
	}
	e.buf.Reset()
	if err := e.enc.WriteFrame(f); err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}
	return e.buf.Bytes(), nil
}
