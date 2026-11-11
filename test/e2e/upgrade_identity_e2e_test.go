//go:build e2e

package e2e

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Snapshot the populated installation's storage and trust identities. Remaining
// Gameplane CRDs must include every version still recorded as stored by Kubernetes.
func readUpgradeIdentity(t *testing.T, gamesNamespace, gs string) map[string]string {
	t.Helper()
	identity := map[string]string{}
	for _, claim := range [][2]string{{"gameplane-system", "gameplane-api-data"}, {gamesNamespace, gs + "-data"}} {
		pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(claim[0]).Get(t.Context(), claim[1], metav1.GetOptions{})
		if err != nil || pvc.UID == "" {
			t.Fatalf("read PVC identity %s/%s: %v", claim[0], claim[1], err)
		}
		identity[claim[0]+"/"+claim[1]] = string(pvc.UID)
	}
	ca, err := envInstance.K8s.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gameplane-agent-ca", metav1.GetOptions{})
	if err != nil || len(ca.Data["ca.crt"]) == 0 {
		t.Fatalf("read public agent CA certificate: %v", err)
	}
	identity["agent CA fingerprint"] = fmt.Sprintf("%x", sha256.Sum256(ca.Data["ca.crt"]))
	crds, err := envInstance.Dyn.Resource(crdGVR).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, crd := range crds.Items {
		if !strings.HasSuffix(crd.GetName(), ".gameplane.local") {
			continue
		}
		if crd.GetUID() == "" {
			t.Fatalf("%s has no UID", crd.GetName())
		}
		identity[crd.GetName()] = string(crd.GetUID())
		var typed apiextensionsv1.CustomResourceDefinition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(crd.Object, &typed); err != nil {
			t.Fatal(err)
		}
		if len(typed.Status.StoredVersions) == 0 {
			t.Fatalf("%s has no storedVersions", crd.GetName())
		}
		for _, stored := range typed.Status.StoredVersions {
			if !slices.ContainsFunc(typed.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool { return v.Name == stored }) {
				t.Fatalf("%s stored version %s is absent from the schema", crd.GetName(), stored)
			}
		}
	}
	if identity["gameservers.gameplane.local"] == "" {
		t.Fatal("missing GameServer CRD")
	}
	return identity
}
