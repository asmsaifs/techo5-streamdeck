package actions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/secrets"
)

func netReg(in *deck.Integrations) (*Registry, secrets.Store) {
	st := secrets.Memory()
	r := New(&fake{}, nil)
	r.Secrets = st
	r.Integrations = func() *deck.Integrations { return in }
	return r, st
}

func TestHTTPAction(t *testing.T) {
	var got struct {
		method, path, query, auth, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		got.method, got.path, got.query, got.auth, got.body = q.Method, q.URL.Path, q.URL.RawQuery, q.Header.Get("X-Key"), string(b)
		switch q.URL.Path {
		case "/fail":
			http.Error(w, "no such light", 404)
		}
	}))
	defer srv.Close()
	r, _ := netReg(nil)
	run := func(js string) error { return r.Run(context.Background(), act(t, js)) }

	if err := run(`{"type":"http","method":"post","url":"` + srv.URL + `/hook?k=1","headers":["X-Key: abc",""],"body":"hi"}`); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/hook" || got.query != "k=1" || got.auth != "abc" || got.body != "hi" {
		t.Errorf("server saw %+v", got)
	}
	if err := run(`{"type":"http","url":"` + srv.URL + `"}`); err != nil || got.method != "GET" {
		t.Errorf("default method: err %v, %s", err, got.method)
	}
	err := run(`{"type":"http","url":"` + srv.URL + `/fail?secret=hunter2"}`)
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "no such light") {
		t.Errorf("error status = %v, want 404 with the reply", err)
	}

	for _, js := range []string{
		`{"type":"http","url":"ftp://x/y"}`, `{"type":"http","url":"file:///etc/passwd"}`, `{"type":"http"}`,
		`{"type":"http","url":"http://x","method":"TRACE"}`, `{"type":"http","url":"http://x","headers":["nocolon"]}`,
		`{"type":"http","url":"http://x","timeout":9999}`,
	} {
		if err := run(js); err == nil {
			t.Errorf("%s was accepted", js)
		}
	}
	// A refused connection must not put the query, where a key may be, in the error.
	dead := httptest.NewServer(nil)
	dead.Close()
	if err := run(`{"type":"http","url":"` + dead.URL + `/x?key=SECRET"}`); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("connection error = %v", err)
	}
}

func TestHomeAssistantService(t *testing.T) {
	var path, auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		path, auth, body = q.URL.Path, q.Header.Get("Authorization"), string(b)
	}))
	defer srv.Close()
	in := &deck.Integrations{HomeAssistant: srv.URL + "/"}
	r, st := netReg(in)
	run := func(js string) error { return r.Run(context.Background(), act(t, js)) }

	if err := run(`{"type":"ha.service","service":"light.turn_on","entity":"light.kitchen"}`); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("without a token: %v", err)
	}
	st.Set(secrets.HomeAssistantToken, "tok")
	if err := run(`{"type":"ha.service","service":"light.turn_on","entity":"light.kitchen","data":"{\"brightness\":120}"}`); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	if path != "/api/services/light/turn_on" || auth != "Bearer tok" || m["entity_id"] != "light.kitchen" || m["brightness"] != float64(120) {
		t.Errorf("server saw %s %s %s", path, auth, body)
	}
	for _, js := range []string{
		`{"type":"ha.service","service":"../config"}`, `{"type":"ha.service","service":"light"}`,
		`{"type":"ha.service","service":"light.on","data":"[1]"}`, `{"type":"ha.service","service":"light.on","data":"{"}`,
	} {
		if err := run(js); err == nil {
			t.Errorf("%s was accepted", js)
		}
	}
	in.HomeAssistant = ""
	if err := run(`{"type":"ha.service","service":"light.on"}`); err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("no address: %v", err)
	}
}

// fakeOBS is an OBS WebSocket v5 server: it asks for the password, if set, and answers requests.
func fakeOBS(t *testing.T, password string, reply func(typ string, data map[string]any) (bool, int, string)) (addr string, seen *[]string) {
	t.Helper()
	var reqs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		c, err := websocket.Accept(w, q, &websocket.AcceptOptions{Subprotocols: []string{"obswebsocket.json"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := q.Context()
		send := func(op int, d any) {
			b, _ := json.Marshal(map[string]any{"op": op, "d": d})
			c.Write(ctx, websocket.MessageText, b)
		}
		hello := map[string]any{"obsWebSocketVersion": "5.5.0", "rpcVersion": 1}
		if password != "" {
			hello["authentication"] = map[string]string{"challenge": "chal", "salt": "salt"}
		}
		send(0, hello)
		read := func() (int, map[string]any) {
			_, b, err := c.Read(ctx)
			if err != nil {
				return -1, nil
			}
			var m struct {
				Op int
				D  map[string]any
			}
			json.Unmarshal(b, &m)
			return m.Op, m.D
		}
		op, d := read()
		if op != 1 {
			return
		}
		if password != "" && d["authentication"] != obsAuth(password, "salt", "chal") {
			c.Close(4009, "Authentication failed")
			return
		}
		send(2, map[string]any{"negotiatedRpcVersion": 1})
		send(5, map[string]any{"eventType": "StudioModeStateChanged"}) // an event nobody asked for
		op, d = read()
		if op != 6 {
			return
		}
		typ := d["requestType"].(string)
		data, _ := d["requestData"].(map[string]any)
		reqs = append(reqs, typ)
		ok, code, comment := reply(typ, data)
		send(7, map[string]any{"requestType": typ, "requestId": d["requestId"], "requestStatus": map[string]any{"result": ok, "code": code, "comment": comment}})
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &reqs
}

func TestOBS(t *testing.T) {
	var lastData map[string]any
	addr, seen := fakeOBS(t, "pw", func(typ string, data map[string]any) (bool, int, string) {
		lastData = data
		if typ == "SetCurrentProgramScene" && data["sceneName"] == "nope" {
			return false, 600, "No scene was found by the name of `nope`."
		}
		return true, 100, ""
	})
	r, st := netReg(&deck.Integrations{OBS: addr})
	run := func(js string) error { return r.Run(context.Background(), act(t, js)) }

	if err := run(`{"type":"obs","command":"record.toggle"}`); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("without the password: %v", err)
	}
	st.Set(secrets.OBSPassword, "wrong")
	if err := run(`{"type":"obs","command":"record.toggle"}`); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wrong password: %v", err)
	}
	st.Set(secrets.OBSPassword, "pw")
	if err := run(`{"type":"obs","command":"record.toggle"}`); err != nil {
		t.Fatal(err)
	}
	if err := run(`{"type":"obs","command":"scene","name":"Game"}`); err != nil || lastData["sceneName"] != "Game" {
		t.Fatalf("scene: %v %v", err, lastData)
	}
	if err := run(`{"type":"obs","command":"scene","name":"nope"}`); err == nil || !strings.Contains(err.Error(), "No scene") {
		t.Errorf("OBS's refusal: %v", err)
	}
	if got := strings.Join(*seen, ","); got != "ToggleRecord,SetCurrentProgramScene,SetCurrentProgramScene" {
		t.Errorf("OBS got %s", got)
	}
	for _, js := range []string{`{"type":"obs","command":"scene"}`, `{"type":"obs","command":"quit"}`, `{"type":"obs"}`} {
		if err := run(js); err == nil {
			t.Errorf("%s was accepted", js)
		}
	}
}

func TestOBSWithoutPasswordAndNotRunning(t *testing.T) {
	addr, _ := fakeOBS(t, "", func(string, map[string]any) (bool, int, string) { return true, 100, "" })
	r, _ := netReg(&deck.Integrations{OBS: addr})
	if err := r.Run(context.Background(), act(t, `{"type":"obs","command":"stream.stop"}`)); err != nil {
		t.Errorf("an OBS with no password: %v", err)
	}
	r, _ = netReg(&deck.Integrations{OBS: "127.0.0.1:1"})
	if err := r.Run(context.Background(), act(t, `{"type":"obs","command":"stream.stop"}`)); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("OBS not running: %v", err)
	}
	r, _ = netReg(nil)
	if err := r.Run(context.Background(), act(t, `{"type":"obs","command":"stream.stop"}`)); err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("no address: %v", err)
	}
}

func TestNetworkActionsDoNothingInDryRun(t *testing.T) {
	r := New(DryRun(nil), nil)
	r.Integrations = func() *deck.Integrations {
		return &deck.Integrations{HomeAssistant: "http://127.0.0.1:1", OBS: "127.0.0.1:1"}
	}
	for _, js := range []string{`{"type":"http","url":"http://127.0.0.1:1/x"}`, `{"type":"ha.service","service":"light.on"}`, `{"type":"obs","command":"record.start"}`} {
		if err := r.Run(context.Background(), act(t, js)); err != nil {
			t.Errorf("%s: %v", js, err)
		}
	}
}

// The example from obs-websocket's protocol document.
func TestOBSAuthMatchesTheProtocolExample(t *testing.T) {
	got := obsAuth("supersecretpassword", "lM1GncleQOaCu9lT1yeUZhFYnqhsLLP1G5lAGo3ixaI=", "+IxH4CnCiqpX1rM9scsNynZzbOe4KhDeYcTNS3PDaeY=")
	if want := "1Ct943GAT+6YQUUX47Ia/ncufilbe6+oD6lY+5kaCu4="; got != want {
		t.Errorf("auth = %s, want %s", got, want)
	}
}

func TestHAState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch q.URL.Path {
		case "/api/states/sensor.t":
			w.Write([]byte(`{"state":"21.5","attributes":{"unit_of_measurement":"°C"}}`))
		case "/api/states/light.k":
			w.Write([]byte(`{"state":"on","attributes":{}}`))
		default:
			http.Error(w, "Entity not found.", 404)
		}
	}))
	defer srv.Close()
	r, st := netReg(&deck.Integrations{HomeAssistant: srv.URL})
	ctx := context.Background()
	if _, err := r.HAState(ctx, "sensor.t"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("no token: %v", err)
	}
	st.Set(secrets.HomeAssistantToken, "tok")
	for entity, want := range map[string]string{"sensor.t": "21.5 °C", "light.k": "on"} {
		if got, err := r.HAState(ctx, entity); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", entity, got, err, want)
		}
	}
	if _, err := r.HAState(ctx, "sensor.gone"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("unknown entity: %v", err)
	}
	if _, err := r.HAState(ctx, "../states"); err == nil {
		t.Error("a path was taken for an entity")
	}
}
