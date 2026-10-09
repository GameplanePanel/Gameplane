package kube

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	remoteconfig "github.com/GameplanePanel/gameplane/api/internal/kubeconfig"
)

// ClusterKubeconfigLabel marks a Secret as containing a kubeconfig for a remote cluster.
// The label must have the value "true".
const ClusterKubeconfigLabel = "gameplane.local/cluster-kubeconfig"

// ConfigFromKubeconfig parses self-contained kubeconfig bytes and returns a rest.Config.
func ConfigFromKubeconfig(data []byte) (*rest.Config, error) {
	cfg, err := remoteconfig.RESTConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	return cfg, nil
}

// ClientFromSecret loads a kubeconfig from a Secret and returns a Client for that cluster.
// The Secret must have label gameplane.local/cluster-kubeconfig=true.
// key defaults to "kubeconfig" if empty.
func ClientFromSecret(ctx context.Context, home *Client, ns, name, key string) (*Client, error) {
	if key == "" {
		key = "kubeconfig"
	}
	secret, err := home.Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}
	// Verify the secret has the required label to prevent loading arbitrary secrets.
	if secret.Labels == nil || secret.Labels[ClusterKubeconfigLabel] != "true" {
		return nil, fmt.Errorf("secret missing required label %s=true", ClusterKubeconfigLabel)
	}
	data, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("secret data missing key %q", key)
	}
	var cfg *rest.Config
	if home.IsStandalone() {
		cfg, err = ValidateStandaloneKubeconfig(data)
		if err == nil {
			err = home.RemoteAccess.ValidateURL(cfg.Host)
			ApplyStandaloneRemotePolicy(cfg, home.RemoteAccess)
		}
	} else {
		cfg, err = ConfigFromKubeconfig(data)
	}
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig from secret: %w", err)
	}
	c, err := New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create client from config: %w", err)
	}
	c.kubeconfigDigest = sha256.Sum256(data)
	return c, nil
}

// IsKubeNotFound returns true if err represents a Kubernetes NotFound error.
func IsKubeNotFound(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Status().Code == 404
	}
	return false
}

// IsKubeAlreadyExists returns true if err represents a Kubernetes AlreadyExists error.
func IsKubeAlreadyExists(err error) bool {
	return apierrors.IsAlreadyExists(err)
}
