# Deck protocol

The deck server speaks the **dashcast protocol** the Show already has (`techo5/dashcast`,
`techo5/echod/internal/feature/dashboard/stream.go`) and extends it for sound. Everything below
the "Extensions" heading is new and is only used with a device that says it understands it, so an
old device keeps working unchanged.

## The connection

The **Show dials the server** (setting *Dashboard server*, `host:port`, default port 9555). The
stream is Noise `NNpsk0` with the prologue `techo5-dashcast/1`; the pre-shared key is derived from
the shared key text. `internal/wire/secure.go` is byte-compatible with the device's `secure.go`.

Inside the encrypted stream:

- **device → server**: text lines of JSON, each ending in `\n`, at most `wire.LineMax` bytes.
- **server → device**: messages of a 4-byte big-endian length, a kind byte, then the payload.

## Device → server

### hello (first line, once)

```json
{"name":"Kitchen Show","w":960,"h":480,"path":"/lovelace/0","kiosk":true,"caps":["audio1"]}
```

| Field | Meaning |
|---|---|
| `name` | The device's name; picks the profile. |
| `w`, `h` | Screen size in pixels. |
| `path`, `kiosk` | Dashcast leftovers; the deck ignores them. |
| `caps` | Optional list of extensions the device understands. **A device that sends no `caps` gets kinds 1-3 only.** Unknown caps are ignored. `wire.Hello.Has` tests one. |

### touch (one line each, after the hello)

```json
{"t":"tap","x":480,"y":240}
```

`t` is `tap`, `down`, `move` or `up`; `x`, `y` are device pixels. The device sends a `tap` only
when the finger lifts (no duration, no `down` first); a finger that moved more than 12 px sends
`down`, `move`s and `up`. Drags that start in the outer 40 px are kept by the device and never
sent (see `spikes.md`). The device can send nothing else: no action, command, URL or file path.

## Server → device

| Kind | Name | Payload | Needs |
|---|---|---|---|
| 1 | picture | 2-byte x, 2-byte y, then a JPEG to draw with its top left there | always |
| 2 | problem | a sentence for the screen to show | always |
| 3 | half | 2-byte x, 2-byte y, then a JPEG at half size, drawn doubled | always |
| 4 | audio | 8-byte stamp (µs), then interleaved S16LE PCM, 48000 Hz, 2 channels | `audio1` |
| 5 | clock | 8-byte stamp: the server's clock now | `audio1` |
| 6 | hello | JSON `{"latency_ms":N}` | `audio1` |

Integers are big-endian. The device's read loop already skips a kind it does not know, but the server never relies on
that: **it never sends kind 4, 5 or 6 to a device whose hello lacked `audio1`.**

## Extensions

### `audio1`: sound on the Show

A device that sends `"caps":["audio1"]` plays the stream's sound.

**kindHello (6)** is sent once, right after the device's hello is accepted, before any audio.
`latency_ms` is how long after its stamp the device presents a chunk (the server proposes 300; the device may clamp it). It is optional:
a device uses its own default until it arrives. Audio is never sent more than `latency_ms` ahead
of real time.

**kindClock (5)** carries the server's monotonic clock in microseconds. It is sent once as soon
as audio could follow, and then about once a second, while a source with sound is shown. As in the
cast protocol, the device only needs the *difference* between the clocks: arrival time minus stamp,
the smallest of the last ten (the network only adds delay). Before the first clock message nothing
can be placed and the device drops audio, so **the server sends a clock message first**.

**kindAudio (4)** is one chunk of at most 20 ms: 960 stereo frames = 3840 bytes of PCM after the
8-byte stamp. The stamp is the moment the chunk is to be heard, on the server's monotonic clock.
The device presents it at `stamp + offset + latency_ms`.

The timing rules are those of the cast protocol (`techo5-cast/docs/protocol.md`, "Time", "Stalls"
and "What the device drops"):

- The device drops a chunk more than 100 ms past its time, a payload that is not whole 4-byte
  frames, and anything stamped more than 2 s ahead of now.
- After a stall the server moves its audio clock forward instead of stamping the backlog in the
  past (it is more than 300 ms behind real time: skip ahead), so there is a pause, not a burst
  of dropped sound.
- When the stream closes, or the source changes to one without sound, the device stops its audio
  and discards what is queued. The server need not send anything to say so.
- The device plays through the same player as cast, so a call, an alarm or the voice assistant
  takes the speaker as it does there.

**Pictures stay unstamped.** For a web tile, how fast a tap shows its result matters more than
lip-sync, so pictures are drawn on arrival. For a video tile the server delays audio by the
measured picture latency instead: the stamp is captured time plus `av_offset_ms` (a per-tile
setting, default 0) so that picture and sound meet. The device needs to know nothing about this.

**Bit rate.** 48 kHz stereo S16 is 1.5 Mbit/s, on top of the pictures.

## Compatibility and tests

- A device without `audio1` gets exactly what it got before: kinds 1-3.
- A server without audio never sends kinds 4-6; a device may advertise `audio1` to any server.
- `internal/wire` has kinds 4-6 (`Sender.Audio`, `Clock`, `Setup`) and refuses them with `ErrNoCap`
  unless `SetCaps` saw `audio1` in the hello; the server calls `SetCaps` on every connection.
- `internal/audio` is the sending side: `Resampler` (any rate, mono or more channels, to 48 kHz
  stereo), and `Stream`, which cuts 20 ms chunks, stamps them, sends the latency and clock first
  and the clock every second, moves the timeline forward after a stall (more than 300 ms behind),
  never stamps further ahead than the latency, and drops the oldest queued chunk when the sink is
  slow. A source (5.4) calls `Stream.Write`.
- `cmd/fakeshow` plays kinds 4/5 in step 5.5.

## Open

- The device holds the speaker from the first clock message until the connection ends, also
  after the Show goes back to the deck. It should let go after a few seconds without audio.

- The device side (5.2) is where the exact player hook-up lives; if it needs a different chunk
  size, change it here first.
- Whether `latency_ms` should come from the device (its buffer) rather than the server: the
  hello above lets the server choose; a device could later say `"latency_ms"` in its own hello
  as a preference.
