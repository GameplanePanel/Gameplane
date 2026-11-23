package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

var errSFTPUnitList = errors.New("sftp unit: list failed")

// sftpUnitFailingLister fails every list call.
type sftpUnitFailingLister struct{ sftpFakeServers }

func (*sftpUnitFailingLister) ListServers(context.Context, string, string) ([]unstructured.Unstructured, error) {
	return nil, errSFTPUnitList
}

// sftpUnitRawPost posts a raw body to the login-side router with the service token.
func sftpUnitRawPost(t *testing.T, env sftpLoginEnv, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.svcRaw)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSFTPLoginUnit_InvalidJSONAndMissingCaller(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	for _, path := range []string{"/sftp/v1/login", "/sftp/v1/sessions/check"} {
		rr := sftpUnitRawPost(t, env, h, path, "{not json")
		if rr.Code != http.StatusBadRequest || sftpProblemID(t, rr) != "server.common.invalid_json" {
			t.Fatalf("%s: status %d body %s", path, rr.Code, rr.Body.String())
		}
	}

	// Handlers called without the service-auth middleware have no caller in the context.
	lh := &sftpLoginHandler{sessions: env.sessions, store: env.store, files: env.files, aud: env.aud}
	for name, fn := range map[string]http.HandlerFunc{"login": lh.login, "check": lh.check} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", strings.NewReader("{}"))
		req.Header.Set("Accept", "application/problem+json")
		rr := httptest.NewRecorder()
		fn(rr, req)
		if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.common.unauthenticated" {
			t.Fatalf("%s without caller: status %d body %s", name, rr.Code, rr.Body.String())
		}
	}
}

func TestSFTPLoginUnit_UnregisteredKeyAndTargetForms(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, uid := env.seedUser(t, "x")

	// A well-formed key nobody registered is refused like every other failure.
	rr := env.login(t, h, map[string]any{"username": name, "publicKey": sftpTestEd25519Line(t)})
	if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.sftp.login_failed" {
		t.Fatalf("unregistered key: status %d body %s", rr.Code, rr.Body.String())
	}

	// "namespace/server" targets resolve to that namespace; a namespace outside the allowed set fails.
	env.fake.items = []unstructured.Unstructured{sftpFakeServer(scope.DefaultNamespace, "survival", uid)}
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	rr = env.login(t, h, map[string]any{"username": name + "+" + scope.DefaultNamespace + "/survival", "password": pw})
	if rr.Code != http.StatusOK {
		t.Fatalf("namespaced target login: status %d body %s", rr.Code, rr.Body.String())
	}
	var resp sftpLoginResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Target == nil || resp.Target.Server != "survival" || resp.Target.Namespace != scope.DefaultNamespace {
		t.Fatalf("target = %+v", resp.Target)
	}
	rr = env.login(t, h, map[string]any{"username": name + "+not-allowed-ns-zz/survival", "password": pw})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disallowed namespace: status %d", rr.Code)
	}
}

func TestSFTPLoginUnit_AuditFailureRefusesAndRevokes(t *testing.T) {
	env := newSFTPLoginEnv(t)
	name, uid := env.seedUser(t, "x")
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("password: %v", err)
	}

	// The audit trail lives in a store that is already closed, so no row can land.
	deadStore := newTestStore(t)
	if err := deadStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	env.aud = audit.New(deadStore)
	h := sftpLoginRouter(env)

	rr := env.login(t, h, map[string]any{"username": name, "password": pw})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unaudited success: status %d body %s", rr.Code, rr.Body.String())
	}
	var live int
	if err := env.store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sftp_sessions WHERE user_id = ? AND revoked_at IS NULL`, uid).Scan(&live); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if live != 0 {
		t.Fatalf("%d live sessions left after an unaudited login", live)
	}

	// A failed login whose audit row cannot be written is still refused with the usual answer.
	rr = env.login(t, h, map[string]any{"username": name, "password": "wrong"})
	if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.sftp.login_failed" {
		t.Fatalf("unaudited failure: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPLoginUnit_CheckStoreFailure(t *testing.T) {
	env := newSFTPLoginEnv(t)
	deadStore := newTestStore(t)
	deadSessions := auth.NewSessionStore(deadStore)
	if err := deadStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	h := &sftpLoginHandler{sessions: deadSessions, store: deadStore, files: env.files, aud: env.aud}
	body, err := json.Marshal(map[string]any{"sessionTokens": []string{auth.SFTPSessionTokenPrefix + "abc", "plain", auth.SFTPSessionTokenPrefix + "abc"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequestWithContext(auth.WithSFTPCaller(t.Context(), auth.SFTPCaller{Cluster: scope.DefaultCluster}),
		http.MethodPost, "/sftp/v1/sessions/check", bytes.NewReader(body))
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.check(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("check on a dead store: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPLoginUnit_CheckDeduplicatesAndReportsPlainTokens(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	dup := auth.SFTPSessionTokenPrefix + "dup"
	raw, err := json.Marshal(map[string]any{"sessionTokens": []string{"not-a-session-token", dup, dup}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rr := sftpUnitRawPost(t, env, h, "/sftp/v1/sessions/check", string(raw))
	var out struct {
		Revoked []string `json:"revoked"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("status %d err %v body %s", rr.Code, err, rr.Body.String())
	}
	if len(out.Revoked) != 2 || out.Revoked[0] != "not-a-session-token" || out.Revoked[1] != dup {
		t.Fatalf("revoked = %v", out.Revoked)
	}
}

// sftpServersRouter serves GET /sftp/v1/servers with the given context decoration.
func sftpServersRouter(files *rbac.FileChecker, decorate func(context.Context) context.Context) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(decorate(req.Context())))
		})
	})
	MountSFTPServers(r, files)
	return r
}

func sftpUnitGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSFTPServersUnit_List(t *testing.T) {
	fake := &sftpFakeServers{items: []unstructured.Unstructured{
		sftpFakeServer(scope.DefaultNamespace, "mine", 7),
		sftpFakeServer(scope.DefaultNamespace, "theirs", 8),
	}}
	files := rbac.NewFileAccess(fake, fake)
	user := &auth.User{ID: 7, Username: "seven", Role: "viewer"}
	caller := auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 3}

	ok := sftpServersRouter(files, func(ctx context.Context) context.Context {
		return auth.WithUser(auth.WithSFTPCaller(ctx, caller), user)
	})
	rr := sftpUnitGet(t, ok, "/sftp/v1/servers")
	var out struct {
		Items []sftpServerItem `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("status %d err %v body %s", rr.Code, err, rr.Body.String())
	}
	if len(out.Items) != 1 || out.Items[0].Server != "mine" || out.Items[0].Namespace != scope.DefaultNamespace {
		t.Fatalf("items = %+v, want only the owned server", out.Items)
	}

	cases := map[string]struct {
		files  *rbac.FileChecker
		deco   func(context.Context) context.Context
		status int
		id     string
	}{
		"no caller": {files, func(ctx context.Context) context.Context { return auth.WithUser(ctx, user) },
			http.StatusForbidden, "server.forbidden"},
		"service token caller": {files, func(ctx context.Context) context.Context {
			return auth.WithUser(auth.WithSFTPCaller(ctx, auth.SFTPCaller{Cluster: scope.DefaultCluster}), user)
		}, http.StatusForbidden, "server.forbidden"},
		"no user": {files, func(ctx context.Context) context.Context { return auth.WithSFTPCaller(ctx, caller) },
			http.StatusUnauthorized, "server.common.unauthenticated"},
		"unknown cluster": {rbac.NewFileAccess(nil, nil), func(ctx context.Context) context.Context {
			return auth.WithUser(auth.WithSFTPCaller(ctx, caller), user)
		}, http.StatusBadRequest, ""},
		"list failure": {rbac.NewFileAccess(fake, &sftpUnitFailingLister{}), func(ctx context.Context) context.Context {
			return auth.WithUser(auth.WithSFTPCaller(ctx, caller), user)
		}, http.StatusInternalServerError, ""},
	}
	for name, tc := range cases {
		rr := sftpUnitGet(t, sftpServersRouter(tc.files, tc.deco), "/sftp/v1/servers")
		if rr.Code != tc.status {
			t.Errorf("%s: status %d, want %d (body %s)", name, rr.Code, tc.status, rr.Body.String())
		}
		if tc.id != "" && sftpProblemID(t, rr) != tc.id {
			t.Errorf("%s: messageId %q, want %q", name, sftpProblemID(t, rr), tc.id)
		}
	}
}

func TestSFTPUnit_WriteSFTPErrMapsEveryError(t *testing.T) {
	cases := []struct {
		err    error
		status int
		id     string
	}{
		{auth.ErrSFTPKeyInvalid, http.StatusBadRequest, "server.sftp.key_invalid"},
		{auth.ErrSFTPKeyType, http.StatusBadRequest, "server.sftp.key_type_unsupported"},
		{auth.ErrSFTPKeyWeak, http.StatusBadRequest, "server.sftp.key_weak"},
		{auth.ErrSFTPKeyName, http.StatusBadRequest, "server.sftp.key_name_invalid"},
		{auth.ErrSFTPKeyNameInUse, http.StatusConflict, "server.sftp.key_name_in_use"},
		{auth.ErrSFTPKeyDuplicate, http.StatusConflict, "server.sftp.key_duplicate"},
		{auth.ErrSFTPKeyLimit, 0, "server.sftp.key_limit_reached"},
		{auth.ErrSFTPNotFound, http.StatusNotFound, "server.sftp.key_not_found"},
		{auth.ErrSFTPPasswordLen, http.StatusBadRequest, "server.sftp.password_length_invalid"},
		{errSFTPUnitList, http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
		req.Header.Set("Accept", "application/problem+json")
		rr := httptest.NewRecorder()
		writeSFTPErr(rr, req, tc.err)
		if tc.status != 0 && rr.Code != tc.status {
			t.Errorf("%v: status %d, want %d", tc.err, rr.Code, tc.status)
		}
		if rr.Code < 400 {
			t.Errorf("%v: status %d is not an error", tc.err, rr.Code)
		}
		if got := sftpProblemID(t, rr); tc.id != "" && got != tc.id {
			t.Errorf("%v: messageId %q, want %q", tc.err, got, tc.id)
		}
	}
}

func TestSFTPSelfUnit_EndpointAndKeyLifecycle(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	h := sftpSelfRouter(env.sessions, env.store, uid, false)

	SetSFTPEndpointSource(func(context.Context, string) (SFTPEndpoint, bool) {
		return SFTPEndpoint{Address: "sftp.example.test", Port: 2222, HostKeyFingerprint: "SHA256:abc"}, true
	})
	t.Cleanup(func() { SetSFTPEndpointSource(nil) })

	line := sftpTestEd25519Line(t)
	keyBody, err := json.Marshal(map[string]string{"name": "desk", "publicKey": line})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rr := sftpSelfDo(t, h, http.MethodPost, "/me/sftp/keys", string(keyBody))
	if rr.Code != http.StatusCreated {
		t.Fatalf("add key: status %d body %s", rr.Code, rr.Body.String())
	}
	var key struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &key); err != nil || key.ID == 0 {
		t.Fatalf("key body: %v %s", err, rr.Body.String())
	}
	if rr := sftpSelfDo(t, h, http.MethodPost, "/me/sftp/password", `{"length":24}`); rr.Code != http.StatusOK {
		t.Fatalf("password: status %d", rr.Code)
	}

	rr = sftpSelfDo(t, h, http.MethodGet, "/me/sftp", "")
	var self struct {
		Username string          `json:"username"`
		Endpoint *SFTPEndpoint   `json:"endpoint"`
		Keys     []auth.SFTPKey  `json:"keys"`
		Password json.RawMessage `json:"password"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &self); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("get self: status %d err %v body %s", rr.Code, err, rr.Body.String())
	}
	if self.Username != "caller" || self.Endpoint == nil || self.Endpoint.Port != 2222 || len(self.Keys) != 1 ||
		string(self.Password) == "null" {
		t.Fatalf("self = %+v (password %s)", self, self.Password)
	}

	// An endpoint source that reports nothing leaves the endpoint null.
	SetSFTPEndpointSource(func(context.Context, string) (SFTPEndpoint, bool) { return SFTPEndpoint{}, false })
	if rr := sftpSelfDo(t, h, http.MethodGet, "/me/sftp", ""); !strings.Contains(rr.Body.String(), `"endpoint":null`) {
		t.Fatalf("unconfigured endpoint: %s", rr.Body.String())
	}

	if rr := sftpSelfDo(t, h, http.MethodDelete, "/me/sftp/keys/abc", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad key id: status %d", rr.Code)
	}
	if rr := sftpSelfDo(t, h, http.MethodDelete, "/me/sftp/keys/987654321", ""); rr.Code != http.StatusNotFound ||
		sftpProblemID(t, rr) != "server.sftp.key_not_found" {
		t.Fatalf("missing key: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := sftpSelfDo(t, h, http.MethodDelete, "/me/sftp/keys/"+jsonInt(key.ID), ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete key: status %d body %s", rr.Code, rr.Body.String())
	}
}

func jsonInt(n int64) string {
	b, err := json.Marshal(n)
	if err != nil {
		return "0"
	}
	return string(b)
}

func TestSFTPSelfUnit_InvalidBodiesAndAnonymousCallers(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	h := sftpSelfRouter(env.sessions, env.store, uid, false)

	if rr := sftpSelfDo(t, h, http.MethodPost, "/me/sftp/keys", "{bad"); rr.Code != http.StatusBadRequest ||
		sftpProblemID(t, rr) != "server.common.invalid_json" {
		t.Fatalf("bad key body: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := sftpSelfDo(t, h, http.MethodPost, "/me/sftp/password", "{bad"); rr.Code != http.StatusBadRequest ||
		sftpProblemID(t, rr) != "server.common.invalid_json" {
		t.Fatalf("bad password body: status %d body %s", rr.Code, rr.Body.String())
	}
	// An empty body is accepted by the decoder and then refused for its zero length.
	if rr := sftpSelfDo(t, h, http.MethodPost, "/me/sftp/password", ""); rr.Code != http.StatusBadRequest ||
		sftpProblemID(t, rr) != "server.sftp.password_length_invalid" {
		t.Fatalf("empty password body: status %d body %s", rr.Code, rr.Body.String())
	}

	// With no user in the context every route answers 401.
	anon := chi.NewRouter()
	mountSFTPRoutes(anon, env.sessions, env.store)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/me/sftp"},
		{http.MethodPost, "/me/sftp/keys"},
		{http.MethodDelete, "/me/sftp/keys/1"},
		{http.MethodPost, "/me/sftp/password"},
		{http.MethodDelete, "/me/sftp/password"},
	} {
		rr := sftpSelfDo(t, anon, rt.method, rt.path, "")
		if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.common.unauthenticated" {
			t.Errorf("%s %s anonymous: status %d body %s", rt.method, rt.path, rr.Code, rr.Body.String())
		}
	}

	// Every route refuses an SFTP-session caller.
	blocked := sftpSelfRouter(env.sessions, env.store, uid, true)
	for _, rt := range []struct{ method, path string }{
		{http.MethodPost, "/me/sftp/keys"},
		{http.MethodDelete, "/me/sftp/keys/1"},
		{http.MethodPost, "/me/sftp/password"},
		{http.MethodDelete, "/me/sftp/password"},
	} {
		if rr := sftpSelfDo(t, blocked, rt.method, rt.path, ""); rr.Code != http.StatusForbidden {
			t.Errorf("%s %s as sftp caller: status %d", rt.method, rt.path, rr.Code)
		}
	}
}

func TestSFTPSelfUnit_StoreFailuresAreServerErrors(t *testing.T) {
	deadStore := newTestStore(t)
	sessions := auth.NewSessionStore(deadStore)
	if err := deadStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	h := sftpSelfRouter(sessions, deadStore, 1, false)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/me/sftp"},
		{http.MethodDelete, "/me/sftp/password"},
		{http.MethodDelete, "/me/sftp/keys/5"},
	} {
		if rr := sftpSelfDo(t, h, rt.method, rt.path, ""); rr.Code < 500 {
			t.Errorf("%s %s on a dead store: status %d, want 5xx", rt.method, rt.path, rr.Code)
		}
	}
}

func TestSFTPHelpersUnit(t *testing.T) {
	base, target, has := splitSFTPUsername("alice+ns/srv+extra")
	if base != "alice+ns/srv" || target != "extra" || !has {
		t.Fatalf("split = %q %q %v", base, target, has)
	}
	if base, _, has := splitSFTPUsername("alice"); base != "alice" || has {
		t.Fatalf("split without target = %q %v", base, has)
	}
	if got, ok := parseSFTPTarget("srv"); !ok || got.Namespace != scope.DefaultNamespace || got.Server != "srv" {
		t.Fatalf("parse bare target = %+v %v", got, ok)
	}
	if _, ok := parseSFTPTarget("Bad_Name"); ok {
		t.Fatal("parse accepted an invalid server name")
	}
	if got := truncateRunes("héllo", 2); got != "hé" {
		t.Fatalf("truncateRunes = %q", got)
	}
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Fatalf("truncateRunes short = %q", got)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil)
	req.RemoteAddr = "192.0.2.9:5555"
	cases := []struct{ remote, want string }{
		{"203.0.113.4:2222", "203.0.113.4"},
		{"203.0.113.5", "203.0.113.5"},
		{"[2001:db8::1]:22", "2001:db8::1"},
		{"garbage", "192.0.2.9"},
		{"", "192.0.2.9"},
	}
	for _, tc := range cases {
		if got := sftpSourceKey(tc.remote, req); got != tc.want {
			t.Errorf("sftpSourceKey(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
	req.RemoteAddr = "no-port-here"
	if got := sftpSourceKey("", req); got != "no-port-here" {
		t.Errorf("sftpSourceKey without a port = %q", got)
	}
}
