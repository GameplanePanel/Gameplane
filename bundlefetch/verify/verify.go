// Package verify performs cosign signature verification of OCI bundles: a
// keyed policy (a public key) or a keyless one (Fulcio identity and issuer).
// It holds the cosign core only. Resolving keys and registry credentials from
// a Kubernetes Secret or a database is the caller's job; it hands this package
// the PEM key bytes and a go-containerregistry authenticator. The package
// imports nothing from Kubernetes.
package verify

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	cosignsig "github.com/sigstore/cosign/v3/pkg/signature"
	"github.com/sigstore/sigstore-go/pkg/root"
	sgtuf "github.com/sigstore/sigstore-go/pkg/tuf"
)

// Verifier checks that the OCI artifact at ref@digest carries a valid cosign
// signature under a source's policy.
type Verifier interface {
	Verify(ctx context.Context, ref, digest string) error
}

// Nop accepts everything. Returned when a source declares no verify policy.
type Nop struct{}

// Verify always succeeds.
func (Nop) Verify(context.Context, string, string) error { return nil }

// cosignVerifier adapts the cosign library to the Verifier interface.
// mkOpts rebuilds CheckOpts per call so the request context flows into the
// registry client.
type cosignVerifier struct {
	mkOpts   func(ctx context.Context) *cosign.CheckOpts
	insecure bool
}

func (cv *cosignVerifier) Verify(ctx context.Context, ref, digest string) error {
	var opts []name.Option
	if cv.insecure {
		opts = append(opts, name.Insecure)
	}
	r, err := name.NewDigest(ref+"@"+digest, opts...)
	if err != nil {
		return fmt.Errorf("parse %s@%s: %w", ref, digest, err)
	}
	if _, _, err := cosign.VerifyImageSignatures(ctx, r, cv.mkOpts(ctx)); err != nil {
		return fmt.Errorf("cosign verify %s@%s: %w", ref, digest, err)
	}
	return nil
}

func baseCheckOpts(ctx context.Context, auth authn.Authenticator) *cosign.CheckOpts {
	return &cosign.CheckOpts{
		ClaimVerifier: cosign.SimpleClaimVerifier,
		RegistryClientOpts: []ociremote.Option{
			ociremote.WithRemoteOptions(
				ggcrremote.WithAuth(auth),
				ggcrremote.WithContext(ctx),
			),
		},
	}
}

func newKeyed(ctx context.Context, pubPEM []byte, auth authn.Authenticator, insecure, requireTlog bool) (Verifier, error) {
	ver, err := cosignsig.LoadPublicKeyRaw(pubPEM, crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("load cosign public key: %w", err)
	}
	// When the source requires transparency-log inclusion, load the Rekor
	// public keys once here rather than per verification: a failure to reach
	// the Sigstore trust root is a configuration problem the caller should see
	// immediately, not a per-bundle verification error.
	var rekorPubs *cosign.TrustedTransparencyLogPubKeys
	if requireTlog {
		rekorPubs, err = cosign.GetRekorPubs(ctx)
		if err != nil {
			return nil, fmt.Errorf("load rekor public keys (verify.requireTransparencyLog): %w", err)
		}
	}
	mk := func(ctx context.Context) *cosign.CheckOpts {
		co := baseCheckOpts(ctx, auth)
		co.SigVerifier = ver
		// A keyed signature carries no Fulcio certificate, so there is never
		// an SCT to check, whatever the log policy is.
		co.IgnoreSCT = true
		// Offline either way: with the log required, the inclusion proof
		// bundled with the signature is verified against the Rekor keys loaded
		// above — no live log query per bundle.
		co.Offline = true
		if rekorPubs != nil {
			co.RekorPubKeys = rekorPubs
			co.IgnoreTlog = false
		} else {
			co.IgnoreTlog = true
		}
		return co
	}
	return &cosignVerifier{mkOpts: mk, insecure: insecure}, nil
}

// tufRootEnv is the variable the deprecated sigstore/pkg/tuf client read to
// locate a cosign-initialized TUF directory.
const tufRootEnv = "TUF_ROOT"

// tufRootWarning reports whether a legacy TUF_ROOT is set, and returns the warning
// to log when it is. See fulcioPools for why it cannot be honoured.
func tufRootWarning() (string, bool) {
	dir := os.Getenv(tufRootEnv)
	if dir == "" {
		return "", false
	}
	return fmt.Sprintf("%s is set to %q, but a private Sigstore instance configured through it is NOT being used: "+
		"module signature verification is proceeding against the PUBLIC-GOOD Sigstore trust root instead. "+
		"Configure a private Fulcio explicitly on the ModuleSource CRD; this environment variable is ignored.",
		tufRootEnv, dir), true
}

// poolsFromCAs turns the trusted root's Fulcio certificate authorities into the
// root and intermediate pools cosign expects. It is separated from the TUF fetch
// so the trust decision — which certificate lands in which pool — is unit-testable
// without network access.
func poolsFromCAs(cas []root.CertificateAuthority) (*x509.CertPool, *x509.CertPool, error) {
	roots := x509.NewCertPool()
	intermediates := x509.NewCertPool()
	var nRoots int
	for _, ca := range cas {
		fca, ok := ca.(*root.FulcioCertificateAuthority)
		if !ok {
			continue
		}
		if fca.Root != nil {
			roots.AddCert(fca.Root)
			nRoots++
		}
		for _, ic := range fca.Intermediates {
			intermediates.AddCert(ic)
		}
	}
	if nRoots == 0 {
		return nil, nil, errors.New("no Fulcio certificate authorities in the sigstore trusted root")
	}
	return roots, intermediates, nil
}

// fulcioPools loads the Fulcio root and intermediate certificate pools from the
// Sigstore TUF trust root.
//
// This replaces github.com/sigstore/sigstore/pkg/fulcioroots, which sigstore
// deprecated in v1.10.9.
//
// The two differ in one way that matters. The old client read TUF_ROOT, and that
// directory carried not just a cache but the pinned root.json and a remote.json
// naming the mirror, so setting it genuinely repointed verification at a private
// Sigstore instance. sigstore-go offers no environment override for either the
// trust anchor or the mirror: DefaultOptions hardcodes an embedded public-good
// root.json and the public mirror, and reads the environment only to site its
// cache directory. Mapping TUF_ROOT onto that CachePath would
// therefore preserve nothing while silently building the root pool from the
// PUBLIC-GOOD Fulcio CA — verification would then succeed against a CA the
// deployment never configured. Rather than fail closed, this now warns loudly and
// proceeds against the public-good root: a deployment configured for a private
// Fulcio via TUF_ROOT will be verifying bundle signatures against a certificate
// authority it did not configure, which is why the warning above is emphatic
// rather than a routine log line. A private instance should still be configured
// explicitly on the ModuleSource CRD rather than through this environment
// side-channel, which sigstore-go itself no longer honours.
func fulcioPools(ctx context.Context) (*x509.CertPool, *x509.CertPool, error) {
	if msg, warn := tufRootWarning(); warn {
		logTUFRootWarning(ctx, msg)
	}
	tr, err := root.FetchTrustedRootWithOptions(sgtuf.DefaultOptions())
	if err != nil {
		return nil, nil, fmt.Errorf("fetch sigstore trusted root: %w", err)
	}
	return poolsFromCAs(tr.FulcioCertificateAuthorities())
}

func newKeyless(ctx context.Context, issuer, identity string, auth authn.Authenticator, insecure bool) (Verifier, error) {
	roots, intermediates, err := fulcioPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("load fulcio roots: %w", err)
	}
	rekorPubs, err := cosign.GetRekorPubs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load rekor public keys: %w", err)
	}
	ctPubs, err := cosign.GetCTLogPubs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load ctlog public keys: %w", err)
	}
	mk := func(ctx context.Context) *cosign.CheckOpts {
		co := baseCheckOpts(ctx, auth)
		co.RootCerts = roots
		co.IntermediateCerts = intermediates
		co.RekorPubKeys = rekorPubs
		co.CTLogPubKeys = ctPubs
		co.Identities = []cosign.Identity{{Issuer: issuer, Subject: identity}}
		return co
	}
	return &cosignVerifier{mkOpts: mk, insecure: insecure}, nil
}

// NewKeyed returns the Verifier for a keyed policy: pubPEM is a PEM-encoded
// cosign public key, auth authenticates the signature fetch, insecure allows a
// plain-HTTP registry, and requireTlog demands Rekor inclusion (it loads the
// Rekor keys once, so an unreachable Sigstore trust root fails here).
func NewKeyed(ctx context.Context, pubPEM []byte, auth authn.Authenticator, insecure, requireTlog bool) (Verifier, error) {
	return newKeyed(ctx, pubPEM, auth, insecure, requireTlog)
}

// NewKeyless returns the Verifier for a keyless policy: signatures must carry a
// Fulcio certificate for the given OIDC issuer and subject identity. It loads
// the Fulcio, Rekor and CT log trust material once, from the Sigstore TUF root.
func NewKeyless(ctx context.Context, issuer, identity string, auth authn.Authenticator, insecure bool) (Verifier, error) {
	return newKeyless(ctx, issuer, identity, auth, insecure)
}

// logTUFRootWarning reports the TUF_ROOT warning through the logr logger in ctx
// (controller-runtime puts one there, so the operator's output is unchanged),
// and through slog when the caller carries none (the standalone API).
func logTUFRootWarning(ctx context.Context, msg string) {
	if l, err := logr.FromContext(ctx); err == nil {
		l.Error(nil, msg, tufRootEnv, os.Getenv(tufRootEnv))
		return
	}
	slog.ErrorContext(ctx, msg, tufRootEnv, os.Getenv(tufRootEnv))
}
