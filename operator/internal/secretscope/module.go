package secretscope

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadModuleSecret resolves a ModuleSource reference. The controller must not
// use a Kubernetes ServiceAccount token to authenticate to a source-selected host.
func ReadModuleSecret(ctx context.Context, reader client.Reader, namespace, name string) (*corev1.Secret, error) {
	var secret corev1.Secret
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &secret); err != nil {
		return nil, err
	}
	if secret.Type == corev1.SecretTypeServiceAccountToken {
		return nil, errors.New("module sources cannot use ServiceAccount token Secrets")
	}
	return &secret, nil
}
