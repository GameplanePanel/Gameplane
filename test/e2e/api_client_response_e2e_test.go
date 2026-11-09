//go:build e2e

package e2e

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAPIClient_ExtractIntField(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, key, want string
	}{
		{name: "user ID", body: `{"id":42,"count":7}`, key: "id", want: "42"},
		{name: "other integer field", body: `{"id":42,"count":7}`, key: "count", want: "7"},
		{name: "zero", body: `{"count":0}`, key: "count", want: "0"},
		{name: "exact key", body: `{"user_id":42}`, key: "id", want: ""},
		{name: "missing", body: `{"id":42}`, key: "count", want: ""},
		{name: "string value", body: `{"id":"42"}`, key: "id", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := extractIntField(test.body, test.key); got != test.want {
				t.Fatalf("extractIntField(%q, %q) = %q, want %q", test.body, test.key, got, test.want)
			}
		})
	}
}

// TestAPIClient_DoContextResponseBody keeps transport failures distinct from
// fully received HTTP errors used by recovery's authorization assertions.
func TestAPIClient_DoContextResponseBody(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		status     int
		body       string
		incomplete bool
	}{
		{name: "complete", status: http.StatusOK, body: "complete response"},
		{name: "HTTP error", status: http.StatusServiceUnavailable, body: "unavailable"},
		{name: "truncated", status: http.StatusOK, body: "partial", incomplete: true},
		{name: "truncated HTTP error", status: http.StatusUnauthorized, body: "denied", incomplete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				length := len(test.body)
				if test.incomplete {
					length++ // Real HTTP transport observes EOF before Content-Length.
				}
				w.Header().Set("Content-Length", strconv.Itoa(length))
				w.WriteHeader(test.status)
				if _, err := io.WriteString(w, test.body); err != nil {
					t.Errorf("write fixture response: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			client := &APIClient{BaseURL: server.URL, HTTP: server.Client()}
			resp, body, err := client.DoContext(t.Context(), http.MethodGet, "/fixture", nil)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if test.incomplete {
				if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "GET /fixture") {
					t.Fatalf("expected contextual wrapped unexpected EOF, got %v", err)
				}
				if resp == nil || resp.StatusCode != test.status || resp.Header.Get("Content-Length") != strconv.Itoa(len(test.body)+1) || string(body) != test.body {
					t.Fatalf("incomplete response lost status, headers or partial bytes: response=%v body=%q", resp, body)
				}
				return
			}
			if err != nil || resp == nil {
				t.Fatalf("complete response failed: response=%v error=%v", resp, err)
			}
			if resp.StatusCode != test.status || string(body) != test.body {
				t.Fatalf("response changed: status=%d body=%q", resp.StatusCode, body)
			}
		})
	}
}
