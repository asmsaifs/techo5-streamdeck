# TECHO5 Stream Deck

A desktop app for macOS, Windows and Linux that turns a TECHO5 Echo Show into a Stream Deck: a
grid of touch buttons that run actions on the computer, plus tiles that stream a website or a
desktop app, with sound, onto the Show and take its touches back.

The desktop app speaks the dashcast protocol, so a Show running TECHO5 needs no change to show
the deck: set its *Dashboard server* to `<computer-ip>:9555` with the deck's key and set
Dashboard to *Streamed*.

Status: early work. See [PLAN.md](PLAN.md) for the design and the progress list.

## Try the hello-world server

```sh
go run ./cmd/decksrv            # prints the key to enter on the Show
go run ./cmd/fakeshow -key <key> # a simulated Show on this computer
```

## Licence

MIT, see [LICENSE](LICENSE).
