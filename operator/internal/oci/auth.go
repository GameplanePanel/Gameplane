package oci

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/GameplanePanel/gameplane/operator/internal/secretscope"
)

// dockerConfigJSON is the subset of dockerconfigjson Secret payload we
// read. Mirrors what kubelet uses for image pull.
type dockerConfigJSON struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"` // base64("user:pass")
	Token    string `json:"identitytoken,omitempty"`
}

// CredentialFunc returns the oras-go credential resolver to attach to a
// remote.Repository's Client. registry is the registry hostname (e.g.
// "ghcr.io"), looked up against the secret's auth map.
type CredentialFunc = func(ctx context.Context, registry string) (auth.Credential, error)

// CredentialFromSecret resolves dockerconfigjson credentials for one
// registry. ns/name addresses a Secret in the cluster; cli is the
// controller-runtime client. When secretRef is nil, returns
// auth.EmptyCredential (anonymous).
func CredentialFromSecret(ctx context.Context, cli client.Client, ns string, secretRef *corev1.LocalObjectReference) (CredentialFunc, error) {
	if secretRef == nil || secretRef.Name == "" {
		return func(context.Context, string) (auth.Credential, error) {
			return auth.EmptyCredential, nil
		}, nil
	}
	sec, err := secretscope.ReadModuleSecret(ctx, cli, ns, secretRef.Name)
	if err != nil {
		return nil, fmt.Errorf("get pull secret %s/%s: %w", ns, secretRef.Name, err)
	}
	raw, ok := sec.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no %q key", ns, secretRef.Name, corev1.DockerConfigJsonKey)
	}
	var cfg dockerConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse dockerconfigjson in %s/%s: %w", ns, secretRef.Name, err)
	}
	return func(_ context.Context, registry string) (auth.Credential, error) {
		entry, ok := cfg.Auths[registry]
		if !ok {
			return auth.EmptyCredential, nil
		}
		cred := auth.Credential{
			Username:     entry.Username,
			Password:     entry.Password,
			RefreshToken: entry.Token,
		}
		if entry.Auth != "" && (cred.Username == "" || cred.Password == "") {
			u, p, err := parseBasicAuth(entry.Auth)
			if err != nil {
				return auth.EmptyCredential, fmt.Errorf("decode auth for %s: %w", registry, err)
			}
			cred.Username, cred.Password = u, p
		}
		return cred, nil
	}, nil
}

func parseBasicAuth(b64 string) (user, pass string, err error) {
	dec, err := base64Decode(b64)
	if err != nil {
		return "", "", err
	}
	for i := 0; i < len(dec); i++ {
		if dec[i] == ':' {
			return string(dec[:i]), string(dec[i+1:]), nil
		}
	}
	return "", "", fmt.Errorf("missing ':' in basic-auth payload")
}
