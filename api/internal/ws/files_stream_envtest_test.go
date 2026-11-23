package ws

// Stream, rename and stat file routes through the real rbac middleware and the real direct
// agent transport. The agent is a local mTLS server; the cluster is a fake dynamic client, so
// this test needs no envtest cluster and runs with the normal unit suite.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// filesAgentServer is a local mTLS stand-in for the agent. It records every operation it
// completely received, as "METHOD /path".
type filesAgentServer struct {
	addr string
	mu   sync.Mutex
	seen []string
}

func (s *filesAgentServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.ReadAll(r.Body); err != nil {
		// A truncated body is never recorded as a completed operation.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.seen = append(s.seen, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *filesAgentServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

func (s *filesAgentServer) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		return ""
	}
	return s.seen[len(s.seen)-1]
}

// newFilesAgentServer starts the mTLS agent. One self-signed ECDSA certificate (IP SAN
// 127.0.0.1, usable for server and client auth) is the trust anchor for both directions. It
// writes the CA, client certificate and client key the API's mTLS loader expects.
func newFilesAgentServer(t *testing.T) (*filesAgentServer, string, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "files-agent-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	for path, data := range map[string][]byte{caFile: certPEM, certFile: certPEM, keyFile: keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)

	agent := &filesAgentServer{}
	srv := httptest.NewUnstartedServer(agent)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	agent.addr = srv.Listener.Addr().String()
	return agent, caFile, certFile, keyFile
}

// filesPerms grants perms on the test namespace of the home cluster, as a role binding would.
func filesPerms(perms ...string) map[string]map[string]map[string]struct{} {
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return map[string]map[string]map[string]struct{}{scope.DefaultCluster: {scope.DefaultNamespace: set}}
}

// newFilesRouter mounts the file routes behind the real rbac middleware. The running GameServer
// is owned by user 42 and shared with user 43.
func newFilesRouter(t *testing.T, agentAddr, caFile, certFile, keyFile string, maxUpload int64) http.Handler {
	t.Helper()
	gs := filesTestServer(map[string]any{
		"gameplane.local/owner-id":      "42",
		"gameplane.local/collaborators": "43",
	}, false)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.GVRs["servers"]: "GameServerList"}, gs)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, &kube.Client{Dynamic: dyn, Typed: k8sfake.NewSimpleClientset()})
	files := rbac.NewFileAccess(reg, nil)

	r := chi.NewRouter()
	r.Use(rbac.MiddlewareWithFiles(reg, files))
	Mount(r, reg, caFile, certFile, keyFile, AgentGatewayOptions{
		Files:          files,
		MaxUploadBytes: maxUpload,
		AgentHost:      func(_, _ string) string { return agentAddr },
	})
	return r
}

func doFilesRequest(t *testing.T, h http.Handler, user *auth.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFileRoutesAuthorizeByRoleThroughAgentTransport(t *testing.T) {
	agent, caFile, certFile, keyFile := newFilesAgentServer(t)
	h := newFilesRouter(t, agent.addr, caFile, certFile, keyFile, 1<<20)

	viewer := &auth.User{ID: 10, Perms: filesPerms("servers:read")}
	operator := &auth.User{ID: 11, Perms: filesPerms("servers:read", "servers:write")}
	owner := &auth.User{ID: 42}
	collaborator := &auth.User{ID: 43}
	stranger := &auth.User{ID: 99}

	const (
		statURL   = "/servers/alpha/files/stat?path=/"
		streamURL = "/servers/alpha/files/stream?path=/a.bin&offset=0"
		renameURL = "/servers/alpha/files/rename"
		renameBod = `{"from":"/a","to":"/b"}`
		agentStat = "/v1/targets/" + filesTestUID + "/files/stat"
		agentStrm = "/v1/targets/" + filesTestUID + "/files/stream"
		agentRnm  = "/v1/targets/" + filesTestUID + "/files/rename"
	)
	cases := []struct {
		name, method, target, body string
		user                       *auth.User
		want                       int
		agentOp                    string
	}{
		{"viewer stat", http.MethodGet, statURL, "", viewer, http.StatusOK, "GET " + agentStat},
		{"viewer stream denied", http.MethodPut, streamURL, "data", viewer, http.StatusForbidden, ""},
		{"viewer rename denied", http.MethodPost, renameURL, renameBod, viewer, http.StatusForbidden, ""},
		{"operator stream", http.MethodPut, streamURL, "data", operator, http.StatusOK, "PUT " + agentStrm},
		{"operator rename", http.MethodPost, renameURL, renameBod, operator, http.StatusOK, "POST " + agentRnm},
		{"owner stat", http.MethodGet, statURL, "", owner, http.StatusOK, "GET " + agentStat},
		{"owner stream", http.MethodPut, streamURL, "data", owner, http.StatusOK, "PUT " + agentStrm},
		{"collaborator rename", http.MethodPost, renameURL, renameBod, collaborator, http.StatusOK, "POST " + agentRnm},
		{"stranger stat denied", http.MethodGet, statURL, "", stranger, http.StatusForbidden, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := agent.count()
			rec := doFilesRequest(t, h, tc.user, tc.method, tc.target, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.agentOp == "" {
				if agent.count() != before {
					t.Fatalf("denied request reached the agent: %s", agent.last())
				}
				return
			}
			if agent.count() != before+1 || agent.last() != tc.agentOp {
				t.Fatalf("agent saw %q (count %d -> %d), want %q", agent.last(), before, agent.count(), tc.agentOp)
			}
		})
	}
}

func TestFileStreamOverUploadLimitNeverReachesAgent(t *testing.T) {
	agent, caFile, certFile, keyFile := newFilesAgentServer(t)
	const limit = 16
	h := newFilesRouter(t, agent.addr, caFile, certFile, keyFile, limit)
	operator := &auth.User{ID: 11, Perms: filesPerms("servers:read", "servers:write")}

	before := agent.count()
	rec := doFilesRequest(t, h, operator, http.MethodPut,
		"/servers/alpha/files/stream?path=/big.bin&offset=0", strings.Repeat("x", limit*4))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-limit upload status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	if agent.count() != before {
		t.Fatalf("over-limit upload completed at the agent: %s", agent.last())
	}
	// A within-limit upload still completes, so the cap is the only difference.
	rec = doFilesRequest(t, h, operator, http.MethodPut,
		"/servers/alpha/files/stream?path=/small.bin&offset=0", strings.Repeat("x", limit))
	if rec.Code != http.StatusOK {
		t.Fatalf("within-limit upload status = %d: %s", rec.Code, rec.Body.String())
	}
}
