# Permissions per OS

What the app asks for, and which feature needs it. Nothing is needed to run the deck server and
press buttons that start apps or open URLs; the rest is asked for when a button first needs it.

## macOS

| Permission | Needed for | Where |
|---|---|---|
| Screen Recording | `stream.app` (showing an app's window and its sound on the Show) | System Settings → Privacy & Security → Screen & System Audio Recording. The capture helper `deckcap-mac` is inside the app, so allow *TECHO5 Stream Deck*. |
| Accessibility | key presses, clicks and touch input into streamed windows; `key`/`type` actions | Privacy & Security → Accessibility |
| Automation (Apple Events) | AppleScript and app-control actions | asked the first time an action controls another app |
| Microphone | microphone mute/unmute button | asked on first use |
| Local Network | finding Shows and being found (`_techo5deck._tcp`) | asked on first start; allow it |

Global hotkeys need no permission. The app is a menu-bar app (no Dock icon): *Start at login* is in
its menu. If a permission was refused, reset it with `tccutil reset ScreenCapture io.github.asmsaifs.techo5-streamdeck`
and start the app again.

An unsigned build (no Developer ID) is blocked by Gatekeeper the first time: right-click the app →
Open.

## Windows

- **Firewall**: the installer offers to add an inbound rule for the app on private networks, so the
  Show can connect. Declined or portable: allow it when Windows asks on the first start, or run
  `netsh advfirewall firewall add rule name="TECHO5 Stream Deck" dir=in action=allow program="<path>\techo5-streamdeck.exe" profile=private`.
- **SmartScreen**: unless the installer was signed, Windows shows "unknown publisher". *More info →
  Run anyway*.
- Streaming an app cannot send input to a window of an elevated (administrator) program: run the
  app as administrator too, or do not stream that program.
- A covered or minimised window cannot be touched; the helper raises it first.

## Linux

- **Streaming apps** works with X11 and XWayland windows. A native Wayland window is not captured
  (the helper answers `unsupported`); run the app through XWayland or use `stream.web`.
- **Sound of a streamed app** needs PulseAudio or PipeWire-pulse and `parec` (package
  `pulseaudio-utils`).
- **Global hotkeys** use X11 key grabs, or the XDG GlobalShortcuts portal on Wayland.
- **uinput**: not used today (clicks go through XTest). The `.deb` ships
  `/lib/udev/rules.d/70-techo5-uinput.rules` for sources that need it later; the AppImage does not,
  copy the file by hand if you need it.
- The AppImage needs FUSE 2 (`libfuse2`) and WebKitGTK 4.1 on the host.

## Where files and secrets go

Config and the key are in the user config folder (`techo5-streamdeck/`); passwords and tokens of
actions are in the OS keychain (Keychain, Credential Manager, Secret Service).

## Updates

The tray menu's *Check for updates* (and a daily check) reads `checksums.txt` and
`checksums.txt.sig` from the latest GitHub release and accepts them only when the release key
signed them. It tells you a newer version exists and opens the release page; it installs nothing.
