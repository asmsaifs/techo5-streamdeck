# Rules for the coding assistant

Read PLAN.md first. Implement one step at a time and tick it off in PLAN.md's progress list.

- Go core. Keep packages small and under `internal/` only. Per-OS code goes in files named
  `_darwin.go`, `_windows.go` and `_linux.go`.
- `internal/wire/secure.go` is a copy of `techo5/dashcast/secure.go` and **must stay
  byte-compatible** with `techo5/echod/internal/feature/dashboard/secure.go`: same prologue
  (`techo5-dashcast/1`), same psk derivation, same record framing.
- Never send a message kind the device did not advertise. Old devices know kinds 1-3 only.
- The device can only press configured buttons and touch the active stream. Never add a message
  that lets it send an action, a command, a URL or a file path.
- Tests are table-driven. Rendering tests compare against golden PNGs under `testdata/`
  (`go test -update` regenerates them).
- Comment style: full sentences explaining why, like the techo5 code.
- Run `go vet ./... && go test ./...` before calling a step done.
