//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// telemetryTestClient is a test-only install (spec 022 T088): it builds and
// signs reports with telemetryschema and a throwaway signing secret, and POSTs
// them to a receiver's public listener. It lets the lifecycle test play the
// other side of the US7 forgery cases (another key claiming an ID, unsigned,
// tampered and replayed reports, a claim on a freshly reset ID).
type telemetryTestClient struct {
	ingestURL string
	secret    []byte
	http      *http.Client
}

// newTelemetryTestClient returns a client that POSTs to
// http://127.0.0.1:<port>/ingest, with a new random signing secret.
func newTelemetryTestClient(t *testing.T, port int) *telemetryTestClient {
	t.Helper()
	secret, err := telemetryschema.NewSecret()
	if err != nil {
		t.Fatalf("new test signing secret: %v", err)
	}
	return &telemetryTestClient{
		ingestURL: fmt.Sprintf("http://127.0.0.1:%d/ingest", port),
		secret:    secret,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// newTestInstallID returns a random lowercase UUIDv4, the form the schema
// accepts for ext.installId.
func newTestInstallID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random install id: %v", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// report builds a valid extended report for installID: the public key is the
// one this client derives for that ID, so a receiver binds the ID to this
// client's key on first use. version is the report's version label, which
// lets a test tell its own reports apart on the receiver's /metrics.
func (c *telemetryTestClient) report(t *testing.T, installID, version string, servers int, sentAt time.Time) telemetryschema.Report {
	t.Helper()
	priv, err := telemetryschema.DeriveKey(c.secret, installID)
	if err != nil {
		t.Fatalf("derive test key: %v", err)
	}
	return telemetryschema.Report{
		Version:   version,
		Servers:   servers,
		Templates: 1,
		Ext: &telemetryschema.Extended{
			Schema:    telemetryschema.SchemaVersion,
			InstallID: installID,
			Env:       telemetryschema.Env{K8s: []string{"1.31"}, Distro: []string{"other"}, Arch: []string{"amd64"}, Nodes: "1", Deployment: "cluster"},
			Games:     telemetryschema.Games{Official: map[string]int{}, Custom: servers},
			Features:  telemetryschema.Features{Tunnels: []string{}, Clusters: "1", DB: "sqlite", Language: "en"},
			Key:       telemetryschema.PublicKeyString(priv),
			SentAt:    sentAt.UTC().Truncate(time.Second),
		},
	}
}

// sign encodes rep and signs the exact body bytes with the key derived for
// rep's install ID.
func (c *telemetryTestClient) sign(t *testing.T, rep telemetryschema.Report) (body []byte, signature string) {
	t.Helper()
	body, err := telemetryschema.Encode(rep)
	if err != nil {
		t.Fatalf("encode test report: %v", err)
	}
	priv, err := telemetryschema.DeriveKey(c.secret, rep.Ext.InstallID)
	if err != nil {
		t.Fatalf("derive test key: %v", err)
	}
	return body, telemetryschema.Sign(priv, body)
}

// post sends body with signature (omitted when empty) and returns the status
// and the "error" code of the receiver's fixed JSON refusal body ("" for 204).
func (c *telemetryTestClient) post(ctx context.Context, t *testing.T, body []byte, signature string) (status int, code string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new ingest request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(telemetryschema.SignatureHeader, signature)
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

// send signs rep and POSTs it.
func (c *telemetryTestClient) send(ctx context.Context, t *testing.T, rep telemetryschema.Report) (status int, code string) {
	t.Helper()
	body, sig := c.sign(t, rep)
	return c.post(ctx, t, body, sig)
}

// receiverMetric sums the samples of one metric family from a receiver's
// /metrics. Only lines whose name is exactly name (followed by a label set or
// a space) and that contain labelFilter (a substring such as
// `reason="id_claimed"`; empty matches all) are counted. token, when set, is
// sent as a Bearer token (the dashboard listener needs it; an old receiver's
// public listener does not). A family that has no sample yet sums to 0.
func receiverMetric(ctx context.Context, t *testing.T, port int, token, name, labelFilter string) float64 {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/metrics", port), nil)
	if err != nil {
		t.Fatalf("new metrics request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET receiver /metrics on %d: %v", port, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET receiver /metrics on %d = %d: %s", port, resp.StatusCode, body)
	}
	var total float64
	for _, line := range strings.Split(string(body), "\n") {
		rest, ok := strings.CutPrefix(line, name)
		if !ok || rest == "" || (rest[0] != '{' && rest[0] != ' ') {
			continue
		}
		if labelFilter != "" && !strings.Contains(line, labelFilter) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse metric line %q: %v", line, err)
		}
		total += v
	}
	return total
}
