package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// MountClusterActions wires the credential-minting cluster operations
// (Add node, Download kubeconfig). They are admin-only (api/internal/rbac)
// and audited (the audit middleware logs every POST with the actor), and
// gated behind an explicit opt-in: when enabled is false they return 501,
// and the chart grants the underlying kube-system RBAC only when the
// operator turns clusterOps on. Safe-by-default off.
//
// externalAddr, when set (chart value clusterOps.externalAddress / flag
// --cluster-external-address), overrides the in-cluster API server address
// used in the join command and the downloaded kubeconfig's "server:" field.
// Left empty, both fall back to the in-cluster kube client's configured
// Host — typically the apiserver's ClusterIP — which is unreachable from
// outside the cluster, so operators fronting the apiserver with a
// node-routable address or a LoadBalancer must set this for those two
// responses to actually work off-cluster.
func MountClusterActions(r chi.Router, k *kube.Client, enabled bool, externalAddr string) {
	h := &clusterActions{k: k, enabled: enabled, externalAddr: strings.TrimSpace(externalAddr)}
	r.Post("/cluster/nodes:join", h.addNode)
	r.Post("/cluster/kubeconfig", h.kubeconfig)
}

type clusterActions struct {
	k            *kube.Client
	enabled      bool
	externalAddr string
}

// apiServerHost returns the scheme+host[:port] external clients (a joining
// node, a downloaded kubeconfig) should use to reach the API server: the
// configured external address when set, otherwise the in-cluster Host the
// API server itself was reached at.
func (h *clusterActions) apiServerHost() string {
	if h.externalAddr == "" {
		return h.k.Config.Host
	}
	if strings.Contains(h.externalAddr, "://") {
		return h.externalAddr
	}
	return "https://" + h.externalAddr
}

func (h *clusterActions) notEnabled(w http.ResponseWriter, req *http.Request) bool {
	if h.enabled {
		return false
	}
	httperr.WriteCode(w, req, http.StatusNotImplemented,
		errors.New("cluster operations are not enabled (set clusterOps.enabled)"))
	return true
}

// ---- Add node ----------------------------------------------------------

type joinResponse struct {
	Command    string `json:"command"`
	Token      string `json:"token"`
	CACertHash string `json:"caCertHash"`
	Endpoint   string `json:"endpoint"`
	ExpiresAt  string `json:"expiresAt"`
}

func (h *clusterActions) addNode(w http.ResponseWriter, req *http.Request) {
	if rejectRemoteCluster(w, req) {
		return
	}
	if h.notEnabled(w, req) {
		return
	}
	id, secret, err := genBootstrapToken()
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	expires := time.Now().Add(24 * time.Hour).UTC()
	// kube bootstrap token Secret — kubeadm and the kubelet read these to
	// authenticate a joining node, then it expires.
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-token-" + id, Namespace: "kube-system"},
		Type:       corev1.SecretType("bootstrap.kubernetes.io/token"),
		StringData: map[string]string{
			"token-id":                       id,
			"token-secret":                   secret,
			"expiration":                     expires.Format(time.RFC3339),
			"usage-bootstrap-authentication": "true",
			"usage-bootstrap-signing":        "true",
			"description":                    "Gameplane dashboard node-join token",
		},
	}
	if _, err := h.k.Typed.CoreV1().Secrets("kube-system").Create(req.Context(), sec, metav1.CreateOptions{}); err != nil {
		httperr.Write(w, req, err)
		return
	}
	hash, err := caCertHash(h.k.Config)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	endpoint := strings.TrimPrefix(strings.TrimPrefix(h.apiServerHost(), "https://"), "http://")
	writeJSON(w, joinResponse{
		Command: fmt.Sprintf("kubeadm join %s --token %s.%s --discovery-token-ca-cert-hash %s",
			endpoint, id, secret, hash),
		Token:      id + "." + secret,
		CACertHash: hash,
		Endpoint:   endpoint,
		ExpiresAt:  expires.Format(time.RFC3339),
	})
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// genBootstrapToken returns a 6-char id and 16-char secret from the
// bootstrap-token alphabet ([a-z0-9]).
func genBootstrapToken() (id, secret string, err error) {
	id, err = randToken(6)
	if err != nil {
		return "", "", err
	}
	secret, err = randToken(16)
	if err != nil {
		return "", "", err
	}
	return id, secret, nil
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = tokenAlphabet[int(b[i])%len(tokenAlphabet)]
	}
	return string(b), nil
}

// caCertHash returns the "sha256:<hex>" hash of the cluster CA's public
// key (the format kubeadm's --discovery-token-ca-cert-hash expects).
func caCertHash(cfg *rest.Config) (string, error) {
	pemBytes := cfg.CAData
	if len(pemBytes) == 0 && cfg.CAFile != "" {
		b, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return "", fmt.Errorf("read CA file: %w", err)
		}
		pemBytes = b
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return "", errors.New("no CA certificate available")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse CA cert: %w", err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ---- Download kubeconfig ----------------------------------------------

func (h *clusterActions) kubeconfig(w http.ResponseWriter, req *http.Request) {
	if rejectRemoteCluster(w, req) {
		return
	}
	if h.notEnabled(w, req) {
		return
	}
	u := auth.UserFromContext(req.Context())
	username := "gameplane-admin"
	if u != nil && u.Username != "" {
		username = u.Username
	}

	// Generate a client key + CSR for the user, in a group the chart binds
	// to read access. The signed cert carries the user's identity and a
	// short TTL so the kubeconfig auto-expires.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "gameplane:" + username, Organization: []string{"gameplane:dashboard"}},
	}, key)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	certPEM, err := h.signClientCert(req.Context(), username, csrPEM)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	caPEM := h.k.Config.CAData
	if len(caPEM) == 0 && h.k.Config.CAFile != "" {
		caPEM, _ = os.ReadFile(h.k.Config.CAFile)
	}
	kubeconfig := renderKubeconfig(h.apiServerHost(), caPEM, certPEM, keyPEM, username)

	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="gameplane-kubeconfig.yaml"`)
	_, _ = w.Write([]byte(kubeconfig))
}

// signClientCert creates a CertificateSigningRequest for the kube-apiserver
// client signer, approves it, and waits briefly for the issued cert.
func (h *clusterActions) signClientCert(ctx context.Context, username string, csrPEM []byte) ([]byte, error) {
	name := "gameplane-" + username + "-" + time.Now().UTC().Format("20060102150405")
	expSeconds := int32(3600) // 1h short-lived
	csr := &certv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: certv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        "kubernetes.io/kube-apiserver-client",
			ExpirationSeconds: &expSeconds,
			Usages:            []certv1.KeyUsage{certv1.UsageClientAuth, certv1.UsageDigitalSignature},
		},
	}
	csrs := h.k.Typed.CertificatesV1().CertificateSigningRequests()
	created, err := csrs.Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}
	// Best-effort cleanup of the CSR object once we're done.
	defer func() { _ = csrs.Delete(ctx, name, metav1.DeleteOptions{}) }()

	created.Status.Conditions = append(created.Status.Conditions, certv1.CertificateSigningRequestCondition{
		Type:    certv1.CertificateApproved,
		Status:  corev1.ConditionTrue,
		Reason:  "GameplaneDashboard",
		Message: "approved by the Gameplane API for a dashboard kubeconfig",
	})
	if _, err := csrs.UpdateApproval(ctx, name, created, metav1.UpdateOptions{}); err != nil {
		return nil, fmt.Errorf("approve CSR: %w", err)
	}

	// Poll for the signer to populate status.certificate.
	deadline := time.Now().Add(15 * time.Second)
	for {
		got, err := csrs.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if len(got.Status.Certificate) > 0 {
			return got.Status.Certificate, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the signed certificate")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// renderKubeconfig assembles a minimal client-cert kubeconfig.
func renderKubeconfig(server string, caPEM, certPEM, keyPEM []byte, username string) string {
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: gameplane
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: gameplane
  context:
    cluster: gameplane
    user: %s
current-context: gameplane
users:
- name: %s
  user:
    client-certificate-data: %s
    client-key-data: %s
`, server, b64(caPEM), username, username, b64(certPEM), b64(keyPEM))
}
