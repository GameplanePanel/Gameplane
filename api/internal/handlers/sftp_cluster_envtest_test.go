//go:build envtest

package handlers

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// sftpClusterEnvtestRouter mounts the cluster routes on the envtest apiserver: Cluster registrations and the
// host key Secret live there, the store is a fresh SQLite database.
func sftpClusterEnvtestRouter(t *testing.T, hostKeySecret string) (http.Handler, *auth.SessionStore) {
	t.Helper()
	sessions := auth.NewSessionStore(newTestStore(t))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, kubeC)
	r := chi.NewRouter()
	MountClustersWithSFTP(r, reg, kubeC, scope.DefaultNamespace, SFTPClusterMount{
		Sessions:      sessions,
		Kube:          kubeC,
		PublicAddress: "files.example.com:2222",
		HostKeySecret: hostKeySecret,
		Namespace:     scope.DefaultNamespace,
	})
	return r, sessions
}

// sftpClusterEnvtestHostKey stores a fresh ed25519 host key in a Secret and returns the fingerprint it must show.
func sftpClusterEnvtestHostKey(t *testing.T, name string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kubeC.Secrets(scope.DefaultNamespace).Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: scope.DefaultNamespace},
		Data:       map[string][]byte{sftpHostKeyDataKey: pemData},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create host key secret: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Secrets(scope.DefaultNamespace).Delete(t.Context(), name, metav1.DeleteOptions{})
	})
	return ssh.FingerprintSHA256(pub)
}

// sftpClusterEnvtestRegister creates a Cluster registration and removes it when the test ends.
func sftpClusterEnvtestRegister(t *testing.T, prefix string) string {
	t.Helper()
	name := uniqueResourceName(prefix)
	if _, err := kubeC.Clusters().Create(t.Context(), newCluster(name, map[string]any{"kubeconfigSecret": map[string]any{"name": name + "-kubeconfig"}}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatalf("register cluster %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = kubeC.Clusters().Delete(t.Context(), name, metav1.DeleteOptions{})
	})
	return name
}

func sftpClusterEnvtestDo(t *testing.T, h http.Handler, method, path, body string, sftpCaller bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/problem+json")
	ctx := auth.WithUser(req.Context(), &auth.User{ID: 1, Username: "admin", Role: "admin"})
	if sftpCaller {
		ctx = auth.WithSFTPCaller(ctx, auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 9})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

// The panel's own endpoint reads its fingerprint from the host key Secret on the apiserver.
func TestSFTPClusterEnvtest_LocalFingerprintFromSecret(t *testing.T) {
	secret := uniqueResourceName("sftp-host-key")
	want := sftpClusterEnvtestHostKey(t, secret)
	h, _ := sftpClusterEnvtestRouter(t, secret)

	view := sftpClusterTestDecode[sftpClusterView](t, sftpClusterEnvtestDo(t, h, http.MethodGet, "/clusters/"+scope.DefaultCluster+"/sftp", "", false))
	if !view.Local || view.Address == nil || *view.Address != "files.example.com:2222" {
		t.Fatalf("local view: %+v", view)
	}
	if view.HostKeyFingerprint == nil || *view.HostKeyFingerprint != want {
		t.Fatalf("fingerprint = %v, want %q", view.HostKeyFingerprint, want)
	}
}

// A remote cluster's endpoint and tokens are its own: the token of one cluster is never listed for another,
// and an SFTP session token cannot reach these routes.
func TestSFTPClusterEnvtest_RemoteTokensStayPerCluster(t *testing.T) {
	h, _ := sftpClusterEnvtestRouter(t, uniqueResourceName("sftp-host-key"))
	remoteA := sftpClusterEnvtestRegister(t, "sftp-remote-a")
	remoteB := sftpClusterEnvtestRegister(t, "sftp-remote-b")

	rr := sftpClusterEnvtestDo(t, h, http.MethodPost, "/clusters/"+remoteA+"/sftp-token", "", false)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create token on %s: status %d body %s", remoteA, rr.Code, rr.Body.String())
	}
	created := sftpClusterTestDecode[struct {
		ID int64 `json:"id"`
	}](t, rr)

	fp := "SHA256:" + strings.Repeat("C", 43)
	rr = sftpClusterEnvtestDo(t, h, http.MethodPut, "/clusters/"+remoteA+"/sftp",
		`{"address":"sftp.`+remoteA+`.example.com:2222","hostKeyFingerprint":"`+fp+`"}`, false)
	if rr.Code != http.StatusOK {
		t.Fatalf("put endpoint: status %d body %s", rr.Code, rr.Body.String())
	}

	viewA := sftpClusterTestDecode[sftpClusterView](t, sftpClusterEnvtestDo(t, h, http.MethodGet, "/clusters/"+remoteA+"/sftp", "", false))
	if viewA.Address == nil || viewA.HostKeyFingerprint == nil || *viewA.HostKeyFingerprint != fp || len(viewA.Tokens) != 1 {
		t.Fatalf("%s view: %+v", remoteA, viewA)
	}
	viewB := sftpClusterTestDecode[sftpClusterView](t, sftpClusterEnvtestDo(t, h, http.MethodGet, "/clusters/"+remoteB+"/sftp", "", false))
	if viewB.Address != nil || len(viewB.Tokens) != 0 {
		t.Fatalf("%s must not see %s's endpoint or tokens: %+v", remoteB, remoteA, viewB)
	}

	// Revoking the token through the other cluster finds nothing.
	rr = sftpClusterEnvtestDo(t, h, http.MethodDelete, "/clusters/"+remoteB+"/sftp-token/"+idPath(created.ID), "", false)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-cluster revoke: status %d body %s", rr.Code, rr.Body.String())
	}

	// An SFTP session token is refused on every cluster route.
	rr = sftpClusterEnvtestDo(t, h, http.MethodGet, "/clusters/"+remoteA+"/sftp", "", true)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("sftp session on cluster route: status %d body %s", rr.Code, rr.Body.String())
	}
}
