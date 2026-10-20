package telemetryschema

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SignatureHeader is the HTTP request header that carries the report
// signature. It is required when the report has an ext part and absent
// otherwise.
const SignatureHeader = "Gameplane-Telemetry-Signature"

// signaturePrefix starts the header value: "ed25519=<base64url signature>".
const signaturePrefix = "ed25519="

// SecretSize is the length in bytes of the install's signing secret.
const SecretSize = 32

// keyInfo is the HKDF context string for signing-key derivation.
const keyInfo = "gameplane-telemetry-signing-v1"

// ErrBadSignature is wrapped by every Verify failure: a missing or malformed
// header, a malformed public key, or a signature that does not match.
var ErrBadSignature = errors.New("bad signature")

// NewSecret returns a new signing secret: SecretSize random bytes.
func NewSecret() ([]byte, error) {
	secret := make([]byte, SecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("telemetryschema: generate signing secret: %w", err)
	}
	return secret, nil
}

// DeriveKey derives the Ed25519 signing key for installID from the install's
// signing secret: seed = HKDF-SHA256(secret, salt = installID, info =
// "gameplane-telemetry-signing-v1", 32 bytes). The key is unique to the ID, so
// a new ID always yields an unlinkable key. Callers recompute it on every send
// and never store it.
func DeriveKey(secret []byte, installID string) (ed25519.PrivateKey, error) {
	if len(secret) != SecretSize {
		return nil, fmt.Errorf("telemetryschema: signing secret is %d bytes, want %d", len(secret), SecretSize)
	}
	if installID == "" {
		return nil, errors.New("telemetryschema: install ID is empty")
	}
	seed, err := hkdf.Key(sha256.New, secret, []byte(installID), keyInfo, ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("telemetryschema: derive signing key: %w", err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// PublicKeyString returns the public half of priv as unpadded base64url, the
// form carried in ext.key.
func PublicKeyString(priv ed25519.PrivateKey) string {
	// An Ed25519 private key is seed || public key.
	return base64.RawURLEncoding.EncodeToString(priv[ed25519.SeedSize:])
}

// Sign signs the exact body bytes and returns the header value
// "ed25519=<base64url signature>".
func Sign(priv ed25519.PrivateKey, body []byte) string {
	return signaturePrefix + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, body))
}

// Verify checks that header, the value of SignatureHeader, is a valid
// signature of body under publicKey, an unpadded base64url Ed25519 public key.
// Every failure wraps ErrBadSignature.
func Verify(header, publicKey string, body []byte) error {
	encoded, ok := strings.CutPrefix(header, signaturePrefix)
	if !ok {
		return fmt.Errorf("%w: header must start with %q", ErrBadSignature, signaturePrefix)
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: malformed signature", ErrBadSignature)
	}
	if base64.RawURLEncoding.EncodeToString(sig) != encoded {
		return fmt.Errorf("%w: malformed signature", ErrBadSignature)
	}
	pub, err := decodePublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	if !ed25519.Verify(pub, body, sig) {
		return fmt.Errorf("%w: signature does not match", ErrBadSignature)
	}
	return nil
}

// KeyFingerprint returns the hex SHA-256 of the raw public key bytes, the
// receiver's stored claim (key_fp). It returns "" when publicKey is not a
// valid unpadded base64url 32-byte key.
func KeyFingerprint(publicKey string) string {
	raw, err := decodePublicKey(publicKey)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
