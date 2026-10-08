# The Show as the computer's speaker — implementation plan

The goal: a sound output on the computer called **TECHO5 Show**. Pick it in the OS sound settings (or
per app, where the OS allows) and everything the computer plays comes out of the Show, and only the
Show: nothing has to be muted on the desktop, because the sound never reaches its speakers.

Order: **macOS first** (the Mac in daily use), then Linux, then Windows.

## 0. What already exists

| Piece | Where | What it gives us |
|---|---|---|
| Sendspin player on the Show | `techo5/echod/internal/feature/sendspin/` | Advertises `_sendspin._tcp` with `path=/sendspin` on port **8928**; a server dials in. Decodes FLAC, Opus and PCM, 48 kHz 16-bit stereo; plays chunks at their timestamps after clock sync; takes `volume` and `mute` commands. **No device change needed.** |
| `sendspin-go` v1.8.2 | Go module (echod uses it) | Message types and `CreateAudioChunk` (`pkg/protocol`), a pure-Go FLAC encoder (`pkg/audio/encode`, mewkiz/flac, no cgo), mDNS browsing (`pkg/discovery`), and a whole server (`pkg/sendspin.Server`). |
| Audio pipeline | `internal/audio` | Resampler (any rate → 48 kHz, mono → stereo) and 20 ms chunker. |
| Capture helpers | `helpers/mac`, `cmd/deckcap-win`, `cmd/deckcap-linux` | Stdio protocol with an audio message (kind 3, any rate, S16LE), restart with backoff (`internal/capture`). The Linux helper already makes a null sink and reads its monitor with `parec`. |

### Key decision: our own small sender, not `sendspin.Server`

The library's server does almost the right thing, but three points rule it out as is:

1. **It dials every player it finds.** With `DiscoverClients` it connects to every `_sendspin._tcp`
   on the network: other speakers, other Shows, Music Assistant's players. We want only the Shows the
   user picked.
2. **It holds the room for good.** The Show keeps whichever server connects first and refuses the
   rest with 409 ("first to arrive holds the room", `listen.go`). A server that stays connected while
   the computer is silent locks Music Assistant out of the Show all day. We must connect when sound
   starts and leave after a stretch of silence.
3. **It pulls from the source on its own 20 ms ticker, 500 ms ahead** (`BufferAheadMs`). A live
   device delivers on its own clock, so pulling on a ticker gives under- and overruns, and 500 ms is
   more lag than needed on a LAN.

So `internal/sendspin` is a **client-dialing sender** built on the library's `pkg/protocol` messages
and FLAC encoder: dial chosen Shows, run the handshake and clock sync, push chunks as the device
delivers them, stamped `now + lead`. It is ≈ 400 lines, and it is the same on all three OSes. Only
the virtual device and the reader of its sound differ per OS.

## 1. Architecture

```
 any app ──► "TECHO5 Show" output device ──► reader ──► internal/audio ──► internal/sendspin ──► Show :8928
             (macOS: HAL plug-in             (helper or   resample to 48k,   activity gate, FLAC,
              Linux: null sink                pactl/parec) 20 ms chunks      stamps, clock sync,
              Windows: virtual driver)                                       volume ⇄ device volume
```

- **Device**: a real output device in the OS, so the user picks it like headphones. Its sound goes
  nowhere else.
- **Reader**: gets that device's mixed sound. One per OS, behind one Go interface:

  ```go
  type Tap interface {
      Start() (<-chan audio.Chunk, error) // S16LE stereo, any rate, as the device delivers it
      Volume() <-chan float64             // the device's volume slider, 0..1
      Stop()
  }
  ```

- **Activity gate** (in `internal/sendspin`): dial when the sound has been above −60 dBFS for
  100 ms; send `stream/end` and close after `idle_s` (default 5 s) of silence, so Music Assistant can
  have the Show again. A 409 shows "Show busy (another source playing)" in the tray, and the dial is
  retried when the sound next starts, not in a loop.
- **Lead**: chunks are stamped `now + lead_ms`, default **200 ms** (configurable, 100 to 1000).
  Every reader on every OS delivers on the host clock (`mach_absolute_time`, the Pulse clock, the
  WASAPI clock), the same clock the stamps use, so there is no drift to correct between device and
  sender.
- **Volume**: the OS's slider for "TECHO5 Show" is forwarded as a Sendspin `volume`/`mute` command,
  so the keyboard's volume keys work on the Show. The device itself passes sound at unity gain.
- **Several Shows**: each chosen Show gets its own connection with the same stamps, so they play in
  step (Sendspin's sync is what makes that work).

### Settings (config.json)

```json
"speaker": {
  "enabled": true,
  "shows": ["Kitchen Show"],
  "lead_ms": 200,
  "idle_s": 5
}
```

Shows are named as they advertise themselves (`name=` in the TXT record); the editor lists what mDNS
finds. Off by default.

## 2. Phases and steps

Sizes: S ≈ a session, M ≈ 2–3 sessions, L ≈ a week of sessions.

### Phase 10 — Shared sender (all OSes)

**10.1 Spike: Sendspin from Go to the Show (S)** — `spikes/sendspin/main.go`: dial a Show's
`ws://<ip>:8928/sendspin`, handshake, play a 440 Hz sine as FLAC at 200 ms lead. Measure: time to
first sound, the smallest lead with no dropouts on Wi-Fi, CPU of the FLAC encode. Write the numbers
into `docs/spikes.md`. Check what the Show does with a second server while Music Assistant holds it
(expected: 409).
*Done when:* the tone plays on the real Show without clicks for 10 minutes.

**10.2 `internal/sendspin` (M)** — the sender from §1:
- `Dial(ctx, addr) (*Conn, error)`: `client/hello` and `server/hello`, time sync (`client/time` and
  `server/time` answered from a monotonic clock), `stream/start` with `flac` 48000/16/2 (fall back to
  `pcm` if the Show doesn't list FLAC), `Write(chunk)`, `Volume(v)`, `Close()` with `stream/end`.
- `Group`: the connections to the chosen Shows, a shared stamp clock, and the activity gate.
- Discovery: browse `_sendspin._tcp` with the `zeroconf` the deck already uses, and keep the name to
  address map current.
- Table-driven tests against an in-process fake player: handshake, stamps rise by 20 ms, gate
  opens and closes, a 409 is reported once, volume is forwarded.
*Done when:* `go test ./internal/sendspin` is green and the spike runs through the package.

**10.3 Settings, tray and editor (S)** — `speaker` settings as above; tray item *Play sound on Show*
(checkbox) and its state (off, waiting for sound, playing on N, busy); editor panel *Speaker*: Show
picker from discovery, lead and idle sliders, a *Test tone* button. A deck action `speaker.toggle`
so a button can switch it.
*Done when:* the test tone plays from the editor; the tray shows the state.

### Phase 11 — macOS

**11.1 Virtual device: AudioServerPlugIn (L)** — `helpers/mac-audio/` builds `TECHO5 Show.driver`,
a Core Audio HAL plug-in. It runs in user space, inside `coreaudiod`'s driver service: no kernel
extension and no system extension approval.
- Start from Apple's **NullAudio** sample (Apple sample code licence, fine for MIT) or **libASPL**
  (MIT, C++). Not BlackHole: it is GPL-3 and can't go into this repo.
- One device, "TECHO5 Show": a 2-channel output stream and a hidden 2-channel input stream that
  plays back what the output wrote (a loopback ring of about 1 s). 48 kHz fixed (offering 44.1 kHz
  too is a later nicety); the zero-timestamp period comes from the host clock.
- A volume and a mute control on the output. They don't change the sound; the reader reads them and
  forwards them (§1, Volume).
- `kAudioDevicePropertyIsHidden` stays false for the output only; the input is marked so that it
  doesn't clutter the microphone list (`kAudioDevicePropertyDeviceCanBeDefaultDevice` false).
- Build with `clang` and a plist from `packaging/macos/build-driver.sh` (no Xcode project), universal
  (arm64 + x86_64).
*Done when:* after a manual install (`sudo cp -R … /Library/Audio/Plug-Ins/HAL/` and
`sudo killall coreaudiod`), "TECHO5 Show" shows in Sound settings, Music plays into it silently, and
QuickTime can record its loopback input.

**11.2 Reader in `deckcap-mac` (M)** — new helper command `{"cmd":"tap","device":"TECHO5 Show"}`
that opens the device's loopback input with an `AudioDeviceIOProc` (not AVAudioEngine, to keep the
latency and pick the device by UID), converts Float32 to S16LE, and sends kind 3 audio as window
capture does. New helper events: `volume` `{v, muted}` on the device's control changes
(`AudioObjectAddPropertyListener`). Update `docs/helpers.md`.
- **Permission**: reading any input device needs the **Microphone** permission on macOS 14+. Add
  `NSMicrophoneUsageDescription` to `Info.plist` and the `com.apple.security.device.audio-input`
  entitlement, and report a missing grant as `permission` with `msg: "microphone"`, like the Screen
  Recording case. (Getting the sound over the driver's own Mach service instead would avoid the
  prompt, but needs `AudioServerPlugIn_MachServices` and an XPC protocol; keep that as a later option.)
*Done when:* the helper, run by hand, prints the levels of what Music plays into "TECHO5 Show".

**11.3 Darwin `Tap` and end to end (S)** — `internal/speaker/tap_darwin.go` drives the helper
through `internal/capture` and turns its audio and volume events into the `Tap` interface; wire it to
`internal/sendspin`.
*Done when:* with the Mac's output set to "TECHO5 Show", Music, YouTube in Safari and a system
alert play on the real Show; the volume keys move the Show's volume; after 5 s of silence Music
Assistant can play on the Show again.

**11.4 Install and update (M)** — the `.dmg` can't put a file in `/Library`, so:
- The app installs the driver on first use of *Play sound on Show*: it asks for the admin password
  (`AuthorizationExecuteWithPrivileges` is deprecated; use an `osascript … with administrator
  privileges` call or a small privileged helper via `SMAppService` for a first version), copies the
  bundle from `Contents/Resources/`, and restarts `coreaudiod` (sound drops for about a second; say
  so before asking).
- The driver carries a version. On an app update the app compares it and offers to replace it.
- An *Uninstall speaker driver* item in the editor's Speaker panel.
- Sign the `.driver` with the Developer ID and include it in the notarized `.dmg`. An ad-hoc
  signature is enough on this Mac for development.
- Add the permission to `docs/permissions.md` (Microphone, admin password once).
*Done when:* a fresh Mac goes from the `.dmg` to sound on the Show with one password prompt and one
Microphone prompt.

**Phase 11 milestone:** the Mac plays through the Show. Tag the next minor release.

### Phase 12 — Linux

**12.1 Virtual device: null sink (S)** — no driver. PulseAudio and PipeWire (through pipewire-pulse)
both give a real output device with:
`pactl load-module module-null-sink sink_name=techo5_show sink_properties=device.description="TECHO5 Show"`.
- Made by the app when *Play sound on Show* is switched on and unloaded when it is switched off or
  the app quits; the module id is kept so that a crash leaves nothing behind for long (on start,
  unload any `techo5_show` left over). Optionally, a *Keep the device when the app isn't running*
  setting writes a PipeWire drop-in under `~/.config/pipewire/pipewire.conf.d/` instead.
- Reuse what `cmd/deckcap-linux/audio_linux.go` and `internal/helperkit/pulse.go` already do.
*Done when:* "TECHO5 Show" shows in GNOME's and KDE's sound settings and apps can pick it.

**12.2 Linux `Tap` (S)** — `internal/speaker/tap_linux.go`: `parec -d techo5_show.monitor
--format=s16le --rate=48000 --channels=2 --latency-msec=20`, no helper needed. Volume: watch
`pactl subscribe` for changes to the sink and read its volume and mute. Check whether the monitor
carries the sound before or after the sink's volume; if after, reset the sink to 100 % and keep the
slider position only as the forwarded value.
*Done when:* on Ubuntu (PipeWire) and one PulseAudio distro in UTM, Firefox plays on the Show and the
volume keys move the Show's volume.

**12.3 Packaging (S)** — `.deb` depends on `pulseaudio-utils` (or `pipewire-pulse` + `pulseaudio-utils`);
the AppImage checks for `pactl`/`parec` and says which package to install.

**Phase 12 milestone:** the same on Linux.

### Phase 13 — Windows

A selectable output device on Windows needs a kernel audio driver; there is no user-space way. So
Windows comes in two stages.

**13.1 Bring your own cable (S)** — works without our own driver: the user installs
**VB-CABLE** (free; its licence doesn't allow bundling it, so the editor links to its page) and picks
"CABLE Input" as the output. The app reads "CABLE Output" with **WASAPI capture** on that endpoint.
- `cmd/deckcap-win`: new `tap` command that opens an endpoint by name through `IMMDeviceEnumerator`
  (the hand-made COM calls in `audio_windows.go` cover most of it), event-driven shared-mode capture,
  kind 3 audio; the endpoint's volume through `IAudioEndpointVolume` with a change callback.
- The editor's Speaker panel lists the capture endpoints and remembers the chosen one.
- First run of `deckcap-win` on real Windows (it has never run): do it in UTM (Windows 11 ARM) and on
  an x64 PC.
*Done when:* on Windows 11 with VB-CABLE, Edge plays on the Show.

**13.2 Own driver (L, optional)** — "TECHO5 Show" as a real endpoint with nothing to install from
elsewhere:
- Base: Microsoft's **SysVAD** sample (MIT) cut down to one render endpoint with a loopback capture
  pin, or the MIT-licensed *Virtual Audio Driver* project. Builds with the WDK for x64 and ARM64.
- Development: test signing (`bcdedit /set testsigning on`) in a VM only.
- Release: **attestation signing** through the Microsoft Partner Center, which needs an EV code
  signing certificate (≈ $300+ a year) and a registered company or individual account. Weigh that
  cost before starting.
- Installer: the NSIS script adds `pnputil /add-driver techo5show.inf /install` and removes it on
  uninstall.
*Done when:* a fresh Windows 11 PC installs the app and gets "TECHO5 Show" in its sound outputs with
no test signing.

**Phase 13 milestone:** the same on Windows.

## 3. Device side (one required, one nice-to-have)

- **Taking over from Music Assistant (required).** The Sendspin spec ranks competing servers by
  declared activity; the Show doesn't yet (first one holds the room). Spike 10.1 found that Music
  Assistant stays connected to the Show around the clock, stopped or not, so without this the
  computer is turned away with 409 every time. Implement it in `feature/sendspin/listen.go`: a
  server that dials in while the one holding the room has no stream running takes the room, and
  the old one gets `client/goodbye` with reason `another_server`. The activity gate on the desktop
  side then gives the Show back by hanging up after `idle_s`, and Music Assistant reconnects.
- **A name per server in the Show's state sensor**, so Home Assistant shows "Playing from
  Office Mac".

## 4. Risks and open questions

1. **Lag for video on the computer.** 200 ms lead puts the sound behind the picture of a video
   playing on the computer. Measure in 10.1; if 100 ms holds on Wi-Fi, use that. Apps with an A/V
   offset (VLC, mpv) can make up the rest; browsers can't.
2. **Microphone prompt on macOS** for something that isn't a microphone may confuse. The text in
   `NSMicrophoneUsageDescription` must say why; the Mach service route in 11.2 removes it later.
3. **coreaudiod restart** on install and update cuts all sound for about a second; never do it
   without asking.
4. **Windows signing cost** decides whether 13.2 happens at all.
5. **The calls and voice assistant on the Show** take the speaker from Sendspin, as they do from
   Music Assistant; the sound from the computer is lost for that time, not delayed. That is the right
   behaviour, but say it in the docs.

## 5. Progress

- [~] 10.1 Spike: Sendspin from Go to the Show (discovery, 409, encode cost done; playback
  waits on the Show being free of Music Assistant, see docs/spikes.md)
- [x] 10.2 `internal/sendspin`
- [~] 10.3 Settings, tray and editor (built; the tone on the real Show waits as 10.1)
- [ ] 10.4 Show: a playing server takes the room from an idle one (techo5, §3)
- [ ] 11.1 macOS AudioServerPlugIn
- [ ] 11.2 Reader in `deckcap-mac`
- [ ] 11.3 Darwin `Tap`, end to end
- [ ] 11.4 macOS install and update
- [ ] 12.1 Linux null sink
- [ ] 12.2 Linux `Tap`
- [ ] 12.3 Linux packaging
- [ ] 13.1 Windows with VB-CABLE
- [ ] 13.2 Windows driver (optional)
