// Package sendspin sends this computer's sound to the Shows the user picked, as a Sendspin server
// that dials them (docs/speaker.md).
//
// It is not sendspin-go's server: that one dials every player it finds, holds a player for as long
// as it runs, and pulls from its source on its own ticker. A Show keeps the first server that
// reaches it and turns the rest away, so a sender that stayed connected through hours of silence
// would lock Music Assistant out of the Show all day. This one dials only the chosen Shows, only
// while there is sound, and pushes each chunk as the computer delivers it.
//
// The messages are the ones sendspin-go's protocol package defines, written out here so this
// package needs neither its WebSocket library nor its encoders, one of which wants cgo.
package sendspin

import "encoding/json"

// protocolVersion is the only one there is. The Show refuses zero.
const protocolVersion = 1

// What a chunk of audio is on the wire: one type byte, an 8-byte big-endian timestamp in
// microseconds on this server's clock, and the coded audio.
const (
	audioChunkType = 4
	chunkHeader    = 1 + 8
)

// envelope is every text message: a type and its payload.
type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type audioFormat struct {
	Codec      string `json:"codec"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   int    `json:"bit_depth"`
}

type playerSupport struct {
	SupportedFormats  []audioFormat `json:"supported_formats"`
	SupportedCommands []string      `json:"supported_commands"`
}

// clientHello is what a player says first. Only what this sender acts on is read. The support
// object has a versioned key in the spec and an older unversioned one that Music Assistant still
// reads, and a player may send either.
type clientHello struct {
	ClientID       string         `json:"client_id"`
	Name           string         `json:"name"`
	Version        int            `json:"version"`
	SupportedRoles []string       `json:"supported_roles"`
	PlayerV1       *playerSupport `json:"player@v1_support"`
	Player         *playerSupport `json:"player_support"`
}

func (h clientHello) player() *playerSupport {
	if h.PlayerV1 != nil {
		return h.PlayerV1
	}
	return h.Player
}

type serverHello struct {
	ServerID         string   `json:"server_id"`
	Name             string   `json:"name"`
	Version          int      `json:"version"`
	ActiveRoles      []string `json:"active_roles"`
	ConnectionReason string   `json:"connection_reason"`
}

type clientTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
}

type serverTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
	ServerReceived    int64 `json:"server_received"`
	ServerTransmitted int64 `json:"server_transmitted"`
}

type streamStartPlayer struct {
	Codec       string `json:"codec"`
	SampleRate  int    `json:"sample_rate"`
	Channels    int    `json:"channels"`
	BitDepth    int    `json:"bit_depth"`
	CodecHeader string `json:"codec_header,omitempty"` // base64
}

type streamStart struct {
	Player *streamStartPlayer `json:"player"`
}

type streamEnd struct {
	Roles []string `json:"roles"`
}

// playerCommand is a volume or a mute. Neither field is omitempty: a volume of zero and an unmute
// are both values the Show must hear.
type playerCommand struct {
	Command string `json:"command"`
	Volume  int    `json:"volume"`
	Mute    bool   `json:"mute"`
}

type serverCommand struct {
	Player playerCommand `json:"player"`
}
