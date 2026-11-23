//go:build envtest

package handlers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// sftpSeq keeps usernames unique across tests that share the database and the package-wide limiters.
var sftpSeq atomic.Int64

var errSFTPFakeNotFound = errors.New("sftp fake: server not found")

// sftpFakeServers serves GameServers to the file checker without a cluster.
type sftpFakeServers struct {
	items []unstructured.Unstructured
}

func (f *sftpFakeServers) GetServer(_ context.Context, _, ns, name string) (*unstructured.Unstructured, error) {
	for i := range f.items {
		if f.items[i].GetNamespace() == ns && f.items[i].GetName() == name {
			return &f.items[i], nil
		}
	}
	return nil, errSFTPFakeNotFound
}

func (f *sftpFakeServers) IDs() []string { return []string{scope.DefaultCluster} }

func (f *sftpFakeServers) ListServers(_ context.Context, _, ns string) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured
	for _, it := range f.items {
		if it.GetNamespace() == ns {
			out = append(out, it)
		}
	}
	return out, nil
}

// sftpFakeServer returns a GameServer in ns owned by ownerID.
func sftpFakeServer(ns, name string, ownerID int64) unstructured.Unstructured {
	u := unstructured.Unstructured{}
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetAnnotations(map[string]string{"gameplane.local/owner-id": strconv.FormatInt(ownerID, 10)})
	return u
}

// sftpLoginEnv is a migrated store with one SFTP service token for the default cluster.
type sftpLoginEnv struct {
	store    *db.Store
	sessions *auth.SessionStore
	fake     *sftpFakeServers
	files    *rbac.FileChecker
	aud      *audit.Auditor
	svcRaw   string
	svcID    int64
}

func newSFTPLoginEnv(t *testing.T) sftpLoginEnv {
	t.Helper()
	auth.SetFastHashParams(t)
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	ctx := t.Context()
	tok, raw, err := sessions.CreateSFTPServiceToken(ctx, scope.DefaultCluster)
	if err != nil {
		t.Fatalf("create service token: %v", err)
	}
	t.Cleanup(func() {
		_ = sessions.RevokeSFTPServiceToken(context.Background(), scope.DefaultCluster, tok.ID)
	})
	fake := &sftpFakeServers{}
	return sftpLoginEnv{
		store: store, sessions: sessions, fake: fake,
		files: rbac.NewFileAccess(fake, fake), aud: audit.New(store),
		svcRaw: raw, svcID: tok.ID,
	}
}

// seedUser inserts a user with the given dashboard password hash ("x" is not a valid hash, so the
// dashboard login cannot succeed for it).
func (env sftpLoginEnv) seedUser(t *testing.T, pwHash string) (string, int64) {
	t.Helper()
	name := fmt.Sprintf("sftpuser%d", sftpSeq.Add(1))
	if _, err := env.store.DB.ExecContext(t.Context(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
		name, name, name+"@example.com", "viewer", pwHash); err != nil {
		t.Fatalf("insert user %s: %v", name, err)
	}
	var id int64
	if err := env.store.DB.QueryRowContext(t.Context(),
		`SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
		t.Fatalf("user id %s: %v", name, err)
	}
	return name, id
}

// login posts to the SFTP login route from a fresh source address, so per-address buckets never fill.
func (env sftpLoginEnv) login(t *testing.T, h http.Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	n := sftpSeq.Add(1)
	body["remoteAddr"] = fmt.Sprintf("10.0.%d.%d:40000", (n>>8)&255, n&255)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sftp/v1/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.svcRaw)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func sftpLoginRouter(env sftpLoginEnv) http.Handler {
	r := chi.NewRouter()
	MountSFTPLogin(r, env.sessions, env.store, env.files, env.aud)
	return r
}

// sftpProblemID returns the messageId of a problem response.
func sftpProblemID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem body %q: %v", rr.Body.String(), err)
	}
	return p.MessageID
}

// sftpTestEd25519Line returns a fresh ed25519 authorized_keys line.
func sftpTestEd25519Line(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

func TestSFTPLogin_FailuresShareOneAnswer(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)

	// A user with a dashboard password but no SFTP password, and an OIDC-only account.
	dashHash, err := auth.HashPassword(t.Context(), "dashboard-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	dashName, _ := env.seedUser(t, dashHash)
	oidcName, _ := env.seedUser(t, "")
	pwName, _ := env.seedUser(t, "x")
	if _, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), userIDOf(t, env, pwName), 24); err != nil {
		t.Fatalf("set sftp password: %v", err)
	}

	cases := map[string]map[string]any{
		"wrong password":        {"username": pwName, "password": "not-the-password"},
		"unknown user":          {"username": "no-such-user-zz", "password": "whatever"},
		"dashboard password":    {"username": dashName, "password": "dashboard-pass"},
		"oidc only account":     {"username": oidcName, "password": "anything"},
		"malformed key":         {"username": pwName, "publicKey": "ssh-ed25519 AAAA-bogus"},
		"two credentials":       {"username": pwName, "password": "x", "publicKey": "ssh-ed25519 AAAA"},
		"bad target name":       {"username": pwName + "+Bad_Name", "password": "x"},
		"target with three rel": {"username": pwName + "+a/b/c", "password": "x"},
	}
	var first string
	for name, body := range cases {
		rr := env.login(t, h, body)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401 (body %s)", name, rr.Code, rr.Body.String())
		}
		if id := sftpProblemID(t, rr); id != "server.sftp.login_failed" {
			t.Fatalf("%s: messageId %q", name, id)
		}
		if first == "" {
			first = rr.Body.String()
		} else if rr.Body.String() != first {
			t.Fatalf("%s: body differs from the other refusals: %s vs %s", name, rr.Body.String(), first)
		}
	}
}

func TestSFTPLogin_PasswordSuccessAndAudit(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, uid := env.seedUser(t, "x")
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("set sftp password: %v", err)
	}

	rr := env.login(t, h, map[string]any{"username": name, "password": pw})
	if rr.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		SessionToken string  `json:"sessionToken"`
		User         string  `json:"user"`
		Target       *string `json:"target"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if !strings.HasPrefix(resp.SessionToken, auth.SFTPSessionTokenPrefix) || resp.User != name || resp.Target != nil {
		t.Fatalf("unexpected login response: %+v", resp)
	}

	var source sql.NullString
	if err := env.store.DB.QueryRowContext(t.Context(),
		`SELECT source FROM audit_events WHERE actor = ? AND status = 200 ORDER BY id DESC LIMIT 1`, name,
	).Scan(&source); err != nil {
		t.Fatalf("read success audit row: %v", err)
	}
	if source.String != "sftp" {
		t.Fatalf("audit source %q, want sftp", source.String)
	}
}

func TestSFTPLogin_TargetRequiresAccess(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	owner, ownerID := env.seedUser(t, "x")
	outsider, outsiderID := env.seedUser(t, "x")
	env.fake.items = []unstructured.Unstructured{sftpFakeServer(scope.DefaultNamespace, "survival", ownerID)}
	ownerPW, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), ownerID, 24)
	if err != nil {
		t.Fatalf("owner password: %v", err)
	}
	outsiderPW, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), outsiderID, 24)
	if err != nil {
		t.Fatalf("outsider password: %v", err)
	}

	rr := env.login(t, h, map[string]any{"username": owner + "+survival", "password": ownerPW})
	if rr.Code != http.StatusOK {
		t.Fatalf("owner target login status %d: %s", rr.Code, rr.Body.String())
	}
	rr = env.login(t, h, map[string]any{"username": outsider + "+survival", "password": outsiderPW})
	if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.sftp.login_failed" {
		t.Fatalf("outsider target login: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPLogin_KeyMustBelongToNamedUser(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	alice, aliceID := env.seedUser(t, "x")
	bob, _ := env.seedUser(t, "x")
	line := sftpTestEd25519Line(t)
	if _, err := env.sessions.AddSFTPKey(t.Context(), aliceID, "laptop", line); err != nil {
		t.Fatalf("add key: %v", err)
	}

	if rr := env.login(t, h, map[string]any{"username": alice, "publicKey": line}); rr.Code != http.StatusOK {
		t.Fatalf("alice key login status %d: %s", rr.Code, rr.Body.String())
	}
	if rr := env.login(t, h, map[string]any{"username": bob, "publicKey": line}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("bob with alice's key: status %d", rr.Code)
	}
}

func TestSFTPLogin_RateLimitPerUsername(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, _ := env.seedUser(t, "x")
	// The per-username bucket has a burst of 6: the seventh attempt is limited whatever the answer.
	for i := range 6 {
		rr := env.login(t, h, map[string]any{"username": name, "password": "wrong"})
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, rr.Code)
		}
	}
	if rr := env.login(t, h, map[string]any{"username": name, "password": "wrong"}); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("seventh attempt: status %d, want 429", rr.Code)
	}
}

func TestSFTPLogin_RefusesWithoutServiceToken(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, uid := env.seedUser(t, "x")
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("set sftp password: %v", err)
	}
	raw, err := json.Marshal(map[string]any{"username": name, "password": pw})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// No bearer, and a session token where the service token belongs.
	for _, hdr := range []string{"", "Bearer " + auth.SFTPSessionTokenPrefix + strings.Repeat("A", 43)} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sftp/v1/login", bytes.NewReader(raw))
		req.Header.Set("Accept", "application/problem+json")
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.common.unauthenticated" {
			t.Fatalf("auth %q: status %d body %s", hdr, rr.Code, rr.Body.String())
		}
	}
}

func TestSFTPLogin_CheckReportsRevokedTokens(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, uid := env.seedUser(t, "x")
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("set sftp password: %v", err)
	}
	rr := env.login(t, h, map[string]any{"username": name, "password": pw})
	var login struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &login); err != nil || login.SessionToken == "" {
		t.Fatalf("login: %v body %s", err, rr.Body.String())
	}

	check := func(tokens []string) (int, []string) {
		raw, err := json.Marshal(map[string]any{"sessionTokens": tokens})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sftp/v1/sessions/check", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+env.svcRaw)
		req.Header.Set("Accept", "application/problem+json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		var body struct {
			Revoked []string `json:"revoked"`
		}
		_ = json.Unmarshal(out.Body.Bytes(), &body)
		return out.Code, body.Revoked
	}

	code, revoked := check([]string{login.SessionToken, auth.SFTPSessionTokenPrefix + "unknown"})
	if code != http.StatusOK || len(revoked) != 1 || revoked[0] != auth.SFTPSessionTokenPrefix+"unknown" {
		t.Fatalf("live token check: status %d revoked %v", code, revoked)
	}
	if err := env.sessions.RevokeSFTPSessionToken(t.Context(), login.SessionToken); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, revoked := check([]string{login.SessionToken}); len(revoked) != 1 || revoked[0] != login.SessionToken {
		t.Fatalf("revoked token not reported: %v", revoked)
	}

	tooMany := make([]string, 501)
	for i := range tooMany {
		tooMany[i] = auth.SFTPSessionTokenPrefix + "x"
	}
	if code, _ := check(tooMany); code != http.StatusBadRequest {
		t.Fatalf("501 tokens: status %d, want 400", code)
	}
}

// userIDOf reads a seeded user's id by name.
func userIDOf(t *testing.T, env sftpLoginEnv, name string) int64 {
	t.Helper()
	var id int64
	if err := env.store.DB.QueryRowContext(t.Context(),
		`SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
		t.Fatalf("user id %s: %v", name, err)
	}
	return id
}

// sftpSelfRouter serves the self-service routes as user uid. sftpCaller marks the request as made by an
// SFTP session, which the routes must refuse.
func sftpSelfRouter(sessions *auth.SessionStore, store *db.Store, uid int64, sftpCaller bool) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), &auth.User{ID: uid, Username: "caller", Role: "viewer"})
			if sftpCaller {
				ctx = auth.WithSFTPCaller(ctx, auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 9})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	mountSFTPRoutes(r, sessions, store)
	return r
}

func sftpSelfDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSFTPSelfService_KeysAndPassword(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, aliceID := env.seedUser(t, "x")
	_, bobID := env.seedUser(t, "x")
	alice := sftpSelfRouter(env.sessions, env.store, aliceID, false)
	bob := sftpSelfRouter(env.sessions, env.store, bobID, false)

	line := sftpTestEd25519Line(t)
	keyBody, err := json.Marshal(map[string]string{"name": "laptop", "publicKey": line})
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if rr := sftpSelfDo(t, alice, http.MethodPost, "/me/sftp/keys", string(keyBody)); rr.Code != http.StatusCreated {
		t.Fatalf("add key: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := sftpSelfDo(t, bob, http.MethodPost, "/me/sftp/keys", string(keyBody)); rr.Code != http.StatusConflict ||
		sftpProblemID(t, rr) != "server.sftp.key_duplicate" {
		t.Fatalf("duplicate key for bob: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := sftpSelfDo(t, alice, http.MethodPost, "/me/sftp/keys", `{"name":"x","publicKey":"not a key"}`); rr.Code != http.StatusBadRequest ||
		sftpProblemID(t, rr) != "server.sftp.key_invalid" {
		t.Fatalf("invalid key: status %d body %s", rr.Code, rr.Body.String())
	}

	rr := sftpSelfDo(t, alice, http.MethodGet, "/me/sftp", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"endpoint":null`) ||
		!strings.Contains(rr.Body.String(), `"password":null`) {
		t.Fatalf("get self: status %d body %s", rr.Code, rr.Body.String())
	}

	gen := sftpSelfDo(t, alice, http.MethodPost, "/me/sftp/password", `{"length":24}`)
	if gen.Code != http.StatusOK || gen.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("generate password: status %d headers %v", gen.Code, gen.Header())
	}
	var pw struct {
		Password string `json:"password"`
		Length   int    `json:"length"`
	}
	if err := json.Unmarshal(gen.Body.Bytes(), &pw); err != nil || len(pw.Password) != 24 || pw.Length != 24 {
		t.Fatalf("generated password body: %v %s", err, gen.Body.String())
	}
	if after := sftpSelfDo(t, alice, http.MethodGet, "/me/sftp", ""); strings.Contains(after.Body.String(), pw.Password) {
		t.Fatalf("GET /me/sftp leaked the password")
	}

	if rr := sftpSelfDo(t, alice, http.MethodPost, "/me/sftp/password", `{"length":5}`); rr.Code != http.StatusBadRequest ||
		sftpProblemID(t, rr) != "server.sftp.password_length_invalid" {
		t.Fatalf("short password: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := sftpSelfDo(t, alice, http.MethodDelete, "/me/sftp/password", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("revoke password: status %d", rr.Code)
	}
	if rr := sftpSelfDo(t, alice, http.MethodDelete, "/me/sftp/password", ""); rr.Code != http.StatusNotFound ||
		sftpProblemID(t, rr) != "server.sftp.password_not_found" {
		t.Fatalf("revoke missing password: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPSelfService_RefusesSFTPCaller(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	h := sftpSelfRouter(env.sessions, env.store, uid, true)
	rr := sftpSelfDo(t, h, http.MethodGet, "/me/sftp", "")
	if rr.Code != http.StatusForbidden || sftpProblemID(t, rr) != "server.forbidden" {
		t.Fatalf("sftp caller on /me/sftp: status %d body %s", rr.Code, rr.Body.String())
	}
}
