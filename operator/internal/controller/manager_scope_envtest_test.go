//go:build envtest

package controller

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// The envtest API has no namespace garbage collector. Each new manager must
// ignore earlier fixtures while continuing to read cluster-scoped templates.
func TestManagerCacheKeepsNamespaceIsolationAndClusterScope(t *testing.T) {
	ns, other := newNamespace(t), newNamespace(t)
	tmpl := buildGameTemplate(uniqueName("cache-scope"))
	if err := k8sClient.Create(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	deleteCleanup(t, tmpl)
	for _, namespace := range []string{ns, other} {
		if err := k8sClient.Create(t.Context(), buildGameServer(namespace, "server", tmpl.Name)); err != nil {
			t.Fatal(err)
		}
	}
	cached := startMgr(t, ns)
	var templates gameplanev1alpha1.GameTemplate
	if err := cached.Get(t.Context(), client.ObjectKeyFromObject(tmpl), &templates); err != nil {
		t.Fatalf("cluster-scoped template unavailable: %v", err)
	}
	var servers gameplanev1alpha1.GameServerList
	if err := cached.List(t.Context(), &servers); err != nil {
		t.Fatal(err)
	}
	if len(servers.Items) != 1 || servers.Items[0].Namespace != ns {
		t.Fatalf("manager cache included resources outside its test namespace: %+v", servers.Items)
	}
}
