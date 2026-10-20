package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestApplyManagedServiceAnnotations verifies the operator converges
// spec.networking.serviceAnnotations onto the Service: applying desired keys,
// pruning keys removed from spec, and never clobbering annotations written by
// other controllers.
func TestApplyManagedServiceAnnotations(t *testing.T) {
	svc := &corev1.Service{}

	applyManagedServiceAnnotations(svc, map[string]string{"a": "1", "b": "2"})
	if svc.Annotations["a"] != "1" || svc.Annotations["b"] != "2" {
		t.Fatalf("apply: %v", svc.Annotations)
	}
	if svc.Annotations[managedServiceAnnotationsKey] != "a,b" {
		t.Fatalf("sentinel = %q, want a,b", svc.Annotations[managedServiceAnnotationsKey])
	}

	// An annotation written by another controller (e.g. cloud LB).
	svc.Annotations["external/foo"] = "bar"

	// Drop "b" from spec: "a" kept, "b" pruned, "external/foo" preserved.
	applyManagedServiceAnnotations(svc, map[string]string{"a": "1"})
	if svc.Annotations["a"] != "1" {
		t.Errorf("a dropped: %v", svc.Annotations)
	}
	if _, ok := svc.Annotations["b"]; ok {
		t.Errorf("b not pruned: %v", svc.Annotations)
	}
	if svc.Annotations["external/foo"] != "bar" {
		t.Errorf("external annotation clobbered: %v", svc.Annotations)
	}
	if svc.Annotations[managedServiceAnnotationsKey] != "a" {
		t.Errorf("sentinel = %q, want a", svc.Annotations[managedServiceAnnotationsKey])
	}

	// Clear all managed annotations: sentinel removed, external preserved.
	applyManagedServiceAnnotations(svc, nil)
	if _, ok := svc.Annotations["a"]; ok {
		t.Errorf("a not pruned on clear: %v", svc.Annotations)
	}
	if _, ok := svc.Annotations[managedServiceAnnotationsKey]; ok {
		t.Errorf("sentinel not removed: %v", svc.Annotations)
	}
	if svc.Annotations["external/foo"] != "bar" {
		t.Errorf("external annotation clobbered on clear: %v", svc.Annotations)
	}
}

// gsWithNetworking is a tiny constructor for desiredServiceAnnotations table
// cases — only the networking subtree matters here.
func gsWithNetworking(n gameplanev1alpha1.GameServerNetworking) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{Networking: n},
	}
}

// TestDesiredServiceAnnotations verifies the operator overlays the external-dns
// hostname hint onto the user's serviceAnnotations only when hostname is set,
// and that the typed hostname field wins over a same-key entry in
// serviceAnnotations.
func TestDesiredServiceAnnotations(t *testing.T) {
	// (a) Empty hostname → desired equals the raw serviceAnnotations.
	got := desiredServiceAnnotations(gsWithNetworking(gameplanev1alpha1.GameServerNetworking{
		ServiceAnnotations: map[string]string{"a": "1", "b": "2"},
	}))
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Errorf("empty hostname: got %v, want {a:1,b:2}", got)
	}
	if _, ok := got[externalDNSHostnameAnnotation]; ok {
		t.Errorf("empty hostname must not add external-dns key: %v", got)
	}

	// (b) Hostname set → external-dns key present with that value, alongside
	// the user's serviceAnnotations.
	got = desiredServiceAnnotations(gsWithNetworking(gameplanev1alpha1.GameServerNetworking{
		Hostname:           "mc.example.com",
		ServiceAnnotations: map[string]string{"a": "1"},
	}))
	if got["a"] != "1" {
		t.Errorf("user annotation dropped: %v", got)
	}
	if got[externalDNSHostnameAnnotation] != "mc.example.com" {
		t.Errorf("external-dns key = %q, want mc.example.com", got[externalDNSHostnameAnnotation])
	}

	// (c) The typed hostname field overrides a same-key entry the user placed
	// directly in serviceAnnotations.
	got = desiredServiceAnnotations(gsWithNetworking(gameplanev1alpha1.GameServerNetworking{
		Hostname:           "typed.example.com",
		ServiceAnnotations: map[string]string{externalDNSHostnameAnnotation: "raw.example.com"},
	}))
	if got[externalDNSHostnameAnnotation] != "typed.example.com" {
		t.Errorf("typed hostname must win: got %q, want typed.example.com",
			got[externalDNSHostnameAnnotation])
	}

	// (d) Nil serviceAnnotations + hostname → single-key map.
	got = desiredServiceAnnotations(gsWithNetworking(gameplanev1alpha1.GameServerNetworking{
		Hostname: "solo.example.com",
	}))
	if len(got) != 1 || got[externalDNSHostnameAnnotation] != "solo.example.com" {
		t.Errorf("nil serviceAnnotations + hostname: got %v, want single external-dns key", got)
	}
}
