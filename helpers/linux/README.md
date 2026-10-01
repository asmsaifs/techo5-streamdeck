# deckcap-linux

Linux capture helper, protocol in [docs/helpers.md](../../docs/helpers.md). Go source in
[cmd/deckcap-linux](../../cmd/deckcap-linux), shared code in [internal/helperkit](../../internal/helperkit).

    GOOS=linux GOARCH=amd64 go build -o deckcap-linux ./cmd/deckcap-linux

No cgo: it cross-compiles from any OS (pure-Go X11 client, `github.com/jezek/xgb`).

- **Windows:** the window manager's `_NET_CLIENT_LIST` (root children when there is none).
- **Picture:** Composite's off-screen pixmap of the window (`RedirectAutomatic` +
  `NameWindowPixmap`), read with plain `GetImage`, so a covered window still draws. XShm would cut
  the copy cost for large windows and is not used yet.
- **Input:** XTest motion and buttons; wheel is buttons 4/5. The window is raised first and the click
  is dropped (with an event) if another window still covers the point.
- **Sound:** needs `pactl` and `parec` (PulseAudio or PipeWire-pulse). The app and its child
  processes are moved to a private null sink, its monitor is read, and everything is put back on
  stop. The app is silent on the local speakers while it streams.
- **Wayland:** native Wayland windows are not reachable; the helper answers `unsupported` when
  there is no X display. The portal route (ScreenCast + RemoteDesktop) is still open.

Written and cross-compiled on macOS; **not yet run on Linux**.
