// Package verify performs cosign signature verification of OCI module
// bundles. A ModuleSource may declare spec.verify (keyed or keyless); the
// operator then refuses to install a bundle that does not carry a matching
// signature, so a compromised registry can't serve a forged GameTemplate.
package verify

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	cosignsig "github.com/sigstore/cosign/v3/pkg/signature"
	"github.com/sigstore/sigstore-go/pkg/root"
	sgtuf "github.com/sigstore/sigstore-go/pkg/tuf"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// cosignPubKey is the Secret data key holding a keyed-verification public key.
const cosignPubKey = "cosign.pub"

// Verifier checks that the OCI artifact at ref@digest carries a valid cosign
// signature under a source's policy.
type Verifier interface {
	Verify(ctx context.Context, ref, digest string) error
}

// Nop accepts everything. Returned when a source declares no verify policy.
type Nop struct{}

// Verify always succeeds.
func (Nop) Verify(context.Context, string, string) error { return nil }

// Build constructs the Verifier for a source from spec.verify, returning Nop
// when verification is not configured. The public key and registry pull
// secret are resolved from namespace.
func Build(ctx context.Context, c client.Client, namespace string, src *gameplanev1alpha1.ModuleSource) (Verifier, error) {
	if src.Spec.Verify == nil {
		return Nop{}, nil
	}
	if src.Spec.OCI == nil {
		return nil, errors.New("spec.verify requires an oci source")
	}
	prefix := src.Spec.OCI.URL
	if prefix == "" || strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "://") {
		return nil, errors.New("parse OCI source registry: expected a registry/repository prefix without a URL scheme")
	}
	// The source is a prefix, possibly just host:port. Like the OCI fetcher,
	// append a module name before parsing so bare hosts are recognized as
	// registries instead of Docker Hub repository names.
	repository, err := name.NewRepository(path.Join(prefix, "module"))
	if err != nil {
		return nil, fmt.Errorf("parse OCI source registry: %w", err)
	}
	auth, err := authFor(ctx, c, namespace, src.Spec.OCI.PullSecretRef, repository.RegistryStr())
	if err != nil {
		return nil, err
	}
	insecure := src.Spec.OCI.Insecure
	v := src.Spec.Verify
	var verifier Verifier
	switch {
	case v.Key != nil:
		pub, readErr := readKey(ctx, c, namespace, v.Key.Name)
		if readErr != nil {
			return nil, readErr
		}
		verifier, err = newKeyed(ctx, pub, auth, insecure, v.RequireTransparencyLog)
	case v.Keyless != nil:
		verifier, err = newKeyless(ctx, v.Keyless.Issuer, v.Keyless.Identity, auth, insecure)
	default:
		return nil, errors.New("spec.verify must set key or keyless")
	}
	if err != nil {
		return nil, err
	}
	return &registryVerifier{Verifier: verifier, registry: repository.RegistryStr()}, nil
}

// Catalog references may lag a source URL change. Never send credentials selected
// for the current source to a different registry named by a stale catalog entry.
type registryVerifier struct {
	Verifier
	registry string
}

func (v *registryVerifier) Verify(ctx context.Context, ref, digest string) error {
	repository, err := name.NewRepository(ref)
	if err != nil {
		return fmt.Errorf("parse signature repository: %w", err)
	}
	if repository.RegistryStr() != v.registry {
		return errors.New("signature repository does not match the source registry")
	}
	return v.Verifier.Verify(ctx, ref, digest)
}

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
		log.FromContext(ctx).Error(nil, msg, tufRootEnv, os.Getenv(tufRootEnv))
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

func readKey(ctx context.Context, c client.Client, namespace, name string) ([]byte, error) {
	var sec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &sec); err != nil {
		return nil, fmt.Errorf("get cosign key secret %s: %w", name, err)
	}
	pub, ok := sec.Data[cosignPubKey]
	if !ok || len(pub) == 0 {
		return nil, fmt.Errorf("secret %s has no %q data key", name, cosignPubKey)
	}
	return pub, nil
}

// authFor resolves a registry pull secret (kubernetes.io/dockerconfigjson)
// into a ggcr authenticator for fetching signatures, falling back to
// anonymous when the Secret has no entry for that registry. Docker configuration
// Secrets may contain credentials for several independent registries.
func authFor(ctx context.Context, c client.Client, namespace string, ref *corev1.LocalObjectReference, registry string) (authn.Authenticator, error) {
	if ref == nil || ref.Name == "" {
		return authn.Anonymous, nil
	}
	var sec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &sec); err != nil {
		return nil, fmt.Errorf("get pull secret %s: %w", ref.Name, err)
	}
	cfg := sec.Data[corev1.DockerConfigJsonKey]
	if len(cfg) == 0 {
		return authn.Anonymous, nil
	}
	var dc struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(cfg, &dc); err != nil {
		return nil, fmt.Errorf("parse dockerconfigjson in %s: %w", ref.Name, err)
	}
	e, ok := dc.Auths[registry]
	// go-containerregistry calls Docker Hub index.docker.io, while ORAS
	// pulls from registry-1.docker.io. These names identify the same service;
	// only this explicit alias set may share credentials. Exact entries win.
	if !ok && isDockerHubRegistry(registry) {
		for _, alias := range []string{"registry-1.docker.io", "index.docker.io", "docker.io"} {
			if e, ok = dc.Auths[alias]; ok {
				break
			}
		}
	}
	if !ok {
		return authn.Anonymous, nil
	}
	switch {
	case e.Username != "" || e.Password != "":
		return authn.FromConfig(authn.AuthConfig{Username: e.Username, Password: e.Password}), nil
	case e.Auth != "":
		return authn.FromConfig(authn.AuthConfig{Auth: e.Auth}), nil
	}
	return authn.Anonymous, nil
}

func isDockerHubRegistry(registry string) bool {
	switch registry {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return true
	default:
		return false
	}
}
