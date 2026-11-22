package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// mcpTokenEnv is a migrated store with two users, each with a unique name.
type mcpTokenEnv struct {
	store    *db.Store
	sessions *auth.SessionStore
	alice    int64
	bob      int64
}

func newMCPTokenEnv(t *testing.T) mcpTokenEnv {
	t.Helper()
	store := newTestStore(t)
	ctx := context.Background()
	prefix := t.Name()
	for _, suffix := range []string{"alice", "bob"} {
		name := prefix + "-" + suffix
		if _, err := store.DB.ExecContext(ctx,
			`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
			name, name, name+"@example.com", "viewer", "x"); err != nil {
			t.Fatalf("insert user %s: %v", name, err)
		}
	}
	env := mcpTokenEnv{store: store, sessions: auth.NewSessionStore(store)}
	for suffix, dst := range map[string]*int64{"alice": &env.alice, "bob": &env.bob} {
		if err := store.DB.QueryRowContext(ctx,
			`SELECT id FROM users WHERE username = ?`, prefix+"-"+suffix).Scan(dst); err != nil {
			t.Fatalf("user id %s: %v", suffix, err)
		}
	}
	return env
}

// mcpTokenRouter serves the token routes as user uid. tokenAuthed marks the
// request as authenticated by an MCP token, which the routes must refuse.
func mcpTokenRouter(sessions *auth.SessionStore, uid int64, tokenAuthed bool) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), &auth.User{ID: uid, Username: "caller", Role: "admin"})
			if tokenAuthed {
				ctx = auth.WithMCPToken(ctx, 77)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/users", func(r chi.Router) {
		mountMCPTokenRoutes(r, sessions)
	})
	return r
}

// mcpTokenDo sends a request. A string body is sent as-is; any other non-nil
// body is JSON-encoded. Accept asks for problem documents so messageId is readable.
func mcpTokenDo(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, rd)
	req.Header.Set("Accept", "application/problem+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// mcpTokenProblemID returns the messageId of a problem document response.
func mcpTokenProblemID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var doc struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem document: %v; body=%s", err, rr.Body.String())
	}
	return doc.MessageID
}

type mcpCreatedBody struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	ExpiresAt string `json:"expiresAt"`
	Token     string `json:"token"`
}

func mcpTokenCreate(t *testing.T, h http.Handler, name string, days int) mcpCreatedBody {
	t.Helper()
	rr := mcpTokenDo(t, h, http.MethodPost, "/users/me/mcp-tokens", map[string]any{"name": name, "expiresInDays": days})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create %q: code=%d body=%s", name, rr.Code, rr.Body.String())
	}
	var out mcpCreatedBody
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return out
}

func TestMCPTokens_CreateListRevokeSelf(t *testing.T) {
	env := newMCPTokenEnv(t)
	h := mcpTokenRouter(env.sessions, env.alice, false)

	created := mcpTokenCreate(t, h, "laptop", 30)
	if !strings.HasPrefix(created.Token, auth.MCPTokenPrefix) || len(created.Token) != len(auth.MCPTokenPrefix)+43 {
		t.Fatalf("token = %q, want gpmcp_ + 43 characters", created.Token)
	}
	if created.Prefix != created.Token[:10] || created.Name != "laptop" || created.ID == 0 {
		t.Fatalf("create response = %+v", created)
	}

	list := mcpTokenDo(t, h, http.MethodGet, "/users/me/mcp-tokens", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: code=%d body=%s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), created.Token) {
		t.Fatal("list response contains the token secret")
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("list has %d items, want 1", len(listed.Items))
	}
	item := listed.Items[0]
	if _, has := item["token"]; has {
		t.Fatalf("list item carries a token field: %+v", item)
	}
	if got, _ := item["id"].(float64); int64(got) != created.ID {
		t.Fatalf("list item id = %v, want %d", item["id"], created.ID)
	}
	if item["revokedAt"] != nil {
		t.Fatalf("new token shows revokedAt: %+v", item)
	}

	rev := mcpTokenDo(t, h, http.MethodDelete, fmt.Sprintf("/users/me/mcp-tokens/%d", created.ID), nil)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke: code=%d body=%s", rev.Code, rev.Body.String())
	}
	list = mcpTokenDo(t, h, http.MethodGet, "/users/me/mcp-tokens", nil)
	if !strings.Contains(list.Body.String(), `"revokedAt":"`) {
		t.Fatalf("revoked token not marked in list: %s", list.Body.String())
	}
}

func TestMCPTokens_AdminListAndRevokeByUserID(t *testing.T) {
	env := newMCPTokenEnv(t)
	bobSelf := mcpTokenRouter(env.sessions, env.bob, false)
	bobTok := mcpTokenCreate(t, bobSelf, "bob-laptop", 90)

	admin := mcpTokenRouter(env.sessions, env.alice, false)
	list := mcpTokenDo(t, admin, http.MethodGet, fmt.Sprintf("/users/%d/mcp-tokens", env.bob), nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"bob-laptop"`) {
		t.Fatalf("admin list for bob: code=%d body=%s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), bobTok.Token) {
		t.Fatal("admin list contains the token secret")
	}

	bad := mcpTokenDo(t, admin, http.MethodGet, "/users/abc/mcp-tokens", nil)
	if bad.Code != http.StatusBadRequest || mcpTokenProblemID(t, bad) != "server.bad_request" {
		t.Fatalf("non-numeric user id: code=%d body=%s", bad.Code, bad.Body.String())
	}

	// Alice cannot revoke bob's token through her own user ID.
	wrong := mcpTokenDo(t, admin, http.MethodDelete,
		fmt.Sprintf("/users/%d/mcp-tokens/%d", env.alice, bobTok.ID), nil)
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("revoke under wrong user: code=%d, want 404", wrong.Code)
	}

	rev := mcpTokenDo(t, admin, http.MethodDelete,
		fmt.Sprintf("/users/%d/mcp-tokens/%d", env.bob, bobTok.ID), nil)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("admin revoke: code=%d body=%s", rev.Code, rev.Body.String())
	}
}

func TestMCPTokens_ErrorIDs(t *testing.T) {
	type errCase struct {
		name   string
		method string
		path   string
		body   any
		code   int
		id     string
	}
	env := newMCPTokenEnv(t)
	h := mcpTokenRouter(env.sessions, env.alice, false)
	mcpTokenCreate(t, h, "dup", 30)

	cases := []errCase{
		{name: "empty name", method: http.MethodPost, path: "/users/me/mcp-tokens",
			body: map[string]any{"name": "", "expiresInDays": 30}, code: http.StatusBadRequest, id: "server.mcp_tokens.invalid_name"},
		{name: "bad lifetime", method: http.MethodPost, path: "/users/me/mcp-tokens",
			body: map[string]any{"name": "x", "expiresInDays": 14}, code: http.StatusBadRequest, id: "server.mcp_tokens.invalid_lifetime"},
		{name: "malformed json", method: http.MethodPost, path: "/users/me/mcp-tokens",
			body: "{", code: http.StatusBadRequest, id: "server.common.invalid_json"},
		{name: "duplicate name", method: http.MethodPost, path: "/users/me/mcp-tokens",
			body: map[string]any{"name": "dup", "expiresInDays": 30}, code: http.StatusConflict, id: "server.mcp_tokens.duplicate_name"},
		{name: "unknown token", method: http.MethodDelete, path: "/users/me/mcp-tokens/999999",
			code: http.StatusNotFound, id: "server.mcp_tokens.not_found"},
		{name: "non-numeric token id", method: http.MethodDelete, path: "/users/me/mcp-tokens/abc",
			code: http.StatusBadRequest, id: "server.bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := mcpTokenDo(t, h, tc.method, tc.path, tc.body)
			if rr.Code != tc.code {
				t.Fatalf("code=%d, want %d; body=%s", rr.Code, tc.code, rr.Body.String())
			}
			if got := mcpTokenProblemID(t, rr); got != tc.id {
				t.Fatalf("messageId=%q, want %q", got, tc.id)
			}
		})
	}

	t.Run("limit reached", func(t *testing.T) {
		limitEnv := newMCPTokenEnv(t)
		lh := mcpTokenRouter(limitEnv.sessions, limitEnv.alice, false)
		for i := range 10 {
			mcpTokenCreate(t, lh, fmt.Sprintf("t%d", i), 7)
		}
		rr := mcpTokenDo(t, lh, http.MethodPost, "/users/me/mcp-tokens",
			map[string]any{"name": "t10", "expiresInDays": 7})
		if rr.Code != http.StatusUnprocessableEntity || mcpTokenProblemID(t, rr) != "server.mcp_tokens.limit_reached" {
			t.Fatalf("11th token: code=%d body=%s", rr.Code, rr.Body.String())
		}
	})
}

func TestMCPTokens_RefusesTokenCaller(t *testing.T) {
	env := newMCPTokenEnv(t)
	h := mcpTokenRouter(env.sessions, env.alice, true)

	requests := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/users/me/mcp-tokens", nil},
		{http.MethodPost, "/users/me/mcp-tokens", map[string]any{"name": "x", "expiresInDays": 7}},
		{http.MethodDelete, "/users/me/mcp-tokens/1", nil},
		{http.MethodGet, fmt.Sprintf("/users/%d/mcp-tokens", env.alice), nil},
		{http.MethodDelete, fmt.Sprintf("/users/%d/mcp-tokens/1", env.alice), nil},
	}
	for _, rq := range requests {
		rr := mcpTokenDo(t, h, rq.method, rq.path, rq.body)
		if rr.Code != http.StatusForbidden || mcpTokenProblemID(t, rr) != "server.forbidden" {
			t.Fatalf("%s %s: code=%d id=%s, want 403 server.forbidden", rq.method, rq.path, rr.Code, mcpTokenProblemID(t, rr))
		}
	}

	var n int
	if err := env.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mcp_tokens`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("token caller created or changed %d rows", n)
	}
}
