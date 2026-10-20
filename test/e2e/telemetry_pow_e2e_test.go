//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// Helpers for the proof-of-work subtests of TestTelemetryLifecycle (spec 022
// S10, T108).

// metricPoWChallenges counts the challenges a receiver issued.
const metricPoWChallenges = "gameplane_telemetry_pow_challenges_total"

// challenge fetches a challenge from the receiver's public port and returns
// its token and difficulty.
func (c *telemetryTestClient) challenge(ctx context.Context, t *testing.T) (token string, bits int) {
	t.Helper()
	url := strings.TrimSuffix(c.ingestURL, "/ingest") + "/v1/challenge"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new challenge request: %v", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Challenge string `json:"challenge"`
		Bits      int    `json:"bits"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.Challenge == "" {
		t.Fatalf("GET %s = %d %s, want 200 with a challenge", url, resp.StatusCode, raw)
	}
	return out.Challenge, out.Bits
}

// solvedPoW fetches a challenge, requires at least wantBits of difficulty and
// returns the solved header value.
func (c *telemetryTestClient) solvedPoW(ctx context.Context, t *testing.T, wantBits int) string {
	t.Helper()
	token, bits := c.challenge(ctx, t)
	if bits < wantBits {
		t.Fatalf("challenge difficulty = %d bits, want at least %d (INGEST_POW_MIN_BITS)", bits, wantBits)
	}
	nonce, err := telemetryschema.SolvePoW(ctx, token, bits)
	if err != nil {
		t.Fatalf("solve a %d-bit challenge: %v", bits, err)
	}
	return telemetryschema.FormatPoW(token, nonce)
}

// postPoW is post with the proof-of-work header (omitted when pow is empty).
func (c *telemetryTestClient) postPoW(ctx context.Context, t *testing.T, body []byte, signature, pow string) (status int, code string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new ingest request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(telemetryschema.SignatureHeader, signature)
	}
	if pow != "" {
		req.Header.Set(telemetryschema.PoWHeader, pow)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", c.ingestURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var refusal struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &refusal) // a 204 has no body
	return resp.StatusCode, refusal.Error
}
