package handlers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

var sftpClusterTestFP = "SHA256:" + strings.Repeat("A", 43)

// sftpClusterTestEnv mounts the SFTP cluster routes with a store, two registered remote clusters
// ("remote-a", "remote-b") and the panel's own cluster.
type sftpClusterTestEnv struct {
	sessions *auth.SessionStore
	h        http.Handler
	known    map[string]bool
}

func newSFTPClusterTestEnv(t *testing.T, localAddress string) sftpClusterTestEnv {
	t.Helper()
	store := newTestStore(t)
	env := sftpClusterTestEnv{
		sessions: auth.NewSessionStore(store),
		known:    map[string]bool{"remote-a": true, "remote-b": true},
	}
	known := env.known
	deps := sftpClusterDeps{
		sessions:       env.sessions,
		defaultCluster: scope.DefaultCluster,
		localAddress:   localAddress,
		exists: func(_ context.Context, name string) (bool, error) {
			return name == scope.DefaultCluster || known[name], nil
		},
		localFingerprint: func(context.Context) (string, error) { return sftpClusterTestFP, nil },
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 1, Username: "admin", Role: "admin"})))
		})
	})
	r.Route("/clusters", func(r chi.Router) { mountSFTPClusterRoutes(r, deps) })
	env.h = r
	return env
}

// do sends one request. sftpCaller marks it as made by an SFTP session token, which must be refused.
func (env sftpClusterTestEnv) do(t *testing.T, method, path, body string, sftpCaller bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/problem+json")
	if sftpCaller {
		req = req.WithContext(auth.WithSFTPCaller(req.Context(), auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 9}))
	}
	rr := httptest.NewRecorder()
	env.h.ServeHTTP(rr, req)
	return rr
}

func sftpClusterTestDecode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func TestSFTPClusterUnit_CreateListRevoke(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")

	rr := env.do(t, http.MethodPost, "/clusters/remote-a/sftp-token", "", false)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	created := sftpClusterTestDecode[struct {
		ID      int64  `json:"id"`
		Cluster string `json:"cluster"`
		Token   string `json:"token"`
	}](t, rr)
	if !strings.HasPrefix(created.Token, auth.SFTPServicePrefix) || created.Cluster != "remote-a" || created.ID == 0 {
		t.Fatalf("created token: %+v", created)
	}

	// The list shows the token's metadata and never its secret.
	rr = env.do(t, http.MethodGet, "/clusters/remote-a/sftp", "", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d body %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), created.Token) {
		t.Fatal("the drawer payload must not carry the token secret")
	}
	view := sftpClusterTestDecode[sftpClusterView](t, rr)
	if view.Local || len(view.Tokens) != 1 || view.Tokens[0].ID != created.ID || view.Address != nil {
		t.Fatalf("remote view: %+v", view)
	}

	rr = env.do(t, http.MethodDelete, "/clusters/remote-a/sftp-token/"+idPath(created.ID), "", false)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d body %s", rr.Code, rr.Body.String())
	}
	view = sftpClusterTestDecode[sftpClusterView](t, env.do(t, http.MethodGet, "/clusters/remote-a/sftp", "", false))
	if len(view.Tokens) != 0 {
		t.Fatalf("revoked token still listed: %+v", view.Tokens)
	}

	rr = env.do(t, http.MethodDelete, "/clusters/remote-a/sftp-token/"+idPath(created.ID), "", false)
	if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.sftp.token_not_found" {
		t.Fatalf("second revoke: status %d body %s", rr.Code, rr.Body.String())
	}
}

// idPath renders a token id for a path.
func idPath(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestSFTPClusterUnit_TokenLimit(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")
	for i := range 2 {
		if rr := env.do(t, http.MethodPost, "/clusters/remote-a/sftp-token", "", false); rr.Code != http.StatusCreated {
			t.Fatalf("token %d: status %d body %s", i, rr.Code, rr.Body.String())
		}
	}
	rr := env.do(t, http.MethodPost, "/clusters/remote-a/sftp-token", "", false)
	if rr.Code != http.StatusConflict || sftpProblemID(t, rr) != "server.sftp.token_limit_reached" {
		t.Fatalf("third token: status %d body %s", rr.Code, rr.Body.String())
	}
	// The limit is per cluster: remote-b still has room.
	if rr := env.do(t, http.MethodPost, "/clusters/remote-b/sftp-token", "", false); rr.Code != http.StatusCreated {
		t.Fatalf("other cluster: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPClusterUnit_UnknownCluster(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/clusters/nope/sftp", ""},
		{http.MethodPut, "/clusters/nope/sftp", `{"address":"a.example.com:2222","hostKeyFingerprint":"` + sftpClusterTestFP + `"}`},
		{http.MethodPost, "/clusters/nope/sftp-token", ""},
		{http.MethodDelete, "/clusters/nope/sftp-token/1", ""},
	}
	for _, tc := range cases {
		rr := env.do(t, tc.method, tc.path, tc.body, false)
		if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != httperr.IDNotFound {
			t.Fatalf("%s %s: status %d body %s", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}
}

func TestSFTPClusterUnit_RevokeIsScopedToCluster(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")
	created := sftpClusterTestDecode[struct {
		ID int64 `json:"id"`
	}](t, env.do(t, http.MethodPost, "/clusters/remote-a/sftp-token", "", false))

	// A token of remote-a is not found through remote-b, and stays active on remote-a.
	rr := env.do(t, http.MethodDelete, "/clusters/remote-b/sftp-token/"+idPath(created.ID), "", false)
	if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.sftp.token_not_found" {
		t.Fatalf("cross-cluster revoke: status %d body %s", rr.Code, rr.Body.String())
	}
	view := sftpClusterTestDecode[sftpClusterView](t, env.do(t, http.MethodGet, "/clusters/remote-a/sftp", "", false))
	if len(view.Tokens) != 1 {
		t.Fatalf("token should survive a cross-cluster revoke: %+v", view.Tokens)
	}
}

func TestSFTPClusterUnit_PutEndpointAndGet(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "files.example.com:2222")
	fp := sftpClusterTestFP
	body := `{"address":" sftp.remote-a.example.com:2222 ","hostKeyFingerprint":"` + fp + `"}`
	rr := env.do(t, http.MethodPut, "/clusters/remote-a/sftp", body, false)
	if rr.Code != http.StatusOK {
		t.Fatalf("put: status %d body %s", rr.Code, rr.Body.String())
	}
	ep := sftpClusterTestDecode[auth.SFTPClusterEndpoint](t, rr)
	if ep.Address != "sftp.remote-a.example.com:2222" || ep.HostKeyFingerprint != fp {
		t.Fatalf("stored endpoint: %+v", ep)
	}

	view := sftpClusterTestDecode[sftpClusterView](t, env.do(t, http.MethodGet, "/clusters/remote-a/sftp", "", false))
	if view.Address == nil || *view.Address != "sftp.remote-a.example.com:2222" ||
		view.HostKeyFingerprint == nil || *view.HostKeyFingerprint != fp {
		t.Fatalf("remote view: %+v", view)
	}

	// The panel's own cluster reports the chart address and the host key fingerprint, and is marked local.
	local := sftpClusterTestDecode[sftpClusterView](t, env.do(t, http.MethodGet, "/clusters/"+scope.DefaultCluster+"/sftp", "", false))
	if !local.Local || local.Address == nil || *local.Address != "files.example.com:2222" ||
		local.HostKeyFingerprint == nil || *local.HostKeyFingerprint != sftpClusterTestFP {
		t.Fatalf("local view: %+v", local)
	}
}

func TestSFTPClusterUnit_LocalWithoutAddressOrKey(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")
	local := sftpClusterTestDecode[sftpClusterView](t, env.do(t, http.MethodGet, "/clusters/"+scope.DefaultCluster+"/sftp", "", false))
	if local.Address != nil {
		t.Fatalf("unset publicAddress must read as null: %+v", local)
	}
	if local.Tokens == nil {
		t.Fatal("tokens must be an empty list, not null")
	}
}

func TestSFTPClusterUnit_PutEndpointValidation(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")
	fp := sftpClusterTestFP
	cases := []struct {
		name, body, want string
	}{
		{"empty address", `{"address":"","hostKeyFingerprint":"` + fp + `"}`, "server.sftp.endpoint_invalid"},
		{"path in address", `{"address":"http://files.example.com:2222","hostKeyFingerprint":"` + fp + `"}`, "server.sftp.endpoint_invalid"},
		{"port out of range", `{"address":"files.example.com:99999","hostKeyFingerprint":"` + fp + `"}`, "server.sftp.endpoint_invalid"},
		{"no host", `{"address":":2222","hostKeyFingerprint":"` + fp + `"}`, "server.sftp.endpoint_invalid"},
		{"md5 fingerprint", `{"address":"files.example.com:2222","hostKeyFingerprint":"MD5:aa:bb"}`, "server.sftp.fingerprint_invalid"},
		{"short fingerprint", `{"address":"files.example.com:2222","hostKeyFingerprint":"SHA256:abc"}`, "server.sftp.fingerprint_invalid"},
		{"invalid json", `{not json`, "server.common.invalid_json"},
	}
	for _, tc := range cases {
		rr := env.do(t, http.MethodPut, "/clusters/remote-a/sftp", tc.body, false)
		if rr.Code != http.StatusBadRequest || sftpProblemID(t, rr) != tc.want {
			t.Fatalf("%s: status %d body %s", tc.name, rr.Code, rr.Body.String())
		}
	}

	// The panel's own endpoint is set in chart values, never through this route.
	rr := env.do(t, http.MethodPut, "/clusters/"+scope.DefaultCluster+"/sftp", `{"address":"files.example.com:2222","hostKeyFingerprint":"`+fp+`"}`, false)
	if rr.Code != http.StatusBadRequest || sftpProblemID(t, rr) != "server.sftp.endpoint_local" {
		t.Fatalf("local put: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPClusterUnit_SFTPSessionAndMissingCallerRefused(t *testing.T) {
	env := newSFTPClusterTestEnv(t, "")
	paths := []struct{ method, path, body string }{
		{http.MethodGet, "/clusters/remote-a/sftp", ""},
		{http.MethodPut, "/clusters/remote-a/sftp", `{"address":"a.example.com:2222","hostKeyFingerprint":"` + sftpClusterTestFP + `"}`},
		{http.MethodPost, "/clusters/remote-a/sftp-token", ""},
		{http.MethodDelete, "/clusters/remote-a/sftp-token/1", ""},
	}
	for _, p := range paths {
		rr := env.do(t, p.method, p.path, p.body, true)
		if rr.Code != http.StatusForbidden || sftpProblemID(t, rr) != "server.forbidden" {
			t.Fatalf("%s %s from an SFTP session: status %d body %s", p.method, p.path, rr.Code, rr.Body.String())
		}
	}

	// Without a user in the context the route answers as unauthenticated.
	bare := chi.NewRouter()
	bare.Route("/clusters", func(r chi.Router) {
		mountSFTPClusterRoutes(r, sftpClusterDeps{sessions: env.sessions, defaultCluster: scope.DefaultCluster,
			exists: func(context.Context, string) (bool, error) { return true, nil }})
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/clusters/remote-a/sftp", nil)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	bare.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.common.unauthenticated" {
		t.Fatalf("no caller: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPClusterUnit_ValidSFTPAddress(t *testing.T) {
	ok := []string{"files.example.com", "files.example.com:2222", "203.0.113.7:30222", "[2001:db8::1]:2222", "[2001:db8::1]"}
	bad := []string{"", " ", "a b", "http://x", "x/y", "user@x", "x?y", ":2222", "x:0", "x:65536", "x:port", "2001:db8::1", "[::1]:x"}
	for _, a := range ok {
		if !validSFTPAddress(a) {
			t.Errorf("address %q should be valid", a)
		}
	}
	for _, a := range bad {
		if validSFTPAddress(a) {
			t.Errorf("address %q should be invalid", a)
		}
	}
	if validSFTPAddress(strings.Repeat("a", 256)) {
		t.Error("an address over 255 characters should be invalid")
	}
}

func TestSFTPClusterUnit_HostKeyFingerprint(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	want := ssh.FingerprintSHA256(pub)

	got, err := sftpHostKeyFingerprint(pemData)
	if err != nil || got != want {
		t.Fatalf("fingerprint = %q, %v; want %q", got, err, want)
	}
	if !sftpFingerprintPattern.MatchString(got) {
		t.Fatalf("fingerprint %q does not match the accepted format", got)
	}
	if _, err := sftpHostKeyFingerprint([]byte("not a key")); err == nil {
		t.Fatal("garbage host key must fail")
	}

	// A missing Secret or a missing cluster client reads as no fingerprint, not an error.
	if fp, err := localSFTPFingerprint(nil, "ns", "gameplane-sftp-host-key")(t.Context()); err != nil || fp != "" {
		t.Fatalf("nil client: %q, %v", fp, err)
	}
	if known, err := clusterExists("default", nil)(t.Context(), "remote-a"); err != nil || known {
		t.Fatalf("nil client, remote cluster: %v, %v", known, err)
	}
	if known, err := clusterExists("default", nil)(t.Context(), "default"); err != nil || !known {
		t.Fatalf("local cluster: %v, %v", known, err)
	}
}
