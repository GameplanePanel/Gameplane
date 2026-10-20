package auth

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func newAuthDB(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func seedUser(t *testing.T, s *db.Store, username, pw, role string) {
	t.Helper()
	SetFastHashParams(t)
	hash, err := HashPassword(context.Background(), pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	_, err = s.DB.ExecContext(context.Background(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		username, username, username+"@example.com", role, hash,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestLogin_RejectsNonJSONContentType(t *testing.T) {
	s := newAuthDB(t)
	l := NewLocal(s)
	ss := NewSessionStore(s)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", strings.NewReader("user=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	l.HandleLogin(ss, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestLogin_BadJSON(t *testing.T) {
	s := newAuthDB(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestLogin_UnknownUser_TimingPath(t *testing.T) {
	s := newAuthDB(t)
	body := strings.NewReader(`{"username":"ghost","password":"hunter2"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "rightpw", "admin")
	body := strings.NewReader(`{"username":"alice","password":"wrong"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestLogin_Success_SetsCookies(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "hunter2", "admin")
	// Seed a cluster-wide viewer role binding so perms actually load.
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (1, 'viewer', 'local', '*')`,
	); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	body := strings.NewReader(`{"username":"alice","password":"hunter2"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body)
	}
	// Assert raw JSON uses camelCase and not PascalCase.
	bodyBytes := rr.Body.Bytes()
	bodyStr := string(bodyBytes)
	if !strings.Contains(bodyStr, `"username"`) {
		t.Fatalf("raw JSON missing camelCase 'username': %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"permissions"`) {
		t.Fatalf("raw JSON missing camelCase 'permissions': %s", bodyStr)
	}
	if strings.Contains(bodyStr, `"Username"`) {
		t.Fatalf("raw JSON contains PascalCase 'Username': %s", bodyStr)
	}
	if strings.Contains(bodyStr, `"Perms"`) {
		t.Fatalf("raw JSON contains PascalCase 'Perms': %s", bodyStr)
	}
	// Decode and verify permissions loaded.
	var got loginResp
	if err := json.NewDecoder(strings.NewReader(bodyStr)).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.User.Username != "alice" || got.CSRF == "" {
		t.Fatalf("got %+v", got)
	}
	// Viewer role grants servers:read cluster-wide.
	if len(got.User.Permissions) == 0 || len(got.User.Permissions["*"]) == 0 {
		t.Fatalf("permissions empty or missing cluster-wide: %+v", got.User.Permissions)
	}
	var hasServersRead bool
	for _, p := range got.User.Permissions["*"] {
		if p == "servers:read" {
			hasServersRead = true
			break
		}
	}
	if !hasServersRead {
		t.Fatalf("cluster-wide permissions missing 'servers:read': %+v", got.User.Permissions["*"])
	}
	cookies := rr.Result().Cookies()
	var session, csrf bool
	for _, c := range cookies {
		if c.Name == sessionCookie {
			session = true
		}
		if c.Name == csrfCookie {
			csrf = true
		}
	}
	if !session || !csrf {
		t.Fatalf("cookies not set: %+v", cookies)
	}
}

func TestLogin_PerUserRateLimit(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "hunter2", "admin")
	// Drain the per-user bucket for "alice".
	for i := 0; i < 6; i++ {
		LoginUserLimiter.AllowUser("alice")
	}
	body := strings.NewReader(`{"username":"alice","password":"hunter2"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("code=%d", rr.Code)
	}
}

// TestLogin_EmbedsPreferences verifies POST /auth/login's response carries
// the same "preferences" shape GET /users/me does (D3, 2026-09-23) so the
// dashboard can seed the correct theme without a second round-trip.
func TestLogin_EmbedsPreferences(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "prefs-alice", "hunter2", "admin")
	// Look up the seeded user's id to seed preferences through the store API.
	var id int64
	if err := s.DB.QueryRowContext(context.Background(), "SELECT id FROM users WHERE username = ?", "prefs-alice").Scan(&id); err != nil {
		t.Fatalf("query user id: %v", err)
	}
	// Seed a non-default preferences row so the assertion can't pass on
	// DefaultUserPreferences() alone.
	if _, err := s.UpsertPreferences(context.Background(), id, db.UserPreferences{
		ThemeType:      "preset",
		PresetID:       "legacy",
		AppearanceMode: "dark",
	}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}

	body := strings.NewReader(`{"username":"prefs-alice","password":"hunter2"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), `"preferences"`) {
		t.Fatalf("login response missing 'preferences': %s", rr.Body)
	}
	var got loginResp
	if err := json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.User.Preferences == nil {
		t.Fatal("decoded login response has nil Preferences")
	}
	if got.User.Preferences.PresetID != "legacy" || got.User.Preferences.AppearanceMode != "dark" {
		t.Errorf("preferences = %+v, want legacy/dark", got.User.Preferences)
	}
}

// TestLogin_Success_AttributesAuditActor pins that a successful login names
// the user on the request's audit actor holder.
func TestLogin_Success_AttributesAuditActor(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "actor-alice", "hunter2", "admin")
	ctx, holder := WithActorHolder(context.Background())
	body := strings.NewReader(`{"username":"actor-alice","password":"hunter2"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body)
	}
	if got := holder.Name(); got != "actor-alice" {
		t.Errorf("audit actor = %q, want %q", got, "actor-alice")
	}
}

// TestLogin_WrongPassword_LeavesAuditActorAnonymous pins that a failed login
// does not attribute the audit row to the attempted username.
func TestLogin_WrongPassword_LeavesAuditActorAnonymous(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "actor-bob", "rightpw", "admin")
	ctx, holder := WithActorHolder(context.Background())
	body := strings.NewReader(`{"username":"actor-bob","password":"wrong"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, "POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
	if got := holder.Name(); got != "" {
		t.Errorf("audit actor = %q, want empty", got)
	}
}

// problemAccept is the Accept header a problem-document caller sends.
const problemAccept = "application/json, application/problem+json"

// loginWithAccept runs POST /login with a JSON body and an optional Accept
// header, returning the recorded response.
func loginWithAccept(t *testing.T, l *Local, ss *SessionStore, body, accept string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	l.HandleLogin(ss, nil).ServeHTTP(rr, req)
	return rr
}

// decodeProblemDoc decodes an RFC 9457 problem body and fails the test when
// the response is not a problem document.
func decodeProblemDoc(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type=%q, want application/problem+json", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem: %v (body %q)", err, rr.Body.String())
	}
	return doc
}

// TestLogin_ProblemDetails_UnknownUserAndWrongPasswordIdentical checks that a
// problem-document caller cannot tell an unknown user from a wrong password
// (FR-030): both get the same registered id, detail and raw body.
func TestLogin_ProblemDetails_UnknownUserAndWrongPasswordIdentical(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "pd-alice", "hunter2", "admin")
	l := NewLocal(s)
	ss := NewSessionStore(s)

	unknown := loginWithAccept(t, l, ss, `{"username":"pd-nobody","password":"x"}`, problemAccept)
	wrong := loginWithAccept(t, l, ss, `{"username":"pd-alice","password":"wrong"}`, problemAccept)

	for name, rr := range map[string]*httptest.ResponseRecorder{"unknown user": unknown, "wrong password": wrong} {
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: code=%d, want 401", name, rr.Code)
		}
		doc := decodeProblemDoc(t, rr)
		if doc["messageId"] != "server.auth.invalid_credentials" {
			t.Errorf("%s: messageId=%v", name, doc["messageId"])
		}
		if doc["detail"] != "invalid credentials" {
			t.Errorf("%s: detail=%v", name, doc["detail"])
		}
		if args, ok := doc["args"].(map[string]any); !ok || len(args) != 0 {
			t.Errorf("%s: args=%v, want empty object", name, doc["args"])
		}
	}
	if unknown.Body.String() != wrong.Body.String() {
		t.Fatalf("bodies differ:\n unknown=%q\n wrong=%q", unknown.Body.String(), wrong.Body.String())
	}
}

// TestLogin_PlainTextBodyUnchanged checks that a caller without a problem
// Accept header still gets the byte-identical plain-text body.
func TestLogin_PlainTextBodyUnchanged(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "pd-bob", "hunter2", "admin")
	l := NewLocal(s)
	ss := NewSessionStore(s)

	unknown := loginWithAccept(t, l, ss, `{"username":"pd-nobody","password":"x"}`, "")
	wrong := loginWithAccept(t, l, ss, `{"username":"pd-bob","password":"wrong"}`, "")

	for name, rr := range map[string]*httptest.ResponseRecorder{"unknown user": unknown, "wrong password": wrong} {
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: code=%d, want 401", name, rr.Code)
		}
		if rr.Body.String() != "invalid credentials\n" {
			t.Errorf("%s: body=%q", name, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Errorf("%s: Content-Type=%q", name, ct)
		}
	}
}

// TestLogin_OtherRejectionsUseGenericIDs checks that the non-credential
// rejections on POST /login answer with the generic id for their status.
func TestLogin_OtherRejectionsUseGenericIDs(t *testing.T) {
	s := newAuthDB(t)
	l := NewLocal(s)
	ss := NewSessionStore(s)

	wrongType := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", strings.NewReader("user=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", problemAccept)
	l.HandleLogin(ss, nil).ServeHTTP(wrongType, req)

	badJSON := loginWithAccept(t, l, ss, "not json", problemAccept)

	for range 6 {
		LoginUserLimiter.AllowUser("pd-drained")
	}
	limited := loginWithAccept(t, l, ss, `{"username":"pd-drained","password":"x"}`, problemAccept)

	cases := []struct {
		name   string
		rr     *httptest.ResponseRecorder
		status int
		detail string
	}{
		{"non-JSON content type", wrongType, http.StatusUnsupportedMediaType, "expected application/json"},
		{"invalid JSON body", badJSON, http.StatusBadRequest, "bad request"},
		{"per-user rate limit", limited, http.StatusTooManyRequests, "too many requests"},
	}
	for _, c := range cases {
		if c.rr.Code != c.status {
			t.Fatalf("%s: code=%d, want %d", c.name, c.rr.Code, c.status)
		}
		doc := decodeProblemDoc(t, c.rr)
		want := "server.generic." + strconv.Itoa(c.status)
		if doc["messageId"] != want {
			t.Errorf("%s: messageId=%v, want %s", c.name, doc["messageId"], want)
		}
		if doc["detail"] != c.detail {
			t.Errorf("%s: detail=%v, want %q", c.name, doc["detail"], c.detail)
		}
	}
}

// TestUnauthenticatedFilesUseOnlyGenericIDs guards FR-030 at the source
// level: the unauthenticated login and OIDC callback files may only register
// the invalid-credentials message, and every other error they write must be
// a generic 5xx.
func TestUnauthenticatedFilesUseOnlyGenericIDs(t *testing.T) {
	for _, name := range []string{"local.go", "oidc.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			switch {
			case pkg.Name == "messages" && sel.Sel.Name == "New":
				if len(call.Args) == 0 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: messages.New with a non-literal id", pos)
					return true
				}
				id, err := strconv.Unquote(lit.Value)
				if err != nil || id != "server.auth.invalid_credentials" {
					t.Errorf("%s: messages.New(%s) is not the invalid-credentials id", pos, lit.Value)
				}
			case pkg.Name == "http" && sel.Sel.Name == "Error":
				if len(call.Args) != 3 {
					return true
				}
				status, ok := call.Args[2].(*ast.SelectorExpr)
				if !ok || status.Sel.Name != "StatusInternalServerError" {
					t.Errorf("%s: http.Error with a status other than a 5xx selector", pos)
				}
			}
			return true
		})
	}
}
