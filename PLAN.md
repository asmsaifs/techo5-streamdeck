# TECHO5 Stream Deck — implementation plan

A desktop app for macOS, Windows and Linux that turns a TECHO5 Echo Show into a Stream Deck:
a grid of touch buttons that run actions on the computer (launch apps, open sites, send hotkeys,
media/volume, scripts, Home Assistant...), plus tiles that **stream a website or a desktop app,
with sound, onto the Show and take its touches back**, the way dashcast does for Home Assistant.

The plan is written to be built step by step with an AI coding assistant ("vibe coding"). Every
step has a goal, what to build, a "done when" check, and a prompt you can paste.

---

## 0. What already exists (and what we reuse)

| Piece | Where | What it gives us |
|---|---|---|
| dashcast server | `techo5/dashcast/` | Headless Chrome (chromedp) → page pictures → **only changed rectangles** as JPEG → device; device touches replayed on the page. Noise-encrypted with a shared key. |
| Device dashcast client | `techo5/echod/internal/feature/dashboard/stream.go`, `secure.go` | The Show **dials a server** (`Dashboard server` setting = `host:port` + key), sends a hello `{name,w,h,path,kiosk}`, then one JSON line per touch `{t:"tap"/"down"/"move"/"up", x, y}`. Draws `kindPicture (1)`, `kindProblem (2)`, `kindHalf (3)`. |
| Cast protocol + receiver | `techo5-cast/wire/`, `techo5/echod/internal/feature/cast/` | Sender→device JPEG frames **and 48 kHz S16LE stereo audio** with clock sync, latency buffer, late-frame dropping, mDNS, pairing code. No touch back-channel. |
| `castsend` | `techo5-cast/cmd/castsend` | Reference for timeline/stall handling and ffmpeg/yt-dlp video sending. |
| OTA release flow | techo5 fork | How device-side changes reach the Show (see own-update-channel docs). |

**Hardware limits that drive the design** (Echo Show 5, 2nd gen):
- Screen 960×480 landscape. Touch: single pointer events tap/down/move/up.
- JPEG decode is the bottleneck: full-size ≈ 12.5 fps; half-size (drawn doubled) = 30 fps at ~4 Mbit/s.
- So: **static UI sends nothing**, UI changes send small rects, motion goes half-size.

### Key decision: the desktop app is a "dashcast-compatible deck server"

The Show already knows how to connect to a dashcast server, show its pictures and send touches.
If the desktop app speaks exactly that protocol, **the first working version needs no device
change at all**: point the Show's *Dashboard server* at `<computer-ip>:9555` with the deck's key,
set Dashboard to *Streamed*, swipe in from the left edge → the deck appears.

Later phases extend the protocol (audio, a separate deck server setting, discovery). Those device
changes go into the techo5 repo and ship by OTA. Everything stays backward-compatible: a new
message kind is only sent after the device says it understands it.

Alternative considered: the cast direction (desktop dials the Show, cast already has pairing and
audio). Rejected for v1 because cast has no touch channel and is session-shaped ("Accept this
cast?"); a deck must be always there when you swipe. Keep it in mind for Phase 9.

---

## 1. Architecture

```
┌──────────────────────── Desktop app (Go core + Wails UI) ─────────────────────────┐
│                                                                                    │
│  Editor UI (React)  ◄── Wails bindings ──►  Core                                   │
│   grid editor, icons, actions,              ├─ store/      profiles/pages/buttons  │
│   window picker, device status              ├─ actions/    registry + OS impls     │
│                                             ├─ hotkeys/    global keyboard combos  │
│                                             ├─ sources/    what is on the screen:  │
│                                             │    deck  (Go-rendered grid)          │
│                                             │    web   (headless Chrome, chromedp) │
│                                             │    app   (native window capture)     │
│                                             ├─ screen/     diff → rects → JPEG     │
│                                             ├─ audio/      PCM 48k s16 stereo      │
│                                             ├─ server/     sessions, touch routing │
│                                             └─ wire/       Noise NNpsk0 framing    │
│                                                     ▲                              │
│   helpers/ (per OS, separate processes)             │ stdio frames + PCM           │
│    mac: Swift ScreenCaptureKit + audio taps ────────┘                              │
│    win: Windows.Graphics.Capture + WASAPI process loopback                         │
│    linux: X11/XComposite or PipeWire portal + Pulse/PipeWire sink                  │
└────────────────────────────────────┬───────────────────────────────────────────────┘
                                     │ TCP :9555, Noise NNpsk0 "techo5-dashcast/1"
                                     │  ◄── hello, touch lines
                                     │  ──► pictures (rects), half pictures, problem, [audio, clock]
                              ┌──────┴──────┐
                              │ Echo Show   │  echod dashboard stream client
                              │ (TECHO5)    │  (+ audio player in Phase 5)
                              └─────────────┘
```

### The `Source` abstraction (core idea)

Everything the Show can display is a `Source`. The server holds one active source per connected
device and swaps it when a button says "open website X" or "back to deck".

```go
type Source interface {
    Start(ctx context.Context, size image.Point) error
    Frames() <-chan Frame          // full-size RGBA frame + optional "motion" hint
    Audio() <-chan AudioChunk      // nil if silent; 48 kHz S16LE stereo, stamped
    Touch(ev TouchEvent)           // tap/down/move/up in device pixels
    Close() error
}
```

- **DeckSource**: renders the button grid in Go. Repaints only what changed (pressed button,
  toggled state, live tile). Instant tap feedback, no Chrome needed.
- **WebSource**: one headless Chrome tab at 960×480 (ported from dashcast). Touches replayed with
  CDP. Persistent profile per site so logins stick.
- **AppSource**: a native window captured by the OS helper, letterboxed to 960×480. Touches mapped
  to window coordinates and injected as mouse events.

`screen/` turns any frame stream into the wire: tile diff → merged dirty rects → JPEG; if >35 % of
the screen changed, send half-size and follow up with full-size 250 ms after motion stops (exactly
dashcast's `soft`/`settle` rule).

### Navigation on the Show

- Deck: tap button = run action. The Show sends a tap only when the finger lifts, with no
  duration, so there is no long-press until a device change adds one (see docs/spikes.md).
  Folder buttons open sub-pages; a reserved "Back" cell appears on sub-pages.
- Live view (web/app): a small "◀ Deck" chip is overlaid for 3 s when the view opens and again
  after any tap in the top 40 px; tapping it returns to the deck. (A swipe down from the top edge
  cannot be used: the device keeps drags that start in the top 40 px or the side 40 px strips.)
  Swipe in from the left edge is still the device's own "leave stream" gesture.

### Data model (stored as JSON)

Location: `os.UserConfigDir()/techo5-streamdeck/` → `config.json`, `icons/`, `chrome-profiles/`.

```json
{
  "version": 1,
  "server": { "listen": "0.0.0.0:9555", "key": "<random ≥16 chars>" },
  "devices": { "Kitchen Show": { "profile": "default" } },
  "profiles": {
    "default": {
      "grid": { "cols": 5, "rows": 3, "gap": 8, "radius": 14 },
      "theme": { "bg": "#101114", "button": "#1d1f24", "text": "#ffffff", "accent": "#4f8cff" },
      "pages": {
        "home": {
          "buttons": {
            "0,0": { "label": "YouTube", "icon": "lucide:youtube",
                     "action": { "type": "stream.web", "url": "https://youtube.com", "sound": "show" } },
            "1,0": { "label": "Mute", "icon": "lucide:mic-off",
                     "action": { "type": "toggle",
                                 "on":  { "type": "mic.mute", "mute": true },
                                 "off": { "type": "mic.mute", "mute": false } } },
            "2,0": { "label": "Apps", "icon": "lucide:folder", "action": { "type": "page", "page": "apps" } }
          }
        }
      }
    }
  },
  "hotkeys": { "CmdOrCtrl+Alt+1": { "profile": "default", "page": "home", "button": "0,0" } }
}
```

Grid defaults by screen: 960×480 → 5×3 (≈184×152 px cells) or 4×2 (big buttons) or 6×3.

### Action catalogue

| Group | Type | macOS | Windows | Linux |
|---|---|---|---|---|
| Navigation | `page`, `back`, `profile` | core | core | core |
| Open | `open.url`, `open.app`, `open.file` | `open`, `open -a` | `ShellExecute` / `start` | `xdg-open`, `gtk-launch` |
| Keyboard | `keys` (combo), `type` (text), `paste` (snippet) | CGEvent (Accessibility perm.) | `SendInput` | XTest (X11) / uinput or portal RemoteDesktop (Wayland) |
| Media | `media.play`/`next`/`prev` | NX system-defined events | VK_MEDIA_* | MPRIS over D-Bus (`playerctl` fallback) |
| Audio | `volume.set/up/down`, `volume.mute`, `mic.mute` | CoreAudio / `osascript` | Core Audio (IAudioEndpointVolume) | `wpctl` / `pactl` |
| System | `lock`, `sleep`, `screenshot` | `pmset`, `screencapture` | `LockWorkStation`, `SetSuspendState` | `loginctl`, portal |
| Scripts | `run` (command, args, cwd, timeout, show output) | shell | cmd/PowerShell | sh |
| Web/IoT | `http` (webhook), `ha.service` (Home Assistant), `obs` (OBS WebSocket v5) | core | core | core |
| Composite | `multi` (sequence with delays), `toggle` (two states) | core | core | core |
| Streaming | `stream.web` (url, sound: show/desktop/off), `stream.app` (app id / window title) | Phase 4/6 | Phase 4/6 | Phase 4/6 |
| Live tiles | `tile.clock`, `tile.cpu`, `tile.ha_state`, `tile.script` (label from command output) | core | core | core |

Every action returns `ok/err`; the deck flashes the button green/red (150 ms) and shows a toast
in the desktop app on error.

### Tech stack

| Concern | Choice | Why |
|---|---|---|
| Language (core) | **Go 1.23+** | Same as techo5, dashcast, cast wire: copy `secure.go`, the diff/JPEG code, chromedp browser code. |
| Desktop shell | **Wails v3** (beta; chosen in spike 0.5.1: tray, autostart and global shortcuts built in) | Go backend + web UI, small binaries, builds for all 3 OSes. |
| UI | React + TypeScript + Vite + Tailwind + **dnd-kit** (grid drag-drop) + shadcn/ui | Fast to vibe-code, good components. |
| Icons | Lucide/Tabler SVG set bundled, rasterized in Go with `oksvg`/`rasterx`; emoji; user PNG/JPG | Crisp at any cell size. |
| Drawing the deck | `github.com/fogleman/gg` (+ `golang.org/x/image/font/opentype` with Inter) | Simple 2D in pure Go. |
| Noise | `github.com/flynn/noise` | What dashcast/echod already use. |
| Browser | `github.com/chromedp/chromedp` + system Chrome/Edge/Chromium (or download Chrome for Testing) | dashcast-proven. |
| Global hotkeys | Wails v3 `app.GlobalShortcut` | mac (Carbon, no permission)/win/X11/Wayland portal. `golang.design/x/hotkey` needed Accessibility permission on macOS (spike 0.5.1). |
| Input injection | `github.com/go-vgo/robotgo` (keys/mouse/window) with per-OS fallbacks | One API for 3 OSes. |
| Clipboard | `golang.design/x/clipboard` | `paste` action. |
| mDNS | `github.com/grandcat/zeroconf` | Discovery (Phase 7). |
| Native capture helpers | Swift (mac), Rust `windows` crate or C++ (win), Go (linux) | Each OS's capture API is easiest from its native language; talk to Go over stdio. |
| Simulator | `cmd/fakeshow` with **Ebitengine** + `oto` audio | Develop without the Show in reach. |

---

## 2. Repository layout (target)

```
techo5-streamdeck/
  PLAN.md                 this file
  CLAUDE.md               rules for the coding assistant (Step 0.1)
  README.md
  go.mod                  module github.com/asmsaifs/techo5-streamdeck
  main.go                 Wails entry
  wails.json
  app.go                  Wails-bound API (thin; calls internal/*)
  internal/
    wire/                 secure.go (copy of dashcast/secure.go) + framing + kinds
    server/               listener, sessions, hello, touch lines, source switching
    screen/               tile diff, rect merge, JPEG, half-size/settle logic
    sources/
      deck/               DeckSource
      web/                WebSource (port of dashcast browser.go, guard.go, warm.go)
      app/                AppSource (talks to helpers)
    render/               button/grid drawing, icons, themes, golden tests
    deck/                 model types, validation, page navigation state per device
    store/                load/save/migrate config.json, icons dir, file watching
    actions/              registry.go, action_*.go, *_darwin.go / *_windows.go / *_linux.go
    hotkeys/
    audio/                PCM types, resampler, stamping/clock, ring buffer
    capture/              helper process client (spawn, stdio protocol, restart)
    discovery/            mDNS advertise (Phase 7)
  cmd/
    fakeshow/             device simulator (Ebitengine)
    decksrv/              headless server only (no UI) for debugging
  helpers/
    mac/                  Swift package → deckcap-mac
    win/                  deckcap-win.exe
    linux/                deckcap-linux (Go)
  frontend/               React app
  docs/
    protocol.md           deck protocol (dashcast v1 + extensions)
    actions.md
    permissions.md        per-OS permissions & troubleshooting
  .github/workflows/      build matrix
```

---

## 3. Phases and steps

Sizes: **S** ≈ one session, **M** ≈ 2–3 sessions, **L** ≈ a week of evenings.
Rule for every step: it ends with something you can run and see, and with tests where logic is
pure (diffing, wire, model, rendering).

### Phase 0 — Groundwork and spikes

**0.1 Repo + CLAUDE.md (S)**
- `git init`, `go mod init github.com/asmsaifs/techo5-streamdeck`, MIT licence, `.gitignore`.
- CLAUDE.md content (paste into the file):
  - Go core, keep packages small, `internal/` only; per-OS code in `_darwin.go/_windows.go/_linux.go`.
  - `internal/wire/secure.go` is a copy of `techo5/dashcast/secure.go` and **must stay
    byte-compatible** with `techo5/echod/internal/feature/dashboard/secure.go`.
  - Never send a message kind the device did not advertise.
  - Table-driven tests; golden PNGs for rendering under `testdata/`.
  - Comment style: full sentences explaining why, like the techo5 code.
  - Run `go vet ./... && go test ./...` before calling a step done.
- Done when: `go test ./...` passes on an empty skeleton; first commit.
- Prompt: *"Create the skeleton from PLAN.md §2 (folders with doc.go files only), go.mod, .gitignore, LICENSE (MIT), README stub and CLAUDE.md with the rules in Step 0.1."*

**0.2 Port the wire (S)**
- Copy `dashcast/secure.go` into `internal/wire/`, add `WriteMsg(kind, payload)` / touch-line
  reader, constants `KindPicture=1, KindProblem=2, KindHalf=3`, and the hello/touch types.
- Add an interop test: run a client handshake (copy of the device's `clientHandshake`) against the
  server in-process with the same key; wrong key must fail.
- Done when: tests pass; a 70 KB message spans records correctly.
- Prompt: *"Port techo5/dashcast/secure.go into internal/wire, keep the prologue 'techo5-dashcast/1' and psk derivation identical. Add framing helpers and an in-process interop test using a copy of the device's client handshake from techo5/echod/internal/feature/dashboard/secure.go."*

**0.3 `cmd/decksrv` hello-world against the real Show (S)**
- Server listens on `:9555`, accepts a device, reads hello, sends one full-screen JPEG (gradient +
  "Hello <name> w×h"), logs every touch line.
- On the Show: Settings/setup page → Dashboard server `<mac-ip>:9555`, key = the one printed;
  Dashboard = Streamed; swipe in from the left.
- Done when: the picture is on the Show and taps print coordinates on the Mac.
- macOS firewall will ask to allow incoming connections: allow.

**0.4 `cmd/fakeshow` simulator (M)**
- Ebitengine window 960×480 (flag `-size 1280x800`), connects like the device, sends hello,
  draws `picture`/`half`/`problem` messages, mouse → tap/down/move/up lines by the device's rules
  (tap on release if it moved ≤ 12 px; otherwise down/move/up; edge drags not sent — see
  docs/spikes.md), keyboard `Esc` = left-edge swipe (disconnect). `-shot` = headless check.
- Later (Phase 5) plays audio with `oto`.
- Done when: fakeshow shows the hello-world picture and taps print on decksrv.
- Prompt: *"Write cmd/fakeshow: an Ebitengine app that behaves like the TECHO5 dashboard stream client (see techo5/echod/internal/feature/dashboard/stream.go): handshake, hello JSON line, draw kindPicture/kindHalf (doubled)/kindProblem, send touch JSON lines from the mouse."*

**0.5 Spikes (decide before Phases 2, 5, 6) (M, timeboxed)**
Write findings into `docs/spikes.md`:
1. Wails v2 vs v3 on all three OSes: tray icon, hide-to-tray, start at login, and
   `golang.design/x/hotkey` working inside the Wails main loop on macOS.
2. Website audio capture — pick one per OS:
   - A. In-page: Chromium flags `--auto-accept-this-tab-capture --use-fake-ui-for-media-stream`, injected
     script calls `getDisplayMedia({preferCurrentTab:true, audio:true})`, AudioWorklet → PCM →
     WebSocket to the Go core. Uniform if it works in `--headless=new`.
   - B. OS loopback on the Chrome process tree: Windows WASAPI process loopback
     (`PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE`, Win10 2004+), macOS 14.2+ Core Audio process
     tap (`AudioHardwareCreateProcessTap`), Linux `PULSE_SINK=<null-sink>` env on Chrome + read the monitor.
3. macOS ScreenCaptureKit helper: capture one window + that app's audio, print fps.

### Phase 1 — Deck on the Show (no UI yet, config by hand) 

**1.1 Model + store (S)**
- Types in `internal/deck`: Profile, Page, Grid, Theme, Button, Action (type + raw JSON params).
- `internal/store`: load/save with defaults, generate a random key on first run, validation
  errors with paths ("profiles.default.pages.home.buttons.9,0: outside a 5×3 grid").
- Watch the file (fsnotify) and hot-reload.
- Done when: unit tests for load/validate/migrate; an example config in `testdata/`.

**1.2 Renderer (M)**
- `render.Grid(profile, page, size, state) *image.RGBA` and `render.Cell(...)` for one cell.
- Cell: rounded rect, icon (SVG rasterized / emoji / image) centered, label at the bottom
  (auto-shrink, 2 lines max, ellipsis), states: normal, pressed (darker + scale 0.96), toggled-on
  (accent ring), flash ok/err, disabled.
- Golden image tests (`go test -update` regenerates).
- Done when: goldens look right at 960×480 and 1280×800.

**1.3 Screen pipeline (M)**
- `screen.Encoder`: keeps last sent frame, tiles 32×32, finds changed tiles, merges into ≤N rects,
  encodes JPEG q85, emits `picture` messages; full frame on first send; half-size + settle rule.
- Port the logic from `dashcast/serve.go` instead of re-inventing.
- Tests: identical frame → no messages; one changed cell → one rect covering it; 60 % change →
  half message then full rects after settle.

**1.4 Server + DeckSource (M)**
- `server`: accept, handshake, read hello, map device name → profile, create session with nav
  state (page stack), run source → encoder → socket, read touches → source.
- DeckSource: hit-test cell from touch; `tap` → pressed state flashed on the cell and run the
  action (the device sends no `down` before a tap, see docs/spikes.md); `down`/`move`/`up` are
  drags and are ignored on the deck.
- Keepalive: none: the device never sets a read deadline after the hello (confirm on the Show in 0.3).
- Done when: fakeshow and the real Show show the grid; pressing animates; folders navigate.

**1.5 First actions (M)**
- Registry: `Register(type string, fn func(ctx, params json.RawMessage) error)`.
- Implement: `page`, `back`, `open.url`, `open.app`, `open.file`, `keys`, `type`, `run`, `multi`, `toggle`.
- `actions --dry-run` flag logs instead of executing (for tests).
- Done when: on the Show, a button opens youtube.com in the Mac's browser, another sends Cmd+Space,
  another runs `say hello`.

**Phase 1 milestone:** usable Stream Deck with hand-edited JSON. Commit + tag `v0.1.0`.

### Phase 2 — Desktop app (Wails)

**2.1 Wails shell + tray (M)**
- Move core startup into `app.go`; Wails window = editor; tray menu: Open editor, Pause deck,
  Quit. Closing the window hides to tray. Start at login toggle (LaunchAgent / registry Run key /
  `~/.config/autostart`).

**2.2 Grid editor (L)**
- Left: pages tree (add/rename/delete, sub-pages). Centre: grid exactly like the Show (aspect
  ratio of the selected device, uses the **same Go renderer** via a `PreviewPNG()` binding so
  preview == device). Right: inspector for the selected cell.
- Drag from an action palette onto a cell; drag cells to move/swap; copy/paste; undo/redo
  (command stack in the frontend); grid size and theme per profile.
- Save writes `config.json`; the running deck updates live (hot reload from 1.1).

**2.3 Inspector + icon picker (M)**
- Per action type a form generated from a JSON schema each action registers (label, type,
  required, enum, file picker, key-combo recorder, URL). One schema → UI form + validation.
- Icon picker: search Lucide/Tabler, emoji, upload image (cropped square, stored in `icons/`),
  icon/background colour, label font size.
- "Test" button runs the action from the desktop.

**2.4 Devices + settings (S)**
- Connected devices list (name, IP, resolution, current source, fps, bytes/s), profile per
  device, "show key / regenerate key", listen address (bind to a LAN IP), copy instructions
  ("On the Show: Dashboard server = 192.168.1.20:9555, key = …").

**Phase 2 milestone:** non-technical setup possible. Tag `v0.2.0`.

### Phase 3 — Global hotkeys, more actions, live tiles

**3.1 Global hotkeys (M)** — assign combos to any button in the inspector ("Hotkey" field with a
recorder); conflicts shown; Wayland: show "not supported, use the desktop's own shortcut settings
to call `techo5-streamdeck trigger <id>`" and add that CLI subcommand (talks to the running app
over a local socket — useful on every OS).

**3.2 Media, volume, mic, system actions (M)** — per-OS files; each with a unit test in dry-run
and a manual checklist in `docs/actions.md`.

**3.3 Integrations (M)** — `http` (method, URL, headers, body), `ha.service` (base URL + token
stored in OS keychain via `github.com/zalando/go-keyring`), `obs` (scene switch, record/stream
toggle via OBS WebSocket v5).

**3.4 Live tiles (M)** — tile sources tick (1 s / 5 s / on event) and repaint only their cell:
clock, CPU/RAM (`gopsutil`), HA entity state (websocket subscribe), script output, toggle that
reflects real state (e.g. mic muted?) by polling a "state" command.

**3.5 Profiles auto-switch (S)** — optional rule: foreground app → profile (mac
`NSWorkspace`, win `GetForegroundWindow`, X11 `_NET_ACTIVE_WINDOW`).

### Phase 4 — Website tiles (picture + touch)

**4.1 Port dashcast browser to WebSource (L)**
- Bring over `browser.go`, `warm.go`, `guard.go` and the capture loop; drop HA token logic; keep
  "only allowed pages" as a per-tile **domain allowlist** (default: the tile URL's registrable
  domain) so the Show can't wander off to arbitrary sites.
- One Chrome process, one tab per active web tile; persistent user-data dir per tile in
  `chrome-profiles/<tile-id>` (log in once on the desktop via an "Open login window" button that
  opens the same profile headed).
- Find Chrome/Edge/Chromium; if none, offer to download Chrome for Testing.
- Touch replay: dashcast's tap/drag/scroll mapping.

**4.2 Live view chrome (S)** — the "◀ Deck" chip overlay, top-edge swipe back, loading spinner
picture, problem text when the page fails.

**4.3 Video-friendly mode (M)** — per tile "video" flag: always half-size, cap at 25 fps, JPEG q70;
otherwise UI mode (rects, q85). Auto-detect: if >35 % changes for >2 s, switch to video mode.

**4.4 Keep tabs warm (S)** — keep the last N (2) web tiles alive for instant return; idle tabs
close after 10 min; memory shown in the devices panel.

**Phase 4 milestone:** YouTube/Spotify Web/HA dashboards on the Show, scroll and tap working,
no sound yet. Tag `v0.3.0`. (Note: an HA dashboard is now just a web tile — the deck covers
what dashcast did.)

### Phase 5 — Sound on the Show (first device change)

**5.1 Protocol extension (S, both repos)** — write `docs/protocol.md`:
- Device hello gains `"caps": ["audio1"]` (old devices send no caps → server never sends audio).
- New server→device kinds: `kindAudio = 4` (8-byte µs stamp + S16LE 48 kHz stereo PCM, ≤ 20 ms
  chunks) and `kindClock = 5` (8-byte stamp, ~1/s); same timing rules as cast protocol §Time
  (present at `stamp + offset + latency_ms`, drop late audio, never more than latency ahead).
- Server→device `kindHello = 6` JSON `{latency_ms}` optional.
- Pictures stay unstamped (UI latency matters more than lip-sync for web tiles); for video mode,
  delay audio by measured picture latency (configurable `av_offset_ms`).

**5.2 Device side in techo5 (M)** — in `feature/dashboard/stream.go`: advertise `audio1`, handle
kinds 4/5 by feeding the **cast audio player** (`feature/cast/audio.go`; factor a shared player
package if needed), stop audio when the stream closes or another app takes the speaker (calls,
alarms, voice assistant ducking as cast does). Tests in the device repo; release via the OTA flow.

**5.3 Desktop audio pipeline (M)** — `audio/`: resample (any rate → 48 kHz, mono→stereo), 20 ms
chunker, stamp with monotonic clock, clock message every second, backpressure (drop oldest when
>latency queued).

**5.4 Website audio capture (L)** — implement the method chosen in spike 0.5.2 per OS. Per tile
`sound: show | desktop | off`; when `show`, mute the desktop output of that Chrome instance.

**5.5 fakeshow audio (S)** — play kinds 4/5 with `oto` so audio can be tested away from the Show.

**Phase 5 milestone:** YouTube on the Show with sound. Tag `v0.4.0`.

### Phase 6 — Streaming native apps (window + sound + touch)

**6.1 Helper protocol (S)** — `docs/helpers.md`. Go spawns `deckcap-<os>`:
- stdin: JSON lines `{"cmd":"list"}`, `{"cmd":"start","window":id,"fps":25,"audio":true,"maxw":960}`,
  `{"cmd":"input","kind":"down","x":..,"y":..}`, `{"cmd":"stop"}`.
- stdout: framed `[len u32][kind u8][payload]`: `1` = window list JSON, `2` = frame (w,h, BGRA or
  JPEG), `3` = PCM 48k s16 stereo stamped, `4` = event/error JSON.
- Go side restarts a crashed helper and surfaces errors in the UI.

**6.2 macOS helper (L)** — Swift package: ScreenCaptureKit `SCShareableContent` for the list,
`SCStream` with a desktop-independent window filter (works when the window is covered),
`capturesAudio` limited to the app; input via `CGEvent` posted at the window's screen coordinates
(Accessibility permission) — optionally `CGEventPostToPid` for background clicks. Ship inside the
`.app` bundle; permissions: Screen Recording + Accessibility, with a guided check screen.

**6.3 Windows helper (L)** — Windows.Graphics.Capture (`CreateForWindow`, borderless where
supported), WASAPI process loopback for that app's process tree; input via `SendInput` after
`SetForegroundWindow` (or `PostMessage` mode for apps that accept it). Elevated windows can't be
driven from a non-elevated app: detect and explain.

**6.4 Linux helper (L)** — X11: XComposite + XShm grab of the window, XTest input. Wayland:
`xdg-desktop-portal` ScreenCast (user picks the window once; keep the restore token) +
RemoteDesktop portal for input; audio by moving the app's sink-input to a per-session null sink
(`pactl move-sink-input`) and reading its monitor (PipeWire-pulse works the same).

**6.5 AppSource + UI (M)** — window picker in the inspector (live thumbnails from the helper),
`stream.app` action (match by bundle id / exe name / window title regex, optionally launch the
app if not running), letterbox + scale to 960×480 keeping aspect, map touches back through the
letterbox, drag = mouse drag, two-finger not available (single pointer) → offer a "scroll mode"
toggle chip that turns vertical drags into wheel events.

**Phase 6 milestone:** stream e.g. Spotify desktop or a game launcher to the Show with sound and
control it by touch. Tag `v0.5.0`.

### Phase 7 — Device-side polish (techo5 repo, OTA)

- **Separate deck server setting** (`deck_server` + key) so HA dashcast and the deck can coexist;
  open the deck with a swipe in from the **right** edge (dashboard keeps the left).
- **Discovery**: desktop advertises `_techo5deck._tcp` (TXT: name, version); the Show's settings
  list found decks; key entered on the device setup page (or a 6-digit code the desktop shows,
  confirmed on the device, then the real key is exchanged inside a Noise XXpsk handshake).
- **Deck when idle** option in place of the clock; screen wake on touch shows the deck directly.
- Click sound/haptic on press played locally on the device (no round-trip).
- Reconnect backoff + "Deck offline" picture with last state greyed out.

### Phase 8 — Packaging, CI, releases

- GitHub Actions matrix (macos-14 arm64 + x86_64/universal, windows-latest, ubuntu-22.04):
  `go test`, frontend lint/test, `wails build`, build helpers, upload artifacts.
- macOS: universal `.app`, hardened runtime, entitlements (screen capture, apple events),
  `NSScreenCaptureUsageDescription`, codesign + notarize (`notarytool`), DMG.
- Windows: NSIS installer from Wails, optional code-signing cert (else SmartScreen warning),
  firewall rule for the listen port added by the installer.
- Linux: AppImage + `.deb`; udev rule docs for uinput if used.
- In-app update check against GitHub releases (signed checksums, same ed25519 idea as the techo5
  updater).
- Docs: README quickstart with screenshots, `docs/permissions.md` per OS.

### Phase 9 — Later ideas

- Plugin actions: a folder with `manifest.json` + executable, JSON over stdio (like Stream Deck
  SDK, simpler); schema-driven inspector reuses 2.3.
- Several Shows at once, each with its own profile and source (sessions already per device).
- On-screen keyboard overlay for web tiles (desktop renders it, injects text with CDP).
- Cast-direction mode (desktop dials the Show via `_techo5cast._tcp`) for Shows without a deck
  server configured.
- Native-drawn deck on the device (send layout + icons once, device draws; zero-latency press)
  if streamed latency ever feels slow.
- Stream Deck profile import (`.streamDeckProfile` is a zip of JSON + PNGs).

---

## 4. Security (non-negotiable)

A deck server can **run commands on your computer** when a button is pressed. Treat it like
remote code execution on the LAN:

- Noise NNpsk0 with a ≥16-char random key (refuse to start otherwise). Key stored in the OS
  keychain, never logged.
- The device can only **press configured buttons** and touch the active stream; it can never
  send an action definition, a command, a URL or a file path. The protocol has no such message.
- Bind to a LAN address; never suggest port forwarding. Optional allowlist of device IPs.
- Web tiles: per-tile domain allowlist; Chrome profiles are separate from your own browser.
- App tiles: input is clipped to the streamed window's rectangle; nothing outside it can be
  clicked. Streaming stops if the window closes.
- `run` actions show the exact command in the inspector and require a confirm on first save.

## 5. Performance budget

| Situation | Target |
|---|---|
| Static deck | 0 bytes/s after first frame |
| Tap → pressed picture on Show | < 80 ms on Wi-Fi |
| Tap → action starts on desktop | < 50 ms after touch line arrives |
| Web UI scrolling | half-size rects, ≥ 20 fps, full-size within 250 ms after stop |
| Video (web/app) | 480×240 half-size, 25–30 fps, ≈ 3–4.5 Mbit/s |
| Audio | 48 kHz stereo S16 ≈ 1.5 Mbit/s, 0 underruns in a 10-min run |
| Desktop CPU | deck idle < 1 %; video stream < 15 % of one core (JPEG encode) |

Use `image/jpeg` first; if encode is too slow, switch to `github.com/pixiv/go-libjpeg`
(libjpeg-turbo, cgo) behind the same interface.

## 6. Testing strategy

- Unit: wire framing/handshake interop, diff/rect merge, model validation, renderer goldens,
  action registry (dry-run), audio resampler/chunker.
- Integration: `go test ./internal/server -run E2E` starts a server and an in-process fake device,
  presses buttons, asserts actions fired and pictures received.
- Manual per phase: checklist in `docs/testing.md` run on fakeshow + the real Show; each OS
  checked in a VM (UTM for Linux/Windows on the Mac).
- Device repo changes keep the techo5 rules: Linux-only component tests via `workflow_dispatch`
  before tagging.

## 7. How to drive the vibe coding

1. One step per session. Start with: *"Read PLAN.md and CLAUDE.md. Implement Step X.Y only."*
2. Ask for a short design note first for M/L steps, approve it, then code.
3. End each step with: tests green, the "done when" check performed on fakeshow (and on the Show
   when it touches the device), a commit `step X.Y: …`.
4. Keep `PLAN.md` live: tick steps off below, write surprises into `docs/spikes.md`.

### Progress

- [x] 0.1 Repo + CLAUDE.md
- [x] 0.2 Port the wire
- [ ] 0.3 decksrv hello-world on the Show — code done and checked with fakeshow; real Show check open
- [x] 0.4 fakeshow simulator
- [ ] 0.5 Spikes (Wails/hotkeys, web audio, ScreenCaptureKit) — macOS done (docs/spikes.md); Windows, Linux and the hands-on tray/hotkey check open
- [x] 1.1 Model + store
- [x] 1.2 Renderer — line and image icons; emoji icons still draw the fallback glyph (needs a colour font)
- [x] 1.3 Screen pipeline
- [x] 1.4 Server + DeckSource — checked with fakeshow and in tests; real Show check open (as 0.3)
- [x] 1.5 First actions → **v0.1.0** (not tagged yet: waiting for the real Show check from 0.3/1.4)
- [x] 2.1 Wails shell + tray — serves the deck and quits cleanly (smoke-tested); tray menu, hide-on-close and start-at-login need a hands-on check
- [x] 2.2 Grid editor — pages, drag-drop grid, preview from the Go renderer, undo/redo, copy/paste, grid and theme; seen in the real window. Nested-folder tree and per-device preview size open
- [x] 2.3 Inspector + icon picker — schema-driven forms, key recorder, Test button, run-command confirm on save, icon picker (Lucide search + upload). Emoji icons still not drawn; icon/background colour and label size open
- [x] 2.4 Devices + settings → **v0.2.0** (not tagged yet, same wait as v0.1.0 for the real Show check). Live fps/bit rate, profile per Show, new/delete profile, listen address, key show/regenerate, copyable setup text
- [ ] 3.1 Global hotkeys + `trigger` CLI
- [ ] 3.2 Media/volume/mic/system
- [ ] 3.3 HTTP / Home Assistant / OBS
- [ ] 3.4 Live tiles
- [ ] 3.5 Profile auto-switch
- [ ] 4.1 WebSource (dashcast port)
- [ ] 4.2 Live view chip + back gesture
- [ ] 4.3 Video mode
- [ ] 4.4 Warm tabs → **v0.3.0**
- [ ] 5.1 Protocol extension doc
- [ ] 5.2 Device audio (techo5, OTA)
- [ ] 5.3 Desktop audio pipeline
- [ ] 5.4 Website audio capture
- [ ] 5.5 fakeshow audio → **v0.4.0**
- [ ] 6.1 Helper protocol
- [ ] 6.2 macOS helper
- [ ] 6.3 Windows helper
- [ ] 6.4 Linux helper
- [ ] 6.5 AppSource + window picker → **v0.5.0**
- [ ] 7 Device polish (deck_server, discovery, idle deck)
- [ ] 8 Packaging + CI → **v1.0.0**

## 8. Open questions to settle early

1. Echo Show 5 only, or also Show 8 (1280×800)? The design handles any `w×h` from the hello; the
   grid defaults per resolution.
2. Wails v2 (stable, tray via a small lib) or v3 (native tray/multi-window)? Decide in spike 0.5.1.
3. Windows helper language: Rust (`windows` crate) vs C++. Rust recommended for safety and cargo builds in CI.
4. Should "sound on the Show" ever duplicate to the desktop speakers too? Default: no (move, not copy).
