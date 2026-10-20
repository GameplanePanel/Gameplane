//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestAPI_I18n_AuthLocale proves the pre-auth GET /auth/locale endpoint on a
// fresh install: it answers without a session and returns exactly one key,
// defaultLanguage, set to "en". It never logs in, so it spends no login
// budget, and it can run in parallel with its bucket neighbours.
func TestAPI_I18n_AuthLocale(t *testing.T) {
	t.Parallel()

	port, stop := envInstance.PortForward(t, "gameplane-system", "svc/gameplane-api", 80)
	defer stop()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/auth/locale", port), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/locale: status %d, body=%s", resp.StatusCode, body)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one key, got %d; body=%s", len(got), body)
	}
	lang, ok := got["defaultLanguage"].(string)
	if !ok {
		t.Fatalf("defaultLanguage missing or not a string; body=%s", body)
	}
	if lang != "en" {
		t.Fatalf("defaultLanguage = %q, want en on a fresh install", lang)
	}
}
