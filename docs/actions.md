# Actions

An action is what a button does: `{"type": "...", ...parameters}` in `config.json`. Try a deck out
with `decksrv -dry-run`, which logs what each action would do and does none of it.

| Type | Parameters | Notes |
|---|---|---|
| `page` | `page` | Opens a sub-page. The deck adds a Back button to a page that has none. |
| `back` | | Goes back one page. |
| `open.url` | `url` | Needs a scheme (`https://`, `spotify:`...). macOS `open`, Windows `rundll32 url.dll`, Linux `xdg-open`. |
| `open.app` | `app` | macOS `open -a`, Windows `start` (names with `& \| < > ^ %` are refused), Linux `gtk-launch`, else the program by name. |
| `open.file` | `path` | `~` is the home folder. Opens with the OS's default. |
| `keys` | `keys` | `"Cmd+Space"`, `"CmdOrCtrl+Shift+4"`, `"F5"`, `"Ctrl+Alt+Delete"`. CmdOrCtrl is Cmd on a Mac and Ctrl elsewhere. |
| `type` | `text` | Types the text as keystrokes. |
| `run` | `command`, `args`, `cwd`, `timeout` (s, 30), `shell`, `detach` | `command` + `args` run directly, so no argument is read by a shell. `shell: true` runs `command` as a shell line. `detach: true` starts it and does not wait, for a program that stays open. A failure shows the end of the command's output in the log. |
| `delay` | `ms` | 0 to 60000. For use in `multi`. |
| `multi` | `steps` | Runs the steps in order and stops at the first that fails. |
| `toggle` | `on`, `off` | Runs `on` when off and `off` when on; flips only if it worked. The deck ringed in the accent colour when on. Starts off and does not look at the computer, so it is out of step if the thing is changed another way. |

A button flashes green when its action worked and red when it failed (the reason is in the log).
The Show sends a tap only when the finger lifts, so the "pressed" look is a short flash after the
tap, not held while touching.

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
