package tiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/asmsaifs/techo5-streamdeck/internal/deck"
)

// Usage tiles show how much of an AI subscription's limit is used, as a ring. Neither service
// documents an API for this; both tools (Claude Code and the Codex CLI) read the same numbers for
// their own /usage screens, with the sign-in they keep on this computer, so the tiles borrow that
// sign-in read-only. A tool that has not been signed in, or whose token has run out, makes the
// tile say so; running the tool once refreshes the token, and the tile never refreshes it itself,
// as refresh tokens are single-use and taking one would sign the tool out.

// UsageKind says which service and which window a usage tile watches.
type UsageKind struct {
	Claude bool // else ChatGPT
	Weekly bool // else the session window (5 hours)
}

// usageKinds maps tile types to what they watch.
var usageKinds = map[string]UsageKind{
	deck.TileClaudeSession:  {Claude: true},
	deck.TileClaudeWeek:     {Claude: true, Weekly: true},
	deck.TileChatGPTSession: {},
	deck.TileChatGPTWeek:    {Weekly: true},
}

// UsageKindOf is what a usage tile type watches; ok is false for any other type.
func UsageKindOf(tileType string) (UsageKind, bool) {
	k, ok := usageKinds[tileType]
	return k, ok
}

var usageClient = &http.Client{}

func usage(ctx context.Context, k UsageKind) (float64, error) {
	if k.Claude {
		return claudeUsage(ctx, k.Weekly)
	}
	return chatgptUsage(ctx, k.Weekly)
}

func claudeUsage(ctx context.Context, weekly bool) (float64, error) {
	tok, err := claudeToken(ctx)
	if err != nil {
		return 0, err
	}
	body, err := usageGet(ctx, "https://api.anthropic.com/api/oauth/usage", map[string]string{
		"Authorization":  "Bearer " + tok,
		"anthropic-beta": "oauth-2025-04-20",
	}, "Claude Code")
	if err != nil {
		return 0, err
	}
	return parseClaudeUsage(body, weekly)
}

func parseClaudeUsage(body []byte, weekly bool) (float64, error) {
	var r struct {
		FiveHour *struct{ Utilization *float64 } `json:"five_hour"`
		SevenDay *struct{ Utilization *float64 } `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, fmt.Errorf("Claude usage: %v", err)
	}
	w := r.FiveHour
	if weekly {
		w = r.SevenDay
	}
	if w == nil || w.Utilization == nil {
		return 0, errors.New("Claude reports no limit for this window on this plan")
	}
	return clampPercent(*w.Utilization), nil
}

// claudeToken is the access token Claude Code keeps: in the macOS keychain, else in
// ~/.claude/.credentials.json (Linux and Windows).
func claudeToken(ctx context.Context) (string, error) {
	var raw []byte
	if runtime.GOOS == "darwin" {
		out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		if err == nil {
			raw = out
		}
	}
	if raw == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		b, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
		if err != nil {
			return "", errors.New("Claude Code is not signed in on this computer")
		}
		raw = b
	}
	var c struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"` // milliseconds since 1970
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.OAuth.AccessToken == "" {
		return "", errors.New("Claude Code is not signed in on this computer")
	}
	return c.OAuth.AccessToken, nil
}

func chatgptUsage(ctx context.Context, weekly bool) (float64, error) {
	tok, account, err := codexToken()
	if err != nil {
		return 0, err
	}
	h := map[string]string{"Authorization": "Bearer " + tok}
	if account != "" {
		h["ChatGPT-Account-Id"] = account
	}
	body, err := usageGet(ctx, "https://chatgpt.com/backend-api/wham/usage", h, "Codex")
	if err != nil {
		return 0, err
	}
	return parseChatGPTUsage(body, weekly)
}

func parseChatGPTUsage(body []byte, weekly bool) (float64, error) {
	type window struct {
		UsedPercent *float64 `json:"used_percent"`
	}
	var r struct {
		RateLimit struct {
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, fmt.Errorf("ChatGPT usage: %v", err)
	}
	w := r.RateLimit.Primary
	if weekly {
		w = r.RateLimit.Secondary
	}
	if w == nil || w.UsedPercent == nil {
		return 0, errors.New("ChatGPT reports no limit for this window on this plan")
	}
	return clampPercent(*w.UsedPercent), nil
}

// codexToken is the sign-in the Codex CLI keeps in $CODEX_HOME/auth.json (default ~/.codex).
func codexToken() (token, account string, err error) {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", herr
		}
		dir = filepath.Join(home, ".codex")
	}
	b, rerr := os.ReadFile(filepath.Join(dir, "auth.json"))
	if rerr != nil {
		return "", "", errors.New("the Codex CLI is not signed in on this computer")
	}
	var a struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(b, &a) != nil || a.Tokens.AccessToken == "" {
		return "", "", errors.New("the Codex CLI is signed in with an API key, which has no subscription limit; sign in with ChatGPT")
	}
	return a.Tokens.AccessToken, a.Tokens.AccountID, nil
}

func usageGet(ctx context.Context, url string, headers map[string]string, tool string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := usageClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("the %s sign-in has run out; use %s once to renew it", tool, tool)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s usage: %s", tool, strings.TrimSpace(resp.Status))
	}
	return body, nil
}

func clampPercent(v float64) float64 { return max(0, min(100, v)) }
