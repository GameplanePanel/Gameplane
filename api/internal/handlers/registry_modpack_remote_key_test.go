package handlers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/registry"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// The share flag is read from the management cluster's Secret, but a remote
// cluster's operator never gets the key: a server on another cluster counts as
// "not shared" even when sharing is on at home.
func TestModpacksBlockedReason_RemoteClusterKeyNotShared(t *testing.T) {
	home := fakeKubeClient()
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: registry.DefaultKeySecretName("curseforge"), Namespace: "ctl",
			Labels:      map[string]string{registry.RegistryKeySecretLabel: "true"},
			Annotations: map[string]string{curseforgeShareAnnotation: "true"},
		},
		Data: map[string][]byte{"apiKey": []byte("k")},
	}
	if _, err := home.Typed.CoreV1().Secrets("ctl").Create(context.Background(), sec, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	h := &registryHandler{home: home, controlNS: "ctl"}
	tmpl := modpackTestTemplate("minecraft")
	prov, ok := pickProvider(tmpl, "curseforge")
	if !ok {
		t.Fatal("curseforge provider missing from the test template")
	}
	server := modpackServerObj("alpha", "minecraft", "1.12.2-forge", "", nil)

	local := &modTarget{cluster: scope.DefaultCluster, server: server, template: tmpl}
	if got := h.modpacksBlockedReason(context.Background(), local, prov); got != "" {
		t.Errorf("home cluster with sharing on: blocked reason %q, want none", got)
	}
	remote := &modTarget{cluster: "edge-1", server: server, template: tmpl}
	if got := h.modpacksBlockedReason(context.Background(), remote, prov); got != blockedCurseForgeKeyNotShared {
		t.Errorf("remote cluster: blocked reason %q, want %q", got, blockedCurseForgeKeyNotShared)
	}
}
