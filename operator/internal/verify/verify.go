// Package verify adapts bundlefetch/verify to ModuleSource: it reads the
// source's spec.verify policy, its public key Secret and its registry pull
// Secret, then asks bundlefetch to verify the cosign signature of OCI module
// bundles. A ModuleSource may declare spec.verify (keyed or keyless); the
// operator then refuses to install a bundle that does not carry a matching
// signature, so a compromised registry can't serve a forged GameTemplate.
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bfverify "github.com/GameplanePanel/gameplane/bundlefetch/verify"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/secretscope"
)

// cosignPubKey is the Secret data key holding a keyed-verification public key.
const cosignPubKey = "cosign.pub"

// Verifier checks that the OCI artifact at ref@digest carries a valid cosign
// signature under a source's policy. It is bundlefetch's Verifier.
type Verifier = bfverify.Verifier

// Nop accepts everything. Returned when a source declares no verify policy.
type Nop = bfverify.Nop

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
		verifier, err = bfverify.NewKeyed(ctx, pub, auth, insecure, v.RequireTransparencyLog)
	case v.Keyless != nil:
		verifier, err = bfverify.NewKeyless(ctx, v.Keyless.Issuer, v.Keyless.Identity, auth, insecure)
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

// readKey loads cosign.pub from the configured module namespace. The shared
// Secret reader rejects service-account token Secrets even if they contain that key.
func readKey(ctx context.Context, c client.Client, namespace, name string) ([]byte, error) {
	sec, err := secretscope.ReadModuleSecret(ctx, c, namespace, name)
	if err != nil {
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
	sec, err := secretscope.ReadModuleSecret(ctx, c, namespace, ref.Name)
	if err != nil {
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
