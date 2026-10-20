// Package controller contains the reconciliation logic for Gameplane CRDs.
package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// agentCertRenewalThreshold: re-issue a server cert if its remaining
// validity falls below this. One year of cert minus this threshold gives
// the operator a comfortable window to roll the cert before it expires
// in the wild.
const agentCertRenewalThreshold = 30 * 24 * time.Hour

// agentTLSSecretName returns the per-GameServer Secret holding the
// agent's server cert + key + CA bundle. The agent reads `tls.crt`,
// `tls.key`, and `ca.crt` from a single mount, so the controller stages
// all three keys in one Secret rather than mounting the cluster CA
// across namespaces (which Kubernetes doesn't allow).
func agentTLSSecretName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-agent-tls"
}

// reconcileAgentTLS ensures `<gs>-agent-tls` exists in the GameServer's
// namespace with a server cert signed by the cluster-wide agent CA. The
// chart provisions the CA + key under r.AgentCASecretName/Namespace via
// Helm's genCA. This function reads that Secret, signs a fresh server
// cert SAN'd for the agent pod's DNS names, and stages cert/key/CA into
// a per-server Secret owned by the GameServer (so it's GC'd on delete).
//
// Idempotent: a still-fresh existing cert (≥30 days remaining) is left
// alone, so this runs cheaply on every Reconcile.
func (r *GameServerReconciler) reconcileAgentTLS(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) error {
	if r.AgentCASecretName == "" || r.AgentCASecretNamespace == "" {
		return errors.New("agent CA Secret reference not configured (set --agent-ca-secret-name and --agent-ca-secret-namespace)")
	}

	name := agentTLSSecretName(gs)

	// Fetch the active CA first to compute its fingerprint.
	var caSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: r.AgentCASecretNamespace, Name: r.AgentCASecretName,
	}, &caSecret); err != nil {
		return fmt.Errorf("get agent CA Secret %s/%s: %w", r.AgentCASecretNamespace, r.AgentCASecretName, err)
	}
	caCertPEM, ok := caSecret.Data["ca.crt"]
	if !ok {
		return fmt.Errorf("agent CA Secret %s/%s missing ca.crt", r.AgentCASecretNamespace, r.AgentCASecretName)
	}
	caKeyPEM, ok := caSecret.Data["ca.key"]
	if !ok {
		return fmt.Errorf("agent CA Secret %s/%s missing ca.key", r.AgentCASecretNamespace, r.AgentCASecretName)
	}
	caCert, caKey, err := parseAgentCA(caCertPEM, caKeyPEM)
	if err != nil {
		return fmt.Errorf("parse agent CA: %w", err)
	}
	caFingerprint := caCertFingerprint(caCertPEM)

	var existing corev1.Secret
	getErr := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: name}, &existing)
	if getErr == nil {
		// Check cert freshness AND that it was signed by the current CA.
		// If the CA changed, reissue even if the existing cert is still valid.
		existingCAFingerprint := existing.Annotations["gameplane.local/ca-fingerprint"]
		if certValidFor(existing.Data["tls.crt"], agentCertRenewalThreshold, agentDNSNames(gs)) &&
			existingCAFingerprint == caFingerprint {
			return nil
		}
	} else if !apierrors.IsNotFound(getErr) {
		return fmt.Errorf("get agent TLS Secret %s/%s: %w", gs.Namespace, name, getErr)
	}

	certPEM, keyPEM, err := signAgentServerCert(caCert, caKey, gs)
	if err != nil {
		return fmt.Errorf("sign agent cert for %s: %w", gs.Name, err)
	}

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeTLS
		sec.Data = map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
			"ca.crt":                caCertPEM,
		}
		if sec.Annotations == nil {
			sec.Annotations = make(map[string]string)
		}
		sec.Annotations["gameplane.local/ca-fingerprint"] = caFingerprint
		return controllerutil.SetControllerReference(gs, sec, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upsert agent TLS Secret %s/%s: %w", gs.Namespace, name, err)
	}
	return nil
}

// certValidFor reports whether the PEM cert still has at least d of
// validity left AND carries every required SAN. The SAN check forces a
// re-issue when the expected DNS name set grows (e.g. the dedicated
// agent Service names) instead of waiting out the expiry window.
func certValidFor(certPEM []byte, d time.Duration, requiredSANs []string) bool {
	if len(certPEM) == 0 {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Until(cert.NotAfter) < d {
		return false
	}
	have := make(map[string]bool, len(cert.DNSNames))
	for _, n := range cert.DNSNames {
		have[n] = true
	}
	for _, want := range requiredSANs {
		if !have[want] {
			return false
		}
	}
	return true
}

func parseAgentCA(certPEM, keyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	cBlock, _ := pem.Decode(certPEM)
	if cBlock == nil {
		return nil, nil, errors.New("ca.crt: not PEM")
	}
	cert, err := x509.ParseCertificate(cBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ca.crt: %w", err)
	}
	kBlock, _ := pem.Decode(keyPEM)
	if kBlock == nil {
		return nil, nil, errors.New("ca.key: not PEM")
	}
	// Helm's genCA emits PKCS#1 RSA, but accept PKCS#8 too in case a
	// future chart variant switches.
	if k, err := x509.ParsePKCS1PrivateKey(kBlock.Bytes); err == nil {
		return cert, k, nil
	}
	if k, err := x509.ParsePKCS8PrivateKey(kBlock.Bytes); err == nil {
		rsaKey, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, errors.New("ca.key: PKCS#8 but not RSA")
		}
		return cert, rsaKey, nil
	}
	return nil, nil, errors.New("ca.key: unsupported PEM format")
}

// caCertFingerprint returns the SHA256 fingerprint of a PEM-encoded certificate.
// Used to detect CA changes and trigger agent cert reissuance.
func caCertFingerprint(certPEM []byte) string {
	h := sha256.Sum256(certPEM)
	return fmt.Sprintf("%x", h)
}

func signAgentServerCert(
	caCert *x509.Certificate, caKey *rsa.PrivateKey, gs *gameplanev1alpha1.GameServer,
) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: gs.Name + "-agent"},
		NotBefore:    now.Add(-1 * time.Hour),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     agentDNSNames(gs),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// agentDNSNames lists every DNS form the API or operator may use to
// dial this GameServer's agent. The canonical path is the dedicated
// `<gs>-agent` ClusterIP Service (api/internal/ws/dialer.go,
// operator/internal/agent/client.go); the pod-DNS and game-Service
// forms are kept so a cert mismatch surfaces only when something is
// genuinely wrong, not because the caller picked a different aliasing.
func agentDNSNames(gs *gameplanev1alpha1.GameServer) []string {
	pod := gs.Name + "-0"
	agentSvc := gs.Name + "-agent"
	return []string{
		pod,
		gs.Name,
		fmt.Sprintf("%s.%s", pod, gs.Name),
		fmt.Sprintf("%s.%s.%s", pod, gs.Name, gs.Namespace),
		fmt.Sprintf("%s.%s.%s.svc", pod, gs.Name, gs.Namespace),
		fmt.Sprintf("%s.%s.%s.svc.cluster.local", pod, gs.Name, gs.Namespace),
		fmt.Sprintf("%s.%s.svc", gs.Name, gs.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", gs.Name, gs.Namespace),
		agentSvc,
		fmt.Sprintf("%s.%s", agentSvc, gs.Namespace),
		fmt.Sprintf("%s.%s.svc", agentSvc, gs.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", agentSvc, gs.Namespace),
	}
}
