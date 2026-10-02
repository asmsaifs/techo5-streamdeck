# Actions

An action is what a button does: `{"type": "...", ...parameters}` in `config.json`. Try a deck out
with `decksrv -dry-run`, which logs what each action would do and does none of it.

| Type | Parameters | Notes |
|---|---|---|
| `page` | `page` | Opens a sub-page. The deck adds a Back button to a page that has none. |
| `back` | | Goes back one page. |
| `stream.web` | `url`, `allow` (more sites it may use), `profile` | Shows a website on the Show: see [Website tiles](#website-tiles). Not run from a hotkey or `trigger`, and the Test button leaves it out: it is the Show's screen that changes. |
| `open.url` | `url` | Needs a scheme (`https://`, `spotify:`...). macOS `open`, Windows `rundll32 url.dll`, Linux `xdg-open`. |
| `open.app` | `app` | macOS `open -a`, Windows `start` (names with `& \| < > ^ %` are refused), Linux `gtk-launch`, else the program by name. |
| `open.file` | `path` | `~` is the home folder. Opens with the OS's default. |
| `keys` | `keys` | `"Cmd+Space"`, `"CmdOrCtrl+Shift+4"`, `"F5"`, `"Ctrl+Alt+Delete"`. CmdOrCtrl is Cmd on a Mac and Ctrl elsewhere. |
| `type` | `text` | Types the text as keystrokes. |
| `run` | `command`, `args`, `cwd`, `timeout` (s, 30), `shell`, `detach` | `command` + `args` run directly, so no argument is read by a shell. `shell: true` runs `command` as a shell line. `detach: true` starts it and does not wait, for a program that stays open. A failure shows the end of the command's output in the log. |
| `media.play`, `media.next`, `media.prev` | | macOS and Windows press the system's media keys (the active player gets them; macOS needs the Accessibility permission). Linux runs `playerctl` (install it). |
| `volume.up`, `volume.down` | | 6 % per press. |
| `volume.set` | `level` (0-100) | Windows has no way to read or set the level, so it steps to 0 and counts up in 2 % steps. |
| `volume.mute` | `mute` (true, false, or empty to flip) | Windows can only flip. |
| `mic.mute` | `mute` (as above) | macOS has no input mute: it sets the input level to 0 and puts the old level back. Linux `wpctl`/`pactl`. **Not on Windows yet** (needs the Core Audio API). |
| `lock`, `sleep` | | macOS lock presses Ctrl+Cmd+Q (Accessibility), sleep is `pmset sleepnow`. Windows `rundll32`. Linux `loginctl lock-session`, `systemctl suspend`. |
| `screenshot` | | Saves a PNG to the Desktop (macOS) or Pictures (Linux: grim, gnome-screenshot, spectacle or scrot). Windows opens the Snipping Tool (Win+Shift+S). |
| `http` | `method` (GET), `url`, `headers` ("Name: value" each), `body`, `timeout` (s, 10) | A webhook. Only http and https. Worked if the status is below 400; the start of the reply is the error otherwise. A failing request never puts the URL's query (where keys often are) in the log. |
| `ha.service` | `service` ("light.toggle"), `entity`, `data` (a JSON object) | POSTs to Home Assistant's `/api/services/<domain>/<service>`. The address is in Settings > Integrations, the long-lived token is in the system keychain. |
| `obs` | `command` (`scene`, `record.toggle/start/stop`, `stream.toggle/start/stop`, `input.mute.toggle`), `name` (scene or input) | OBS Studio's WebSocket v5 server (built in since OBS 28; Tools > WebSocket Server Settings). Address in Settings, password (if OBS has one) in the keychain. Connects for each press. |
| `delay` | `ms` | 0 to 60000. For use in `multi`. |
| `multi` | `steps` | Runs the steps in order and stops at the first that fails. |
| `toggle` | `on`, `off` | Runs `on` when off and `off` when on; flips only if it worked. The deck ringed in the accent colour when on. Starts off and does not look at the computer, so it is out of step if the thing is changed another way, unless the button has a `state` live tile (below). |

A button flashes green when its action worked and red when it failed (the reason is in the log).
The Show sends a tap only when the finger lifts, so the "pressed" look is a short flash after the
tap, not held while touching.

## Website tiles

`stream.web` opens the page in a headless Chrome on this computer and streams it to the Show as
pictures (only what changed, as on the deck); the Show's touches go back to it. It needs Chrome,
Edge, Brave or Chromium installed (`TECHO5_CHROME` names one if none is found).

- **Touch**: a tap is a click, a drag scrolls (the page follows the finger). Sliders cannot be
  dragged, only tapped. The page is the size of the Show's screen, so it lays out as a desktop site.
- **Back**: a small "‹" tab on the left edge, half way down, goes back one page in the site's
  history when tapped (the tap does not reach the page).
- **Refresh**: pulling down from the top of the page (a drag of 200 px or more that starts with the
  page scrolled to its top) loads the page again. It is the way out of a page that has stuck, such
  as a paused video or a half drawn layout.
- **Log**: the app writes `deck.log` in the config folder (`deck.log.1` is the one before it).
  Page errors are there, as `the page failed` and `sound` lines.
- **Leaving**: a "Deck" chip shows in the top left corner for 3 seconds when the page opens and
  again after a tap within 40 px of the top edge (that tap reaches the page too). A tap on the chip
  closes the page and returns to the deck. Swiping in from the left edge leaves the stream
  altogether, as for any stream.
- **Which sites**: the page may only be at the URL's own registered domain (`youtube.com` for
  `www.youtube.com`), and at the domains listed in `allow`. A tap on a link anywhere else does
  nothing; a script or redirect that goes elsewhere is stopped and the page put back. Sign-ins that
  live on another domain need it in `allow` (Google sign-in: `google.com`).
- **Sign-ins**: each `profile` is a separate browser profile in `chrome-profiles/` next to the
  config, never your own browser's. Empty `profile` means one per site, shared by its buttons. To
  sign in, open the same site on the Show and log in by touch (a "login window" on the desktop
  comes with step 4.4).
- **Video**: pictures are normally only what changed, sharp, and go half size while most of the
  screen moves. With `video: true` every change goes at half size, JPEG quality 70 and at most 25
  pictures a second (the Show decodes about 30 half-size frames a second). Without it, the same
  mode switches on by itself after two seconds of the whole page moving and off after three
  seconds of stillness. Paused video is sharpened as soon as it stops.
- **Warm pages**: leaving a page parks its window (stream stopped, page frozen) instead of closing
  it. Opening the same button again within 10 minutes gets it back at once without loading. Two
  are kept; a third closes the oldest. A browser goes when its last window does. The Shows panel
  lists the browsers, their windows and memory.
- **No sound yet** (Phase 5): the browser is muted.
- A page that cannot open leaves the deck as it was and flashes the button red; the reason is in
  the log.

## Permissions

- **macOS**: `keys` and `type` need *Accessibility* (System Settings → Privacy & Security). The
  first press asks; the app that runs the deck (Terminal, while using `decksrv`) is the one to allow.
- **Linux**: `keys` and `type` work on X11. Wayland needs the portal or libei backend robotgo
  selects; not checked yet.
- **Windows**: nothing to allow, but a window of an app run as administrator does not take keys from
  a deck that is not.

## Checked so far

macOS only: every action above through `decksrv -dry-run`, and `run` for real in tests. Real key and
text injection, and the Windows and Linux openers, are untested on a real machine.

## Global hotkeys and `trigger`

A button can have a **global hotkey**: set it in the editor's inspector ("Global hotkey", press the
keys to record). It is stored in `config.json` under `hotkeys`, by combo, and presses the button
from anywhere on the computer, as if it had been tapped on the Show. Folder and Back buttons cannot
have one: they only move around on a Show's screen.

The system may refuse a combo (another program owns it; a key it cannot register). The editor
shows the reason under the field after saving.

`techo5-streamdeck trigger <id>` does the same from a terminal, a script, or a desktop's own
shortcut settings (use it where global hotkeys are not available, such as Wayland without the
GlobalShortcuts portal). The id is a hotkey of the config (`CmdOrCtrl+Alt+1`, any letter case) or
`profile/page/col,row` (`default/home/0,0`). It talks to the running app over `control.sock` in the
config folder (mode 0600) and exits 1 with the app's reason if the press failed. Like a Show, it
can press only buttons the config has.

Manual check (needs a person at the screen): save a hotkey for an "Open site" button, press it
with another app in front; run `techo5-streamdeck trigger default/home/0,0` from a terminal.

### Manual checklist for 3.2

On each OS press the button from the editor's Test and see it work: play/pause with a player
open, next/previous, volume up/down/set 30, mute and unmute, mic mute and unmute (check in the
system's sound settings), screenshot file appears, and last lock and sleep. Checked so far: macOS
volume read/up/down against the real system; everything else only by tests of the commands.

### Integrations and secrets

`config.json` holds the Home Assistant and OBS **addresses** (`integrations`). The **token and
password** are in the OS keychain (service `techo5-streamdeck`: Keychain, Credential Manager, or
the Secret Service on Linux), are set from Settings > Integrations, and never come back to the
window or the config. With `-dry-run` these three actions only log. Checked for real so far: a
webhook to a local server and the macOS keychain; OBS and Home Assistant are tested against fake
servers (OBS's password hash is checked against the protocol document's example).

## Live tiles

Any button can have a `tile`: its big text is a value read again every few seconds, drawn where
the icon would be (the label stays below). Set it in the inspector under "Live tile". The button
can still have an action.

| `tile.type` | Shows | Default interval |
|---|---|---|
| `clock` | the time; `format` is `24h`, `12h`, `24h-seconds` or `date` | 1 s |
| `cpu`, `ram` | load and memory in use, in percent | 2 s, 5 s |
| `ha_state` | a Home Assistant `entity`'s state with its unit ("21.5 °C"); address and token as for `ha.service` | 5 s |
| `script` | the first line `command` (with `args`, or a `shell` line) prints, cut to 60 characters | 10 s |
| `state` | nothing: whether the command says the button's toggle is really on (exit 0 and not "no", "false", "off" or "0") | 5 s |

`every` (1 to 3600 s) changes the interval. Only the page a Show is on is read, a reading is shared
by every Show that has it, and the picture is only resent when a value changed. A tile that cannot
be read shows "—" and the reason is in the log once. A `state` tile makes a `toggle` button flip
from what is true, which fixes the "out of step" caveat of toggles. Home Assistant is polled, not
subscribed to. With `-dry-run`, script and state tiles do not run their command.

Checked for real: clock, cpu, ram, script and state tiles over the wire to the fake Show. Home
Assistant tiles only against a fake server.

## Profile by application (auto-switch)

`autoSwitch` in `config.json` (editor: Shows > Profile by application) gives a Show another profile
while an application is in front on this computer:

```json
"autoSwitch": [{"app": "Zoom", "profile": "meeting"}, {"app": "code.exe", "profile": "dev", "device": "Desk Show"}]
```

`app` is the application's name or id in any letter case ("Safari", "com.apple.Safari", "chrome",
"chrome.exe"); `device` limits a rule to one Show. The first matching rule wins; with none a Show
uses its own profile. The Show goes to the new profile's home page. With no rules nothing is polled.
This app being in front (to edit a rule) never switches anything. The editor's Detect button waits
4 seconds for you to switch to the application, then fills it in.

| OS | How the front application is found | Notes |
|---|---|---|
| macOS | `lsappinfo` (Launch Services) | No permission needed. Checked for real. |
| Windows | `GetForegroundWindow`, then the process's exe name | Compiles and vets; **untested on Windows**. |
| Linux X11 | `xprop` (`_NET_ACTIVE_WINDOW`, `WM_CLASS`) | Needs xprop. **Untested.** |
| Linux Wayland | not possible: a window cannot see another | Only XWayland windows answer; the rest says it cannot tell, once, in the log. |
