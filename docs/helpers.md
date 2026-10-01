# Capture helper protocol

Each OS has one helper executable (`deckcap-mac`, `deckcap-win.exe`, `deckcap-linux`) that lists
windows, captures one of them with its sound, and injects mouse input into it. The Go core
(`internal/capture`) starts it as a child process and talks over stdio. The helper holds no
state that outlives a `start`: the core can kill and restart it at any time.

## Commands: core → helper (stdin, one JSON object per line)

| Line | Meaning |
|---|---|
| `{"cmd":"list"}` | Reply with a window list (kind 1). |
| `{"cmd":"start","window":"<id>","fps":25,"maxw":960,"audio":true}` | Capture that window. Frames are scaled down to at most `maxw` wide, aspect kept. `audio` adds the owning app's sound. A second `start` replaces the first. |
| `{"cmd":"input","kind":"down","x":120,"y":80}` | Mouse input. `kind` is `tap`, `down`, `move`, `up` or `wheel` (then `dy` pixels, positive scrolls down). `x`,`y` are in the pixels of the last frame sent, origin top left; the helper maps them to the window and **clips to its rectangle**. |
| `{"cmd":"stop"}` | Stop capturing. The helper keeps running. |

stdin closing means quit. Unknown commands are answered with an `error` event, never ignored.

## Messages: helper → core (stdout, binary frames)

```
[len u32 LE][kind u8][payload: len bytes]
```

`len` counts the payload only and is at most 16 MiB; a larger value is a protocol error and the
core restarts the helper.

| Kind | Payload |
|---|---|
| 1 window list | JSON `[{"id","title","app","w","h"}]`. `id` is opaque (CGWindowID, HWND, X11 id or portal token) and only valid while the window lives. |
| 2 frame | `w u16 LE, h u16 LE`, then `w*h*4` bytes of BGRA, row after row, no padding. Sent when the helper has a new picture; it may send fewer than `fps`. |
| 3 audio | `rate u32 LE, channels u8`, then S16LE interleaved PCM. Any rate and channel count: the core resamples (`internal/audio`) and does the 20 ms chunking and stamping, so the helper sends what the OS gave it, converted to S16LE. |
| 4 event | JSON `{"event":"ready"\|"started"\|"stopped"\|"error", "code":"...", "msg":"..."}`. |

The helper sends `ready` once when it can take commands. Error codes the core understands:
`permission` (Screen Recording / Accessibility missing, `msg` says which), `no_window` (the id is
gone: the core stops the stream and returns to the deck), `unsupported`, `internal`.

stderr is free text for logs; the core copies it to its own log.

## Restarts

If the helper exits or breaks the framing, the core starts it again after 1 s, 2 s, 4 s ... capped
at 10 s, waits for `ready`, and sends the last `start` again if a capture was active. After five
failures in a row it gives up and reports the last event or exit status to the UI.
