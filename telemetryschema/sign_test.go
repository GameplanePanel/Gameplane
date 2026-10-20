package telemetryschema

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// Fixed vector: secret = bytes 0x00..0x1f, ID = testID. These values were
// computed once with this package and pin the derivation (HKDF-SHA256,
// salt = installID, info = "gameplane-telemetry-signing-v1") so an accidental
// change to it, which would orphan every install's claim, fails a test.
const (
	vectorSeedHex = "ba8437e857f84d8a133ba2bdd61e9487835821b360b0ea80eaae8214510c34e4"
	vectorPublic  = testKey
	vectorFP      = "91acb03049b6b19322b826facc44ff4cb52f241170856c5ae17cf97d60525654"
	vectorBody    = "hello gameplane"
	vectorHeader  = "ed25519=52awxyQ-StRsfZ1vVDldOkSsGpSqV-UK49-DF3KJN9fJJb0UomHqlaTNeFpL1-EwT-8k4wEoq2MZGoEazHTFCw"
	otherID       = "9b2e7c14-5d3a-4f68-8e0b-1a4c6d8f2e37"
)

// vectorSecret returns the fixed secret 0x00..0x1f.
func vectorSecret() []byte {
	secret := make([]byte, SecretSize)
	for i := range secret {
		secret[i] = byte(i)
	}
	return secret
}

// vectorKey derives the fixed vector's private key.
func vectorKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	priv, err := DeriveKey(vectorSecret(), testID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	return priv
}

// TestDeriveKeyVector pins the derivation, the public key string, the
// fingerprint and a signature to fixed values.
func TestDeriveKeyVector(t *testing.T) {
	t.Parallel()
	priv := vectorKey(t)
	if got := hex.EncodeToString(priv.Seed()); got != vectorSeedHex {
		t.Fatalf("seed = %s, want %s", got, vectorSeedHex)
	}
	pub := PublicKeyString(priv)
	if pub != vectorPublic {
		t.Fatalf("PublicKeyString = %s, want %s", pub, vectorPublic)
	}
	if len(pub) != 43 || strings.ContainsAny(pub, "=+/") {
		t.Fatalf("public key %q is not unpadded base64url of 32 bytes", pub)
	}
	if got := KeyFingerprint(pub); got != vectorFP {
		t.Fatalf("KeyFingerprint = %s, want %s", got, vectorFP)
	}
	// Ed25519 signing is deterministic, so the header is a fixed value too.
	if got := Sign(priv, []byte(vectorBody)); got != vectorHeader {
		t.Fatalf("Sign = %s, want %s", got, vectorHeader)
	}
	if err := Verify(vectorHeader, vectorPublic, []byte(vectorBody)); err != nil {
		t.Fatalf("Verify(vector): %v", err)
	}
}

// TestDeriveKeyIsDeterministicAndPerID checks that the same inputs give the
// same key and that a different ID or secret gives a different key.
func TestDeriveKeyIsDeterministicAndPerID(t *testing.T) {
	t.Parallel()
	a := vectorKey(t)
	b := vectorKey(t)
	if !bytes.Equal(a, b) {
		t.Fatal("same secret and ID gave different keys")
	}
	other, err := DeriveKey(vectorSecret(), otherID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if bytes.Equal(a, other) || PublicKeyString(a) == PublicKeyString(other) {
		t.Fatal("different IDs derived the same key")
	}
	secret2 := vectorSecret()
	secret2[0] ^= 0xff
	diff, err := DeriveKey(secret2, testID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if bytes.Equal(a, diff) {
		t.Fatal("different secrets derived the same key")
	}
	if KeyFingerprint(PublicKeyString(a)) == KeyFingerprint(PublicKeyString(other)) {
		t.Fatal("different keys share a fingerprint")
	}
}

// TestDeriveKeyRejectsBadInput covers the input checks.
func TestDeriveKeyRejectsBadInput(t *testing.T) {
	t.Parallel()
	for name, secret := range map[string][]byte{
		"nil":   nil,
		"empty": {},
		"short": make([]byte, SecretSize-1),
		"long":  make([]byte, SecretSize+1),
	} {
		if _, err := DeriveKey(secret, testID); err == nil {
			t.Errorf("%s secret: expected error", name)
		}
	}
	if _, err := DeriveKey(vectorSecret(), ""); err == nil {
		t.Error("empty install ID: expected error")
	}
}

// TestNewSecret checks the length and that two secrets differ.
func TestNewSecret(t *testing.T) {
	t.Parallel()
	a, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	b, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	if len(a) != SecretSize || len(b) != SecretSize {
		t.Fatalf("lengths = %d, %d, want %d", len(a), len(b), SecretSize)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two secrets are identical")
	}
	if _, err := DeriveKey(a, testID); err != nil {
		t.Fatalf("a new secret must be usable: %v", err)
	}
}

// TestSignVerifyRoundTrip signs an encoded report with a derived key and
// verifies it with the ext.key the decoder returns.
func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	priv := vectorKey(t)
	body, err := Encode(Report{
		Version: "0.3.0", Servers: 1, Templates: 1,
		Ext: &Extended{
			Schema: 1, InstallID: testID,
			Env:      Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"amd64"}, Nodes: "1"},
			Features: Features{Clusters: "1", DB: "sqlite", Language: "en"},
			Key:      PublicKeyString(priv),
			SentAt:   time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	header := Sign(priv, body)
	if !strings.HasPrefix(header, "ed25519=") {
		t.Fatalf("header %q lacks the ed25519= prefix", header)
	}
	r, _, err := Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := Verify(header, r.Ext.Key, body); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestVerifyDetectsEverySingleByteTamper flips each bit-0 of the body in turn,
// then of the signature, and expects a failure every time.
func TestVerifyDetectsEverySingleByteTamper(t *testing.T) {
	t.Parallel()
	priv := vectorKey(t)
	body := []byte(`{"version":"0.3.0","servers":3,"templates":7}`)
	header := Sign(priv, body)
	pub := PublicKeyString(priv)
	if err := Verify(header, pub, body); err != nil {
		t.Fatalf("untampered body: %v", err)
	}
	for i := range body {
		tampered := bytes.Clone(body)
		tampered[i] ^= 0x01
		err := Verify(header, pub, tampered)
		if !errors.Is(err, ErrBadSignature) {
			t.Fatalf("flipping body byte %d: err = %v, want ErrBadSignature", i, err)
		}
	}
	if err := Verify(header, pub, append(bytes.Clone(body), ' ')); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("appended byte: err = %v, want ErrBadSignature", err)
	}
	if err := Verify(header, pub, body[:len(body)-1]); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("truncated body: err = %v, want ErrBadSignature", err)
	}
	if err := Verify(header, pub, nil); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("empty body: err = %v, want ErrBadSignature", err)
	}

	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(header, "ed25519="))
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	for i := range sig {
		tampered := bytes.Clone(sig)
		tampered[i] ^= 0x01
		h := "ed25519=" + base64.RawURLEncoding.EncodeToString(tampered)
		if err := Verify(h, pub, body); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("flipping signature byte %d: err = %v, want ErrBadSignature", i, err)
		}
	}
}

// TestVerifyRejectsWrongKey checks that another ID's key cannot verify.
func TestVerifyRejectsWrongKey(t *testing.T) {
	t.Parallel()
	body := []byte("body")
	header := Sign(vectorKey(t), body)
	other, err := DeriveKey(vectorSecret(), otherID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if err := Verify(header, PublicKeyString(other), body); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// TestVerifyRejectsMalformedHeaders covers every malformed header form.
func TestVerifyRejectsMalformedHeaders(t *testing.T) {
	t.Parallel()
	body := []byte(vectorBody)
	raw := strings.TrimPrefix(vectorHeader, "ed25519=")
	sig, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode vector: %v", err)
	}
	cases := map[string]string{
		"empty":                  "",
		"prefix only":            "ed25519=",
		"no separator":           "ed25519",
		"wrong algorithm":        "rsa=" + raw,
		"uppercase algorithm":    "ED25519=" + raw,
		"leading space":          " " + vectorHeader,
		"space after equals":     "ed25519= " + raw,
		"trailing space":         vectorHeader + " ",
		"bare signature":         raw,
		"not base64":             "ed25519=!!!!",
		"standard base64":        "ed25519=" + base64.StdEncoding.EncodeToString(sig),
		"padded":                 vectorHeader + "==",
		"too short":              "ed25519=" + raw[:len(raw)-4],
		"too long":               vectorHeader + "AAAA",
		"one byte":               "ed25519=AA",
		"non-canonical tail":     "ed25519=" + raw[:len(raw)-1] + "B",
		"signature with newline": "ed25519=" + raw[:len(raw)/2] + "\n" + raw[len(raw)/2:],
		"two signatures":         vectorHeader + ",ed25519=" + raw,
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			err := Verify(header, vectorPublic, body)
			if !errors.Is(err, ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
}

// TestVerifyRejectsMalformedKeys covers every malformed public key form.
func TestVerifyRejectsMalformedKeys(t *testing.T) {
	t.Parallel()
	body := []byte(vectorBody)
	cases := map[string]string{
		"empty":         "",
		"short":         vectorPublic[:42],
		"long":          vectorPublic + "AA",
		"padded":        vectorPublic + "=",
		"not base64":    "!!!" + vectorPublic[3:],
		"standard":      strings.ReplaceAll(vectorPublic, "_", "/"),
		"non-canonical": vectorPublic[:42] + "t",
		"whitespace":    " " + vectorPublic,
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			err := Verify(vectorHeader, key, body)
			if !errors.Is(err, ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
}

// TestKeyFingerprint checks the fingerprint form and its rejection of bad keys.
func TestKeyFingerprint(t *testing.T) {
	t.Parallel()
	fp := KeyFingerprint(vectorPublic)
	if len(fp) != 64 || fp != strings.ToLower(fp) {
		t.Fatalf("fingerprint %q is not 64 lowercase hex characters", fp)
	}
	if _, err := hex.DecodeString(fp); err != nil {
		t.Fatalf("fingerprint is not hex: %v", err)
	}
	for _, bad := range []string{"", "short", vectorPublic + "=", vectorPublic[:42]} {
		if got := KeyFingerprint(bad); got != "" {
			t.Errorf("KeyFingerprint(%q) = %q, want empty", bad, got)
		}
	}
}

// TestSignatureHeaderName pins the header name shared by both sides.
func TestSignatureHeaderName(t *testing.T) {
	t.Parallel()
	if SignatureHeader != "Gameplane-Telemetry-Signature" {
		t.Fatalf("SignatureHeader = %q", SignatureHeader)
	}
}
