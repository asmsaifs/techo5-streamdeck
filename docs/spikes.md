# Spikes and surprises

Findings from Phase 0, with what they change in PLAN.md. Spike code lives under `spikes/`.
Everything below was run on macOS 26.6.2 (Apple Silicon) on 2026-10-01; Windows and Linux are
still to check.

## The Show's touches (found while writing fakeshow, step 0.4)

Read from `techo5/echod/internal/hardware/touch/touch.go` and
`internal/feature/display/dashboard.go` (`dashGesture`). The dashboard puts the touch screen in
*follow* mode, and then:

| Finger | Lines sent |
|---|---|
| Lifts having moved ≤ 12 px (`followMove`) | one `tap` at where it landed, **on release only**, however long it was held (the Show 5 has no holds: `holdGestures = false`) |
| Moves > 12 px | `down` at where it landed, `move`s, `up` where it lifts |
| A drag that starts at x < 40 or x ≥ w−40 (scaled by w/960), or y < 40 | **nothing**: the device keeps it (left = leave the stream, right = drawer, top = settings sheet). A tap there is still sent. |

What that changes:

- **No press feedback before release, no long-press.** A tap arrives only when the finger lifts
  and carries no duration. DeckSource (1.4) should draw the pressed state *on* the tap and flash
  it, not on `down`. Long-press (secondary action) needs a device change: e.g. a `hold1` cap in
  Phase 7 that sends `down` at once and `tap` with a duration. Until then, drop it from 1.4.
- **"Swipe down from the top edge" can never reach the server**: the device turns it into its
  settings sheet. The way back to the deck from a live view has to be a tap, e.g. the "◀ Deck"
  chip (4.2), which works anywhere including the edge strips, or a drag that starts inside the
  page.
- The device sends no keepalive and never sets a read deadline after the hello, so a static deck
  can send nothing for as long as it likes (1.4's keepalive question: none needed). Still to
  confirm on the real Show in 0.3.

## 1. Desktop shell: Wails v3 (spike `spikes/wails3`, nested module)

- Wails v3 is still beta (`v3.0.0-beta.26`, v2 is at 2.16.0). It builds with a plain `go build`
  (no Wails CLI needed for the spike; the CLI is for packaging), with only linker warnings about
  the macOS deployment target.
- Tray (`app.SystemTray`), hide-on-close (`WindowClosing` hook + `Cancel`), accessory activation
  policy (no Dock icon) and **start-at-login** (`app.Autostart`: SMAppService in a bundled .app on
  macOS 13+, LaunchAgent otherwise; registry/autostart on Windows/Linux) are all built in.
- **Global hotkeys are built in too** (`app.GlobalShortcut.Register("CmdOrCtrl+Alt+1", fn)`):
  Carbon `RegisterEventHotKey` on macOS (no permission needed), `RegisterHotKey` on Windows,
  `XGrabKey` on X11, and the XDG GlobalShortcuts portal on Wayland. Registered without error
  inside the Wails loop.
- `golang.design/x/hotkey` registered on the main thread via `application.InvokeSync` **failed**:
  "grant the application Accessibility (Input Monitoring) permission". It would add a permission
  prompt that v3's own shortcuts don't need.

**Decision: Wails v3, and its GlobalShortcut instead of golang.design/x/hotkey.** Pin the beta
version and re-check before Phase 2. Wayland hotkeys come for free via the portal (3.1 can still
add the `trigger` CLI).

Not yet checked by hand (needs a person at the screen): the tray icon showing and its menu
working, the window hiding rather than quitting, the hotkey actually firing (osascript is not
allowed to send keystrokes here). Run `go run ./spikes/wails3 -quit 60s` from `spikes/wails3`,
close the window, use the tray menu, press Cmd+Alt+1.

## 2. Website audio: option A works (spike `spikes/webaudio`)

The page calls `getDisplayMedia({video: true, preferCurrentTab: true, audio: {...}})`, Chrome is
started with `--auto-accept-this-tab-capture`, and an AudioWorklet measures the captured audio.

| Browser | headless | Result |
|---|---|---|
| Edge (system) | `new`, old, headed | tab audio track, 48 kHz, signal present |
| Chrome for Testing 153 | `new`, old | same |

- **Do not pass `--use-fake-ui-for-media-stream`**: with it Chrome picks the screen, not the tab,
  and fails with `NotReadableError: Could not start video source` (it would need Screen Recording
  permission). `--auto-accept-this-tab-capture` alone is right.
- No click on the page is needed: `Runtime.evaluate` with `userGesture: true` counts as the user
  gesture `getDisplayMedia` wants.
- Ask for `echoCancellation: false, autoGainControl: false, noiseSuppression: false,
  channelCount: 2`; the defaults are mono with call processing (AGC halved the level).
- **`suppressLocalAudioPlayback: true` is honoured** (`local_echo=false`): the tab's sound stops
  coming out of the computer's speakers while captured. That is PLAN §8 Q4's "move, not copy"
  with no OS work at all.
- The AudioContext can run at 48 kHz and resamples the track (which reports 44.1 kHz), so PCM
  leaves the page already at the rate the Show wants.

**Decision: option A on every OS** (it is all inside Chrome). Option B (OS loopback) stays a
fallback if a site defeats it.

Open risks for 5.4: a full navigation kills the injected script and its stream (re-inject on
`Page.frameNavigated` and re-run with `userGesture`; SPAs like YouTube rarely navigate fully);
DRM'd audio (Netflix, Spotify Web's Widevine) may capture as silence; check that tab capture
keeps going while the tab is hidden behind another tab.

## Step 5.4: website sound, as built (`internal/sources/web/sound.go`)

Option A as the spike found it, with three things only a real tile showed:

- **`--mute-audio` makes the capture silent.** chromedp's default flags include it, so the browser
  has to be started with `mute-audio=false` explicitly. What keeps a page off the computer's
  speakers is `suppressLocalAudioPlayback` while it is captured, so *every* tile except
  `sound: "desktop"` captures; `off` captures and drops. A page that plays before its capture
  starts (a second at most) can be heard on the computer.
- **A captured tab gets a "sharing this tab" bar**, which takes 57 px off the page area, so every
  screencast frame came out 960x423 and was dropped as the wrong size (the page still worked; the
  Show saw nothing change). `--disable-infobars` removes it.
- **Stop the capture's video track** (`getVideoTracks().forEach(t => t.stop())`); the audio
  carries on and the page is not asked for frames it will not use.
- **YouTube's content security policy refuses the `blob:` worklet module** (`Failed to load worklet
  module script`), so capture failed and the page played on the Mac. The script now falls back to a
  `ScriptProcessorNode`, which loads nothing; checked on a real YouTube video (peak near full
  scale) and with a CSP test page.
- PCM goes from the page to Go through a `Runtime.addBinding` (random name per tab), base64, one
  20 ms block per call; no WebSocket and no port.
- A full navigation starts the capture again from `Page.domContentEventFired` (the script is
  `AddScriptToEvaluateOnNewDocument`); a parked tab stops it and a reused one starts it.
- Tested with Chrome 154 on macOS (tone page: peak near full scale; survives a navigation).
  Not yet tried: Windows, Linux, DRM'd audio (Spotify Web, Netflix), a tab hidden behind another.

## 3. macOS window + app audio capture: ScreenCaptureKit works (spike `spikes/sckit`)

`swiftc -O -parse-as-library -o bin/sckit spikes/sckit/main.swift && bin/sckit <app or title>`

- Capturing an Edge window with `SCContentFilter(desktopIndependentWindow:)`, 960 wide, 30 fps
  cap: **30 frames/s steadily at 959×546**, while the window was partly covered.
- `capturesAudio` with a window filter gives the **owning app's** audio (all of Edge, not one
  tab): 48 000 samples/s, **48 kHz, 2 ch, Float32, non-interleaved** (flags 41). The helper must
  convert to S16LE interleaved.
- A command-line helper must call `CGMainDisplayID()` before using SCStream, or it dies with
  `Assertion failed: (did_initialize), function CGS_REQUIRE_INIT`.
- Listing windows worked here because the parent process already had Screen Recording permission;
  a shipped helper will need its own grant (6.2's guided check screen).
- Every frame came back `.complete`: SCK did not mark unchanged frames idle for this window, so
  the Go side's tile diff (1.3) is what keeps a static window at 0 bytes/s.

## Step 1.2: renderer

- **Lucide dropped its brand icons** (youtube, github, ...), so `lucide:youtube` in the plan's
  example config draws the fallback glyph (a question mark in a circle). The bundled set is
  lucide-static 1.49.0 (2121 icons, `internal/render/lucide/`, about 1 MB). A brand button needs an
  image in `icons/` for now; the icon picker (2.3) should say so.
- **Emoji are not drawn yet.** Go has no colour-emoji renderer; they get the fallback glyph. Options
  when it matters: embed a monochrome Noto Emoji, or rasterize through the OS in a helper.
- **Font is Go Bold** (from `golang.org/x/image`), not Inter: it is already in the module cache and
  needs no licence file. Swapping is one line in `render.New`.
- Cell and Grid pixels match to within 2 levels, not exactly: the rasterizer rounds antialiased
  edges differently at another offset. Invisible, and fine for repainting one cell.

## Step 4.1: website tiles (headless Chrome)

Found while porting dashcast's browser code to a desktop browser (Edge, Chrome should be the same;
only Edge was at hand), all checked by tests that run a real browser:

- **A new tab is a hidden page and streams nothing.** The screencast of a tab opened beside
  another gave zero frames; `Page.bringToFront` fixes one tab, but tabs of one window then starve
  each other. Each tile gets a window of its own (`Target.createTarget` with `newWindow`) and is
  brought to the front.
- **Frames are of the real window's page area, not the emulated viewport.** A window made 960×480
  has a page area of 952×349 and the frames came that size. `fitWindow` reads the window's frame
  size and sets the bounds so the page area is exactly the screen; frames of any other size are
  dropped rather than drawn in the wrong place.
- **A page that stands still sends no frame**, so a Show would wait for ever for its first
  picture. One screenshot is sent right after the page loads (dashcast does the same for a tab it
  picks up again).
- **`Input.dispatchTouchEvent` never returns** in this Chrome (new headless), with or without
  touch emulation; mouse events return at once. So a tap is press+release and a drag is the wheel.
  `Input.dispatchMouseEvent` with `mouseMoved` alone waits 5 s for a frame, so no hover move is
  sent before a click. dashcast's touch events worked in `headless-shell`, the old headless.
- **A refused navigation shows Chrome's error page** over the page. The tap on a bad link is
  stopped in the page (a capture-phase click handler); what gets past that (script, form,
  redirect) is failed in `Fetch` and the tab goes back one page.
- `chromedp.Cancel` panics if called twice, and the profile folder is written to until the process
  has exited: browser shutdown waits for it and runs once.
- Not done: Chrome download if none is installed (4.4), a Windows/Linux check (compiles only),
  `Fetch` checks only the main frame, so a frame inside an allowed page can show anything.

## Step 10.1: Sendspin from Go to the Show (spike `spikes/sendspin`)

Run on 2026-10-08 against the Echo Show 5 2nd gen (192.168.1.181, TECHO5 v1.3.x) over Wi-Fi, with
the sender in `internal/sendspin` (the spike is a thin loop over it).

- **Discovery works**: `_sendspin._tcp` finds the Show as `Echo Show 5 2nd gen` at
  `ws://192.168.1.181:8928/sendspin` (TXT `name=`, `path=/sendspin`) within a second.
- **A second server gets 409, as expected** ("already connected"), and the Group reports the Show
  busy once and does not dial it again until the sound next starts.
- **Music Assistant never lets go.** The Show's log shows Music Assistant (192.168.1.208)
  connected for a day at a time, through `group state=stopped`, and back within 20 s of any
  drop. So it is not "busy while Music Assistant plays": with Music Assistant on the network the
  computer is turned away **always**, and the desktop's activity gate cannot change that.
  docs/speaker.md §3 *Taking over from Music Assistant* is therefore required, not a nicety: the
  Show must let a server that starts a stream take the room from one that is connected but idle
  (the spec's ranking by activity, with `client/goodbye` reason `another_server` to the old one).
  Until then the speaker works only on a Show that Music Assistant does not hold (the player
  disabled in Music Assistant, or no Music Assistant).
- **FLAC encode** (`BenchmarkFLACEncode`, M-series Mac): 137 µs per 20 ms chunk, 0.7 % of one
  core per Show; a tone codes to 752 bytes a chunk against 3840 for PCM. The whole spike (tone
  generation, gate, mDNS) used 1.1 % of one core.
- **Gate**: the Show was let go 5.0 s after the sound stopped (`idle_s` 5).
- **Played, with Music Assistant stopped** (disabling the player in Music Assistant was not enough:
  its Sendspin side still dialed in). FLAC was chosen, Show log `late=0 dropped=0` over 20 s at
  200 ms, `lead_ms` 196-198 on arrival, drift held within 2 ms.
- **Time to connect**: 360 ms from the first sound (100 ms gate, mDNS answered at once, handshake,
  clock burst). It was 2.2 s while the resolve waited out a fixed 2 s browse; it now stops at the
  first answer.
- **Lead**: 100 ms gave 7 late chunks in 30 s. 150 ms was clean for 45 s, then a Wi-Fi stall of
  several seconds cut the margin to 75 ms and 206 chunks came late. The Show's output plays about
  85 ms behind what it is handed (`queued_ms` against `lead_ms`), so the usable margin is the lead
  less that. **200 ms stays the default.**
- **Start skip**: chunks held from before the connection were replayed with as little as 20 ms
  left, and the Show anchored the stream on a late one: a 115 ms skip at every start. Only those
  with half the lead left are replayed now; the start then corrects by 39 ms, the clock filter
  settling.
- Still to do: the 10-minute listening run.
