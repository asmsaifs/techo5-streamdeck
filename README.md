# TECHO5 Stream Deck

A desktop app for macOS, Windows and Linux that turns a TECHO5 Echo Show into a Stream Deck: a
grid of touch buttons that run actions on the computer, plus tiles that stream a website or a
desktop app, with sound, onto the Show and take its touches back.

The desktop app speaks the dashcast protocol, so a Show running TECHO5 needs no change to show
the deck: set its *Dashboard server* to `<computer-ip>:9555` with the deck's key and set
Dashboard to *Streamed*.

## Install

Download the installer for your computer from the
[latest release](https://github.com/asmsaifs/techo5-streamdeck/releases/latest): a `.dmg` (macOS,
universal), a `Setup.exe` (Windows) or an `.AppImage` / `.deb` (Linux). What each OS asks permission
for is in [docs/permissions.md](docs/permissions.md).

1. Start the app: it lives in the tray (menu bar). *Open editor* shows the button grid.
2. Open the editor's Shows panel: it shows the address and key.
3. On the Show: setup page → *Deck server* (or *Look for decks*), enter the key, then swipe in from
   the left edge to open the deck.

Releases are built by GitHub Actions (`.github/workflows/release.yml`) from a `vX.Y.Z` tag; the
local scripts are in `packaging/` (`macos/build.sh`, `windows/build.ps1`, `linux/build.sh`). The
tray's *Check for updates* verifies a signature made with the release key (`cmd/relsign`).

Status: early work. See [PLAN.md](PLAN.md) for the design and the progress list.

## Try the hello-world server

```sh
go run ./cmd/decksrv            # prints the key to enter on the Show
go run ./cmd/fakeshow -key <key> # a simulated Show on this computer
```

## Licence

MIT, see [LICENSE](LICENSE).
