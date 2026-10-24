// Package secretscope defines the operator's explicit Secret read boundary.
package secretscope

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CacheOptions scopes every Secret informer, including Owns and lazy reads,
// without restricting cluster-scoped Gameplane resources or other informers.
// Empty names are rejected: controller-runtime treats an empty namespace as
// all namespaces, and an empty ByObject namespace map inherits the defaults.
func CacheOptions(games, controlPlane, ca, modules string) (cache.Options, error) {
	namespaces := make(map[string]cache.Config)
	for _, namespace := range append(strings.Split(games, ","), controlPlane, ca, modules) {
		if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
			return cache.Options{}, fmt.Errorf("secret namespace %q must be an explicit DNS-label namespace: %s", namespace, strings.Join(problems, "; "))
		}
		namespaces[namespace] = cache.Config{}
	}
	return cache.Options{ByObject: map[client.Object]cache.ByObject{
		&corev1.Secret{}: {Namespaces: namespaces},
	}}, nil
}
