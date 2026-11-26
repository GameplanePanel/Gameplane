package verify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
)

func testPubPEM(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// The keyed path's transparency-log policy is a security control with no
// visible effect until a signature is actually rejected, so assert the
// CheckOpts it produces directly.
func TestNewKeyed_TransparencyLogPolicy(t *testing.T) {
	t.Run("default verifies offline and ignores the log", func(t *testing.T) {
		v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, false, false)
		if err != nil {
			t.Fatalf("newKeyed: %v", err)
		}
		co := v.(*cosignVerifier).mkOpts(context.Background())
		if !co.IgnoreTlog {
			t.Error("IgnoreTlog = false, want true: a keyed signature is complete without a log entry, and requiring one would break air-gapped clusters")
		}
		if co.RekorPubKeys != nil {
			t.Error("RekorPubKeys set, want nil when the log is not required")
		}
		if !co.Offline {
			t.Error("Offline = false, want true")
		}
		if !co.IgnoreSCT {
			t.Error("IgnoreSCT = false, want true: a keyed signature carries no Fulcio certificate")
		}
	})

	t.Run("requireTransparencyLog enforces log inclusion", func(t *testing.T) {
		// Loading the Rekor keys reaches the Sigstore TUF root, so this can
		// only run on a networked machine. Skipping keeps an offline `go test`
		// honest instead of silently asserting nothing.
		v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, false, true)
		if err != nil {
			t.Skipf("rekor public keys unavailable (offline?): %v", err)
		}
		co := v.(*cosignVerifier).mkOpts(context.Background())
		if co.IgnoreTlog {
			t.Error("IgnoreTlog = true, want false when requireTransparencyLog is set")
		}
		if co.RekorPubKeys == nil {
			t.Error("RekorPubKeys = nil, want the loaded keys so inclusion proofs can be checked")
		}
		if !co.Offline {
			t.Error("Offline = false, want true: the bundled inclusion proof is checked without a live log query")
		}
	})
}

func TestCosignVerifier_Verify(t *testing.T) {
	v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, true, false)
	if err != nil {
		t.Fatalf("newKeyed: %v", err)
	}

	t.Run("malformed digest is a parse error", func(t *testing.T) {
		if err := v.Verify(context.Background(), "registry.example/x", "not-a-digest"); err == nil {
			t.Fatal("expected a parse error for a malformed digest")
		}
	})

	t.Run("unreachable registry surfaces a verify error", func(t *testing.T) {
		// 127.0.0.1:1 is always closed → cosign's signature fetch fails
		// fast. This exercises mkOpts/baseCheckOpts and the verify-error
		// branch without reaching a real registry.
		digest := "sha256:" + strings.Repeat("a", 64)
		if err := v.Verify(context.Background(), "127.0.0.1:1/x", digest); err == nil {
			t.Fatal("expected a verify error against a closed registry")
		}
	})
}
