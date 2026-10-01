package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
	"github.com/asmsaifs/techo5-streamdeck/internal/secrets"
)

const (
	defaultNetTimeout = 10 * time.Second
	maxNetTimeout     = 2 * time.Minute
	maxReply          = 64 << 10 // all of a reply that is read: enough for an error message
)

// dry reports whether this registry only logs what it would do. The network actions are not part
// of System, so they ask.
func (r *Registry) dry() bool { _, ok := r.Sys.(dryRun); return ok }

func (r *Registry) integrations() deck.Integrations {
	if r.Integrations != nil {
		if in := r.Integrations(); in != nil {
			return *in
		}
	}
	return deck.Integrations{}
}

func (r *Registry) secret(name, what string) (string, error) {
	if r.Secrets == nil {
		return "", errors.New("no place to keep secrets")
	}
	v, err := r.Secrets.Get(name)
	if errors.Is(err, secrets.ErrNotFound) {
		return "", fmt.Errorf("%s is not set: enter it in Settings", what)
	}
	return v, err
}

func timeoutOf(sec float64) (time.Duration, error) {
	if sec == 0 {
		return defaultNetTimeout, nil
	}
	if sec < 0 || sec > maxNetTimeout.Seconds() {
		return 0, fmt.Errorf("timeout must be 1 to %d seconds", int(maxNetTimeout.Seconds()))
	}
	return time.Duration(sec * float64(time.Second)), nil
}

var httpClient = &http.Client{
	// A redirect from a webhook to somewhere else is followed as a browser would, but at most a few.
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// do sends the request and fails on an error status, with the start of the reply as the reason.
func do(ctx context.Context, req *http.Request) error {
	resp, err := httpClient.Do(req.WithContext(ctx))
	if err != nil {
		return redact(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s%s", resp.Status, tail(body))
	}
	return nil
}

// redact keeps a URL's query out of an error: webhook addresses often carry a key in it.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			u.RawQuery, u.User = "", nil
			ue.URL = u.String()
		}
	}
	return err
}

var methods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// httpAction is a webhook: one request, and it worked if the status is below 400.
func httpAction(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Method   string   `json:"method"`
		URL      string   `json:"url"`
		Headers  []string `json:"headers"` // "Name: value"
		Body     string   `json:"body"`
		TimeoutS float64  `json:"timeout"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = "GET"
	}
	if !methods[method] {
		return fmt.Errorf("method %q is not one of GET, POST, PUT, PATCH, DELETE", p.Method)
	}
	u, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q is not an http:// or https:// address", p.URL)
	}
	to, err := timeoutOf(p.TimeoutS)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, u.String(), strings.NewReader(p.Body))
	if err != nil {
		return err
	}
	for _, h := range p.Headers {
		if strings.TrimSpace(h) == "" {
			continue
		}
		k, v, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("header %q is not \"Name: value\"", h)
		}
		req.Header.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	if r.dry() {
		r.Log.Info("dry run: http", "method", method, "host", u.Host)
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	return do(ctx, req)
}

var haService = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

// haServiceAction calls a Home Assistant service, "light.turn_on" with an entity and data.
func haServiceAction(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Service string `json:"service"`
		Entity  string `json:"entity"`
		Data    string `json:"data"` // a JSON object
	}
	if err := params(a, &p); err != nil {
		return err
	}
	svc := strings.TrimSpace(p.Service)
	// The pattern also keeps a service name from reaching outside /api/services/.
	if !haService.MatchString(svc) {
		return fmt.Errorf("service %q is not like light.turn_on", p.Service)
	}
	data := map[string]any{}
	if strings.TrimSpace(p.Data) != "" {
		if err := json.Unmarshal([]byte(p.Data), &data); err != nil {
			return errors.New("data is not a JSON object, like {\"brightness\": 120}")
		}
	}
	if e := strings.TrimSpace(p.Entity); e != "" {
		data["entity_id"] = e
	}
	base := strings.TrimRight(strings.TrimSpace(r.integrations().HomeAssistant), "/")
	if base == "" {
		return errors.New("the Home Assistant address is not set: enter it in Settings")
	}
	if r.dry() {
		r.Log.Info("dry run: ha.service", "service", svc)
		return nil
	}
	token, err := r.secret(secrets.HomeAssistantToken, "the Home Assistant token")
	if err != nil {
		return err
	}
	body, _ := json.Marshal(data)
	dom, name, _ := strings.Cut(svc, ".")
	req, err := http.NewRequest("POST", base+"/api/services/"+dom+"/"+name, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(ctx, defaultNetTimeout)
	defer cancel()
	return do(ctx, req)
}

// obsRequests maps the action's commands to OBS WebSocket v5 requests; needsName says which take
// the scene or input name.
var obsRequests = map[string]struct {
	typ       string
	nameField string
}{
	"scene":             {"SetCurrentProgramScene", "sceneName"},
	"record.toggle":     {"ToggleRecord", ""},
	"record.start":      {"StartRecord", ""},
	"record.stop":       {"StopRecord", ""},
	"stream.toggle":     {"ToggleStream", ""},
	"stream.start":      {"StartStream", ""},
	"stream.stop":       {"StopStream", ""},
	"input.mute.toggle": {"ToggleInputMute", "inputName"},
}

// obsAction sends one request to OBS Studio's WebSocket server (v5, built into OBS 28 and later).
// It connects for each press, so OBS can be started and quit at will.
func obsAction(ctx context.Context, r *Registry, a *deck.Action) error {
	var p struct {
		Command string `json:"command"`
		Name    string `json:"name"`
	}
	if err := params(a, &p); err != nil {
		return err
	}
	req, ok := obsRequests[p.Command]
	if !ok {
		return fmt.Errorf("command %q is not one OBS can be given here", p.Command)
	}
	data := map[string]any{}
	if req.nameField != "" {
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("name is missing: the scene or input to use")
		}
		data[req.nameField] = p.Name
	}
	addr := strings.TrimSpace(r.integrations().OBS)
	if addr == "" {
		return errors.New("the OBS address is not set: enter it in Settings")
	}
	if r.dry() {
		r.Log.Info("dry run: obs", "command", p.Command)
		return nil
	}
	// OBS asks for a password only when its server is set to; a missing one is tried as none.
	var pw string
	if r.Secrets != nil {
		pw, _ = r.Secrets.Get(secrets.OBSPassword)
	}
	ctx, cancel := context.WithTimeout(ctx, defaultNetTimeout)
	defer cancel()
	return obsCall(ctx, "ws://"+addr, pw, req.typ, data)
}

type obsMsg struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

func obsSend(ctx context.Context, c *websocket.Conn, op int, d any) error {
	b, err := json.Marshal(obsMsg{Op: op, D: mustJSON(d)})
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, b)
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func obsRead(ctx context.Context, c *websocket.Conn, want int) (json.RawMessage, error) {
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return nil, err
		}
		var m obsMsg
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, errors.New("OBS sent something that is not its protocol")
		}
		if m.Op == want {
			return m.D, nil
		}
		// Events (op 5) can arrive at any time; they are not wanted here.
	}
}

// obsAuth is the answer to OBS's challenge: base64(sha256(base64(sha256(password+salt))+challenge)).
func obsAuth(password, salt, challenge string) string {
	h := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(h[:])
	h = sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(h[:])
}

func obsCall(ctx context.Context, wsURL, password, typ string, data map[string]any) error {
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Subprotocols: []string{"obswebsocket.json"}})
	if err != nil {
		return fmt.Errorf("OBS is not reachable at %s (is it running, with its WebSocket server on?): %w", strings.TrimPrefix(wsURL, "ws://"), err)
	}
	defer c.CloseNow()

	raw, err := obsRead(ctx, c, 0) // Hello
	if err != nil {
		return err
	}
	var hello struct {
		Auth *struct{ Challenge, Salt string } `json:"authentication"`
	}
	if err := json.Unmarshal(raw, &hello); err != nil {
		return err
	}
	ident := map[string]any{"rpcVersion": 1}
	if hello.Auth != nil {
		if password == "" {
			return errors.New("OBS wants a password: enter it in Settings")
		}
		ident["authentication"] = obsAuth(password, hello.Auth.Salt, hello.Auth.Challenge)
	}
	if err := obsSend(ctx, c, 1, ident); err != nil {
		return err
	}
	if _, err := obsRead(ctx, c, 2); err != nil { // Identified; a wrong password closes the socket
		if cs := websocket.CloseStatus(err); cs == 4009 {
			return errors.New("OBS refused the password")
		}
		return fmt.Errorf("OBS did not accept the connection: %w", err)
	}

	const id = "deck"
	if err := obsSend(ctx, c, 6, map[string]any{"requestType": typ, "requestId": id, "requestData": data}); err != nil {
		return err
	}
	raw, err = obsRead(ctx, c, 7)
	if err != nil {
		return err
	}
	var resp struct {
		Status struct {
			Result  bool   `json:"result"`
			Code    int    `json:"code"`
			Comment string `json:"comment"`
		} `json:"requestStatus"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if !resp.Status.Result {
		return fmt.Errorf("OBS: %s (code %d)", resp.Status.Comment, resp.Status.Code)
	}
	return nil
}

// HAState reads a Home Assistant entity for a live tile: its state, with its unit when it has one
// ("21.5 °C").
func (r *Registry) HAState(ctx context.Context, entity string) (string, error) {
	if !haService.MatchString(entity) { // the same shape as a service: domain.name
		return "", fmt.Errorf("entity %q is not like sensor.kitchen_temperature", entity)
	}
	base := strings.TrimRight(strings.TrimSpace(r.integrations().HomeAssistant), "/")
	if base == "" {
		return "", errors.New("the Home Assistant address is not set")
	}
	if r.dry() {
		return "dry run", nil
	}
	token, err := r.secret(secrets.HomeAssistantToken, "the Home Assistant token")
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/states/"+entity, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", redact(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s%s", resp.Status, tail(body))
	}
	var st struct {
		State      string `json:"state"`
		Attributes struct {
			Unit string `json:"unit_of_measurement"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return "", errors.New("Home Assistant sent something that is not a state")
	}
	if st.Attributes.Unit != "" {
		return st.State + " " + st.Attributes.Unit, nil
	}
	return st.State, nil
}
