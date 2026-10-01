package actions

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenKind is what Open is asked to open.
type OpenKind int

const (
	OpenURL  OpenKind = iota // a web address, or any URL the OS has a handler for
	OpenApp                  // an application by name
	OpenFile                 // a file or folder, with whatever the OS opens it with
)

// ExecSpec is a command to run.
type ExecSpec struct {
	Command string
	Args    []string
	Dir     string
	Shell   bool // Command is a line for the shell (sh -c, or cmd /C) and Args are ignored
	Wait    bool // wait for it to end and fail if it fails; otherwise start it and let it go
}

// System is what the actions do to the computer. The registry takes one so tests and --dry-run
// can swap it for one that only records.
type System interface {
	Open(ctx context.Context, kind OpenKind, target string) error
	Keys(ctx context.Context, c Combo) error
	Type(ctx context.Context, text string) error
	Exec(ctx context.Context, e ExecSpec) error
}

// OS is the System of the computer this runs on.
func OS() System { return osSystem{} }

type osSystem struct{}

func (osSystem) Open(ctx context.Context, kind OpenKind, target string) error {
	return openTarget(ctx, kind, target)
}

func (osSystem) Keys(_ context.Context, c Combo) error     { return tapKeys(c) }
func (osSystem) Type(_ context.Context, text string) error { return typeText(text) }

func (osSystem) Exec(ctx context.Context, e ExecSpec) error {
	var cmd *exec.Cmd
	switch {
	case e.Shell && shellCmd != "":
		if e.Wait {
			cmd = exec.CommandContext(ctx, shellCmd, append(shellArgs, e.Command)...)
		} else {
			cmd = exec.Command(shellCmd, append(shellArgs, e.Command)...)
		}
	case e.Wait:
		cmd = exec.CommandContext(ctx, e.Command, e.Args...)
	default:
		// Not tied to ctx: the caller's context ends as soon as the action returns, and would
		// kill a program that was meant to keep running.
		cmd = exec.Command(e.Command, e.Args...)
	}
	cmd.Dir = e.Dir
	if !e.Wait {
		cmd.SysProcAttr = detached()
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait() // reap it when it ends
		return nil
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{b: &out}, &limited{b: &out}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", e.Command, ctx.Err())
		}
		return fmt.Errorf("%s: %w%s", e.Command, err, tail(out.Bytes()))
	}
	return nil
}

// limited keeps the first 64 KB a command writes, which is plenty for an error message and stops a
// chatty command from filling memory.
type limited struct{ b *bytes.Buffer }

func (l *limited) Write(p []byte) (int, error) {
	if room := 64<<10 - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// tail is the end of a command's output as ": ..." for an error, or nothing.
func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if len(s) > 300 {
		s = "…" + s[len(s)-300:]
	}
	return ": " + s
}

// expand resolves a leading ~ to the home folder.
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// DryRun is a System that logs what would be done and does none of it.
func DryRun(log *slog.Logger) System { return dryRun{log} }

type dryRun struct{ log *slog.Logger }

func (d dryRun) Open(_ context.Context, k OpenKind, target string) error {
	d.log.Info("dry run: open", "kind", [...]string{"url", "app", "file"}[k], "target", target)
	return nil
}

func (d dryRun) Keys(_ context.Context, c Combo) error {
	d.log.Info("dry run: keys", "key", c.Key, "mods", c.Mods)
	return nil
}

func (d dryRun) Type(_ context.Context, text string) error {
	d.log.Info("dry run: type", "chars", len([]rune(text)))
	return nil
}

func (d dryRun) Exec(_ context.Context, e ExecSpec) error {
	d.log.Info("dry run: run", "command", e.Command, "args", e.Args, "dir", e.Dir, "shell", e.Shell, "wait", e.Wait)
	return nil
}
