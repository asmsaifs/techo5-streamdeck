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
