package verify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// genCert creates an ECDSA certificate for use as a synthetic Fulcio root,
// intermediate, or leaf. When parent/parentKey are nil the certificate is
// self-signed; otherwise it is signed by parent using parentKey. No network,
// no fixtures on disk.
func genCert(t *testing.T, cn string, isCA bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	tmpl := &x509.Certificate{}
	tmpl.SerialNumber = serial
	tmpl.Subject = pkix.Name{CommonName: cn}
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	tmpl.BasicConstraintsValid = true
	tmpl.IsCA = isCA
	signerCert := tmpl
	signerKey := key
	if parent != nil {
		signerCert = parent
		signerKey = parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create certificate %s: %v", cn, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %s: %v", cn, err)
	}
	return cert, key
}

// stubCA is a root.CertificateAuthority implementation that is deliberately
// NOT *root.FulcioCertificateAuthority, to exercise poolsFromCAs' type-switch
// skip path.
type stubCA struct{}

func (stubCA) Verify(*x509.Certificate, time.Time) ([][]*x509.Certificate, error) {
	return nil, nil
}

var _ root.CertificateAuthority = stubCA{}

func TestPoolsFromCAs_RootAndIntermediatesLandInCorrectPools(t *testing.T) {
	rootCert, rootKey := genCert(t, "fulcio-root", true, nil, nil)
	inter1, inter1Key := genCert(t, "fulcio-intermediate-1", true, rootCert, rootKey)
	inter2, _ := genCert(t, "fulcio-intermediate-2", true, rootCert, rootKey)

	fca := &root.FulcioCertificateAuthority{
		Root:          rootCert,
		Intermediates: []*x509.Certificate{inter1, inter2},
	}

	roots, intermediates, err := poolsFromCAs([]root.CertificateAuthority{fca})
	if err != nil {
		t.Fatalf("poolsFromCAs: unexpected error: %v", err)
	}

	// The root pool must contain rootCert: a leaf directly signed by the root
	// key, verified against the root pool alone, must succeed.
	leafViaRoot, _ := genCert(t, "leaf-via-root", false, rootCert, rootKey)
	if _, err := leafViaRoot.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: time.Now(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}); err != nil {
		t.Errorf("expected root pool to contain the Fulcio root, verify failed: %v", err)
	}

	// The intermediate pool must contain inter1: a leaf signed by inter1,
	// chaining root -> inter1 -> leaf, must verify only when both pools are
	// supplied together.
	leafViaInter1, _ := genCert(t, "leaf-via-inter1", false, inter1, inter1Key)
	if _, err := leafViaInter1.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   time.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}); err != nil {
		t.Errorf("expected intermediate pool to contain inter1, verify failed: %v", err)
	}
	// Without the intermediate pool the same leaf must NOT verify, proving
	// the chain genuinely depends on inter1 being present in that pool
	// rather than on some other path.
	if _, err := leafViaInter1.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: time.Now(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}); err == nil {
		t.Error("expected verify to fail without the intermediate pool, but it succeeded")
	}
}

func TestPoolsFromCAs_NonFulcioCASkippedWithoutCorruptingPools(t *testing.T) {
	rootCert, rootKey := genCert(t, "fulcio-root-2", true, nil, nil)
	fca := &root.FulcioCertificateAuthority{Root: rootCert}

	cas := []root.CertificateAuthority{stubCA{}, fca, stubCA{}}

	roots, _, err := poolsFromCAs(cas)
	if err != nil {
		t.Fatalf("poolsFromCAs: unexpected error with a mixed-in non-Fulcio CA: %v", err)
	}

	leaf, _ := genCert(t, "leaf-via-root-2", false, rootCert, rootKey)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: time.Now(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}); err != nil {
		t.Errorf("expected the valid Fulcio CA's root to still land despite the stub CA being mixed in: %v", err)
	}
}

func TestPoolsFromCAs_NilSliceErrors(t *testing.T) {
	roots, intermediates, err := poolsFromCAs(nil)
	if err == nil {
		t.Fatal("poolsFromCAs(nil): expected an error, got nil")
	}
	if roots != nil || intermediates != nil {
		t.Errorf("poolsFromCAs(nil): expected nil pools alongside the error, got roots=%v intermediates=%v", roots, intermediates)
	}
}

func TestPoolsFromCAs_NilRootWithIntermediatesOnlyErrors(t *testing.T) {
	rootCert, rootKey := genCert(t, "fulcio-root-3", true, nil, nil)
	inter, _ := genCert(t, "fulcio-intermediate-3", true, rootCert, rootKey)

	fca := &root.FulcioCertificateAuthority{
		Root:          nil,
		Intermediates: []*x509.Certificate{inter},
	}

	_, _, err := poolsFromCAs([]root.CertificateAuthority{fca})
	if err == nil {
		t.Fatal("poolsFromCAs: expected an error when the only CA has a nil Root, got nil")
	}
}

func TestTufRootWarning_UnsetReturnsFalse(t *testing.T) {
	t.Setenv(tufRootEnv, "")
	msg, warn := tufRootWarning()
	if warn {
		t.Errorf("tufRootWarning: expected false with TUF_ROOT unset, got true (msg=%q)", msg)
	}
	if msg != "" {
		t.Errorf("tufRootWarning: expected empty message with TUF_ROOT unset, got %q", msg)
	}
}

func TestTufRootWarning_SetReturnsExplicitWarning(t *testing.T) {
	t.Setenv(tufRootEnv, "/etc/sigstore/private-tuf")
	msg, warn := tufRootWarning()
	if !warn {
		t.Fatal("tufRootWarning: expected true with TUF_ROOT set, got false")
	}
	if !strings.Contains(msg, tufRootEnv) {
		t.Errorf("tufRootWarning message %q does not name the variable %q", msg, tufRootEnv)
	}
	if !strings.Contains(msg, "/etc/sigstore/private-tuf") {
		t.Errorf("tufRootWarning message %q does not include the configured value", msg)
	}
	if !strings.Contains(strings.ToUpper(msg), "PUBLIC-GOOD") {
		t.Errorf("tufRootWarning message %q does not make the public-good substitution explicit", msg)
	}
}
