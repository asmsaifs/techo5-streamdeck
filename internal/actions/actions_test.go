package actions

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// fake is a System that records what it was asked and can be made to fail.
type fake struct {
	mu       sync.Mutex
	calls    []string
	err      error
	execs    []ExecSpec
	keys     []Combo
	controls []Control
}

func (f *fake) rec(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	return f.err
}

func (f *fake) Open(_ context.Context, k OpenKind, t string) error {
	return f.rec([...]string{"url", "app", "file"}[k] + " " + t)
}
func (f *fake) Keys(_ context.Context, c Combo) error {
	f.keys = append(f.keys, c)
	return f.rec("keys " + c.Key)
}
func (f *fake) Control(_ context.Context, c Control) error {
	f.controls = append(f.controls, c)
	return f.rec("control " + c.Kind)
}
func (f *fake) Type(_ context.Context, s string) error { return f.rec("type " + s) }
func (f *fake) Exec(ctx context.Context, e ExecSpec) error {
	f.mu.Lock()
	f.execs = append(f.execs, e)
	f.mu.Unlock()
	if d, ok := ctx.Deadline(); ok && time.Until(d) < 0 {
		return ctx.Err()
	}
	return f.rec("exec " + e.Command)
}

func act(t *testing.T, js string) *deck.Action {
	t.Helper()
	var a deck.Action
	if err := a.UnmarshalJSON([]byte(js)); err != nil {
		t.Fatal(err)
	}
	return &a
}

func TestParseCombo(t *testing.T) {
	tests := []struct {
		in      string
		goos    string
		want    Combo
		wantErr string
	}{
		{"Cmd+Space", "darwin", Combo{"space", []string{"cmd"}}, ""},
		{"CmdOrCtrl+Shift+4", "darwin", Combo{"4", []string{"cmd", "shift"}}, ""},
		{"CmdOrCtrl+Shift+4", "windows", Combo{"4", []string{"ctrl", "shift"}}, ""},
		{"ctrl+alt+delete", "linux", Combo{"delete", []string{"ctrl", "alt"}}, ""},
		{"Option+Esc", "darwin", Combo{"escape", []string{"alt"}}, ""},
		{"Win+E", "windows", Combo{"e", []string{"cmd"}}, ""},
		{"F5", "linux", Combo{"f5", nil}, ""},
		{"Shift+F12", "linux", Combo{"f12", []string{"shift"}}, ""},
		{"Cmd++", "darwin", Combo{"+", []string{"cmd"}}, ""},
		{"Cmd+Cmd+A", "darwin", Combo{"a", []string{"cmd"}}, ""},
		{" Return ", "darwin", Combo{"enter", nil}, ""},
		{"", "darwin", Combo{}, "no keys"},
		{"Cmd+", "darwin", Combo{}, "empty part"},
		{"Hyper+A", "darwin", Combo{}, "not a modifier"},
		{"Cmd+Spacebar", "darwin", Combo{}, "not a key"},
		{"F25", "darwin", Combo{}, "not a key"},
	}
	for _, tt := range tests {
		got, err := parseCombo(tt.in, tt.goos)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%q: error %v, want %q", tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q on %s = %+v, %v; want %+v", tt.in, tt.goos, got, err, tt.want)
		}
	}
}

func TestSimpleActions(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    string
		wantErr string
	}{
		{"url", `{"type":"open.url","url":"https://youtube.com"}`, "url https://youtube.com", ""},
		{"app", `{"type":"open.app","app":"Safari"}`, "app Safari", ""},
		{"file", `{"type":"open.file","path":"/tmp/x.txt"}`, "file /tmp/x.txt", ""},
		{"keys", `{"type":"keys","keys":"Cmd+Space"}`, "keys space", ""},
		{"type", `{"type":"type","text":"hello"}`, "type hello", ""},
		{"run", `{"type":"run","command":"say","args":["hello"]}`, "exec say", ""},
		{"url without a scheme", `{"type":"open.url","url":"youtube.com"}`, "", "no scheme"},
		{"url that is an option", `{"type":"open.url","url":"-a Calculator"}`, "", `starts with "-"`},
		{"app that is an option", `{"type":"open.app","app":"--help"}`, "", `starts with "-"`},
		{"missing url", `{"type":"open.url"}`, "", "url is missing"},
		{"bad keys", `{"type":"keys","keys":"Hyper+A"}`, "", "not a modifier"},
		{"empty text", `{"type":"type","text":""}`, "", "text is missing"},
		{"no command", `{"type":"run"}`, "", "command is missing"},
		{"unknown", `{"type":"teleport"}`, "", `no action "teleport"`},
		{"wrong parameter type", `{"type":"open.url","url":5}`, "", "bad parameters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fake{}
			err := New(f, nil).Run(context.Background(), act(t, tt.json))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want %q", err, tt.wantErr)
				}
				if len(f.calls) != 0 {
					t.Errorf("a refused action still did %v", f.calls)
				}
				return
			}
			if err != nil || len(f.calls) != 1 || f.calls[0] != tt.want {
				t.Errorf("did %v, %v; want %q", f.calls, err, tt.want)
			}
		})
	}
}

func TestRunParameters(t *testing.T) {
	f := &fake{}
	r := New(f, nil)
	ctx := context.Background()
	if err := r.Run(ctx, act(t, `{"type":"run","command":"ls | wc","shell":true,"cwd":"/tmp","detach":true,"timeout":5}`)); err != nil {
		t.Fatal(err)
	}
	want := ExecSpec{Command: "ls | wc", Dir: "/tmp", Shell: true, Wait: false}
	if got := f.execs[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("exec %+v, want %+v", got, want)
	}
	r.Run(ctx, act(t, `{"type":"run","command":"x","args":["a b","$HOME"]}`))
	if got := f.execs[1]; !got.Wait || !reflect.DeepEqual(got.Args, []string{"a b", "$HOME"}) {
		t.Errorf("exec %+v", got)
	}
	// ~ in cwd goes to the home folder.
	r.Run(ctx, act(t, `{"type":"run","command":"x","cwd":"~/work"}`))
	if got := f.execs[2].Dir; strings.HasPrefix(got, "~") || !strings.HasSuffix(got, "work") {
		t.Errorf("cwd %q", got)
	}
}

func TestMulti(t *testing.T) {
	f := &fake{}
	r := New(f, nil)
	err := r.Run(context.Background(), act(t, `{"type":"multi","steps":[
		{"type":"open.app","app":"Notes"},{"type":"delay","ms":10},{"type":"keys","keys":"Cmd+N"},{"type":"type","text":"hi"}]}`))
	if err != nil || !reflect.DeepEqual(f.calls, []string{"app Notes", "keys n", "type hi"}) {
		t.Errorf("did %v, %v", f.calls, err)
	}

	// A failing step stops the rest and is named.
	f2 := &fake{}
	err = New(f2, nil).Run(context.Background(), act(t, `{"type":"multi","steps":[
		{"type":"open.app","app":"Notes"},{"type":"keys","keys":"Hyper+N"},{"type":"type","text":"hi"}]}`))
	if err == nil || !strings.Contains(err.Error(), "step 2") || len(f2.calls) != 1 {
		t.Errorf("did %v, %v", f2.calls, err)
	}
	if err := New(f, nil).Run(context.Background(), act(t, `{"type":"multi","steps":[]}`)); err == nil {
		t.Error("an empty multi ran")
	}
}

func TestDelayStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	err := New(&fake{}, nil).Run(ctx, act(t, `{"type":"delay","ms":30000}`))
	if err == nil || time.Since(start) > 2*time.Second {
		t.Errorf("delay: %v after %v", err, time.Since(start))
	}
	if err := New(&fake{}, nil).Run(context.Background(), act(t, `{"type":"delay","ms":-1}`)); err == nil {
		t.Error("a negative delay ran")
	}
}

func TestToggle(t *testing.T) {
	f := &fake{}
	r := New(f, nil)
	tg := act(t, `{"type":"toggle","on":{"type":"type","text":"on"},"off":{"type":"type","text":"off"}}`)
	ctx := WithButton(context.Background(), ButtonKey("default", "home", deck.Cell{Col: 1}))
	key := ButtonKey("default", "home", deck.Cell{Col: 1})
	if key != "default/home/1,0" || r.On(key) {
		t.Fatalf("key %q on=%v", key, r.On(key))
	}
	for i, want := range []string{"type on", "type off", "type on"} {
		if err := r.Run(ctx, tg); err != nil || f.calls[i] != want {
			t.Fatalf("press %d: %v, %v", i, f.calls, err)
		}
		if r.On(key) != (i%2 == 0) {
			t.Errorf("after press %d on=%v", i, r.On(key))
		}
	}
	// A failure leaves the state where it was, so the next press tries the same side again.
	f.err = errors.New("no")
	if err := r.Run(ctx, tg); err == nil || !r.On(key) {
		t.Errorf("a failed toggle: %v on=%v", err, r.On(key))
	}
	// Another button is another toggle; and without a button there is none to flip.
	if r.On("default/home/2,0") {
		t.Error("toggles share state")
	}
	f.err = nil
	if err := r.Run(context.Background(), tg); err == nil {
		t.Error("a toggle ran with no button")
	}
	if err := r.Run(ctx, act(t, `{"type":"toggle","on":{"type":"type","text":"x"}}`)); err == nil {
		t.Error("a toggle with no off ran")
	}
}

func TestDryRunDoesNothing(t *testing.T) {
	r := New(DryRun(slog.New(slog.DiscardHandler)), nil)
	for _, js := range []string{
		`{"type":"open.url","url":"https://example.com"}`, `{"type":"open.app","app":"X"}`,
		`{"type":"keys","keys":"Cmd+Q"}`, `{"type":"type","text":"x"}`, `{"type":"run","command":"rm","args":["-rf","/"]}`,
	} {
		if err := r.Run(context.Background(), act(t, js)); err != nil {
			t.Errorf("%s: %v", js, err)
		}
	}
}

func TestTypesAreListed(t *testing.T) {
	got := strings.Join(New(&fake{}, nil).Types(), " ")
	for _, want := range []string{"keys", "multi", "open.app", "open.file", "open.url", "run", "toggle", "type"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not registered: %s", want, got)
		}
	}
}

// The real command runner, on Unix: arguments are not read by a shell, failures say what the
// command said, and a timeout stops a command that does not end.
func TestRealExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	sys := OS()
	ctx := context.Background()
	if err := sys.Exec(ctx, ExecSpec{Command: "true", Wait: true}); err != nil {
		t.Error(err)
	}
	err := sys.Exec(ctx, ExecSpec{Command: "sh", Args: []string{"-c", "echo boom >&2; exit 3"}, Wait: true})
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("failure: %v", err)
	}
	// "$HOME" and ";" are one argument, not a shell's.
	err = sys.Exec(ctx, ExecSpec{Command: "test", Args: []string{"$HOME;", "=", "$HOME;"}, Wait: true})
	if err != nil {
		t.Errorf("arguments were interpreted: %v", err)
	}
	if err := sys.Exec(ctx, ExecSpec{Command: "echo hi | grep -q hi", Shell: true, Wait: true}); err != nil {
		t.Errorf("shell line: %v", err)
	}
	if err := sys.Exec(ctx, ExecSpec{Command: "definitely-not-a-program", Wait: true}); err == nil {
		t.Error("a missing program succeeded")
	}
	if err := sys.Exec(ctx, ExecSpec{Command: "pwd", Dir: "/", Wait: true}); err != nil {
		t.Error(err)
	}

	tctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = sys.Exec(tctx, ExecSpec{Command: "sleep", Args: []string{"30"}, Wait: true})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("a long command was not stopped: %v after %v", err, time.Since(start))
	}

	// A detached command is started and not waited for, and outlives the context it was started with.
	dctx, dcancel := context.WithCancel(ctx)
	start = time.Now()
	if err := sys.Exec(dctx, ExecSpec{Command: "sleep", Args: []string{"1"}}); err != nil {
		t.Error(err)
	}
	dcancel()
	if time.Since(start) > 500*time.Millisecond {
		t.Error("a detached command was waited for")
	}
	if err := sys.Exec(ctx, ExecSpec{Command: "definitely-not-a-program"}); err == nil {
		t.Error("a detached missing program did not fail to start")
	}
}

func TestChattyCommandIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	err := OS().Exec(context.Background(), ExecSpec{Command: "sh", Args: []string{"-c", "yes | head -c 5000000; exit 1"}, Wait: true})
	if err == nil || len(err.Error()) > 1000 {
		t.Errorf("error is %d bytes", len(err.Error()))
	}
}

func TestSchemasMatchTheRegistry(t *testing.T) {
	reg := New(DryRun(nil), nil)
	have := map[string]bool{}
	for _, s := range Schemas() {
		if have[s.Type] {
			t.Errorf("schema for %s twice", s.Type)
		}
		have[s.Type] = true
	}
	for _, typ := range reg.Types() {
		if !have[typ] {
			t.Errorf("action %s has no schema", typ)
		}
	}
	for typ := range have {
		deckOwn := typ == "page" || typ == "back"
		if deckOwn == slices.Contains(reg.Types(), typ) {
			t.Errorf("%s: deck-handled and registered must be opposites", typ)
		}
	}
}
