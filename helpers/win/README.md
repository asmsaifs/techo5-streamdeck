# deckcap-win

Windows capture helper, protocol in [docs/helpers.md](../../docs/helpers.md). Go source in
[cmd/deckcap-win](../../cmd/deckcap-win), shared code in [internal/helperkit](../../internal/helperkit).

    GOOS=windows GOARCH=amd64 go build -o deckcap-win.exe ./cmd/deckcap-win

No cgo: it cross-compiles from any OS.

- **Picture:** `PrintWindow(PW_RENDERFULLCONTENT)` into a DIB section, so a covered window still
  draws. This replaces the Windows.Graphics.Capture route in the plan (WinRT from Go without cgo is
  a lot of untestable plumbing for the same picture); a minimized window keeps its last frame.
- **Sound:** WASAPI process loopback of the window's process tree (Windows 10 2004 or later), asked
  for as 48 kHz S16 stereo. On older systems the picture works and an `unsupported` event says why
  there is no sound.
- **Input:** `SetCursorPos` + `SendInput` after raising the window; the click is dropped (with an
  event) when another window still covers the point. An elevated target cannot be driven from a
  non-elevated helper (UIPI): the helper sends a `permission` event once.

Written and cross-compiled on macOS; **not yet run on a Windows machine**.
