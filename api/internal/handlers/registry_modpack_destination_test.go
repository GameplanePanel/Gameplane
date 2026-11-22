package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func destinationSecret(ns, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{destinationLabel: "true"},
		},
	}
}

func TestBackupDestinationNameFirstByName(t *testing.T) {
	const ns = "games"
	cs := k8sfake.NewSimpleClientset(
		destinationSecret(ns, "b-dest"),
		destinationSecret(ns, "a-dest"),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled", Namespace: ns}},
	)
	h := &registryHandler{}
	target := &modTarget{k: &kube.Client{Typed: cs}, namespace: ns}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if got := h.backupDestinationName(req, target); got != "a-dest" {
		t.Errorf("backupDestinationName = %q, want a-dest", got)
	}
}

func TestBackupDestinationNameNone(t *testing.T) {
	const ns = "games"
	cs := k8sfake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled", Namespace: ns}})
	h := &registryHandler{}
	target := &modTarget{k: &kube.Client{Typed: cs}, namespace: ns}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if got := h.backupDestinationName(req, target); got != "" {
		t.Errorf("backupDestinationName = %q, want empty", got)
	}
	if got := h.backupDestinationName(req, &modTarget{namespace: ns}); got != "" {
		t.Errorf("nil client backupDestinationName = %q, want empty", got)
	}
}
