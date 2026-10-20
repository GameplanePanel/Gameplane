package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// doLanguageReq sends a request as userID, optionally with an Accept header,
// and returns the status, content type and body.
func doLanguageReq(t *testing.T, srv *httptest.Server, userID int64, method, path, accept, rawBody string) (int, string, []byte) {
	t.Helper()
	var buf io.Reader
	if rawBody != "" {
		buf = bytes.NewReader([]byte(rawBody))
	}
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if rawBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != 0 {
		req.Header.Set("X-Test-User-ID", strconv.FormatInt(userID, 10))
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), out
}

// storedLanguage reads the language back through GET /users/me/preferences.
func storedLanguage(t *testing.T, srv *httptest.Server, userID int64) *string {
	t.Helper()
	status, _, body := doLanguageReq(t, srv, userID, http.MethodGet, "/users/me/preferences", "", "")
	if status != http.StatusOK {
		t.Fatalf("get preferences: status %d body=%s", status, body)
	}
	return decodePrefs(t, body).Language
}

func TestPutLanguage_AcceptedThenReadBack(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-putter", "viewer", "")

	status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{"language":"en"}`)
	if status != http.StatusNoContent {
		t.Fatalf("PUT en: status %d body=%s", status, body)
	}
	if got := storedLanguage(t, srv, userID); got == nil || *got != "en" {
		t.Fatalf("stored language = %v, want en", got)
	}
}

func TestPutLanguage_NullClears(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-clearer", "viewer", "")

	if status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{"language":"en"}`); status != http.StatusNoContent {
		t.Fatalf("seed en: status %d body=%s", status, body)
	}
	status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{"language":null}`)
	if status != http.StatusNoContent {
		t.Fatalf("PUT null: status %d body=%s", status, body)
	}
	if got := storedLanguage(t, srv, userID); got != nil {
		t.Fatalf("stored language = %q after null, want nil", *got)
	}
}

func TestPutLanguage_UnknownTagPlainText(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-plain", "viewer", "")

	status, ctype, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{"language":"xx"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT xx: status %d, want 400 body=%s", status, body)
	}
	if strings.Contains(ctype, "problem+json") {
		t.Fatalf("plain caller got content type %q", ctype)
	}
	if string(body) != "unknown language: xx\n" {
		t.Fatalf("plain body = %q, want %q", body, "unknown language: xx\n")
	}
}

func TestPutLanguage_UnknownTagProblemDocument(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-problem", "viewer", "")

	status, ctype, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "application/problem+json", `{"language":"xx"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT xx problem: status %d, want 400 body=%s", status, body)
	}
	if !strings.Contains(ctype, "problem+json") {
		t.Fatalf("content type = %q, want problem+json", ctype)
	}
	var doc struct {
		MessageID string         `json:"messageId"`
		Args      map[string]any `json:"args"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode problem: %v; body=%s", err, body)
	}
	if doc.MessageID != "server.i18n.unknown_language" {
		t.Fatalf("messageId = %q, want server.i18n.unknown_language", doc.MessageID)
	}
	if doc.Args["language"] != "xx" {
		t.Fatalf("args.language = %v, want xx", doc.Args["language"])
	}
}

// A tag the shipped catalog does not list is rejected even when it looks valid.
// languageListed is overridden for "fr" only, and restored afterwards.
func TestPutLanguage_ShippedButUnlistedRejected(t *testing.T) {
	prev := languageListed
	languageListed = func(tag string) bool {
		if tag == "fr" {
			return false
		}
		return prev(tag)
	}
	t.Cleanup(func() { languageListed = prev })

	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-unlisted", "viewer", "")

	status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{"language":"fr"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT fr: status %d, want 400 body=%s", status, body)
	}
	if string(body) != "unknown language: fr\n" {
		t.Fatalf("body = %q", body)
	}
	if got := storedLanguage(t, srv, userID); got != nil {
		t.Fatalf("stored language = %q after rejected PUT, want nil", *got)
	}
}

func TestPutLanguage_MissingKeyRejected(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-missing", "viewer", "")

	status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT {}: status %d, want 400 body=%s", status, body)
	}
	if string(body) != "language is required\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestPutLanguage_UnauthenticatedRejected(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)

	status, _, _ := doLanguageReq(t, srv, 0, http.MethodPut, "/users/me/language", "", `{"language":"en"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated PUT: status %d, want 401", status)
	}
}

// PUT /users/me/preferences must accept a body that carries a language key
// without changing the saved language.
func TestPutPreferences_IgnoresLanguageKey(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "lang-prefs-ignore", "viewer", "")

	if status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/language", "", `{"language":"en"}`); status != http.StatusNoContent {
		t.Fatalf("seed en: status %d body=%s", status, body)
	}
	status, _, body := doLanguageReq(t, srv, userID, http.MethodPut, "/users/me/preferences",
		"", `{"themeType":"preset","presetId":"pink","appearanceMode":"dark","customCssEnabled":false,"language":"xx"}`)
	if status != http.StatusOK {
		t.Fatalf("PUT preferences with language key: status %d body=%s", status, body)
	}
	if got := storedLanguage(t, srv, userID); got == nil || *got != "en" {
		t.Fatalf("stored language = %v, want en unchanged", got)
	}
}
