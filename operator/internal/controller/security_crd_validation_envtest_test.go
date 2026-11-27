//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// These cases cover the CEL and bounds rules on spec.security (GameServer
// overrides, GameTemplate defaults and risky reasons) and on
// Cluster.spec.controlPlaneNamespace. They run against the CRD schemas the
// suite loads from operator/config/crd, like crd_validation_envtest_test.go.

func securityCRDIDOverride(value *int64, useImageDefault bool) *gameplanev1alpha1.SecurityIDOverride {
	o := &gameplanev1alpha1.SecurityIDOverride{Value: value}
	if useImageDefault {
		t := true
		o.UseImageDefault = &t
	}
	return o
}

func securityCRDBool(v bool) *bool {
	return &v
}

func securityCRDInt64(v int64) *int64 {
	return &v
}

func TestSecurityCRD_GameServerOverrideAccepted(t *testing.T) {
	cases := []struct {
		name string
		sec  *gameplanev1alpha1.GameServerSecuritySpec
	}{
		{"runAsUser value", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: securityCRDIDOverride(securityCRDInt64(1000), false)}},
		{"runAsUser useImageDefault", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: securityCRDIDOverride(nil, true)}},
		{"runAsUser value 4294967295 bound", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: securityCRDIDOverride(securityCRDInt64(4294967295), false)}},
		{"runAsGroup and fsGroup values", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsGroup: securityCRDIDOverride(securityCRDInt64(1000), false),
			FSGroup:    securityCRDIDOverride(securityCRDInt64(1000), false)}},
		{"volumeOwnershipCheck EveryStart", &gameplanev1alpha1.GameServerSecuritySpec{
			VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheckEveryStart}},
		{"volumeOwnershipCheck OnlyWhenWrong", &gameplanev1alpha1.GameServerSecuritySpec{
			VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			gs := validGameServer(ns, "sec-ok")
			gs.Spec.Security = tc.sec
			if err := k8sClient.Create(context.Background(), gs); err != nil {
				t.Fatalf("expected accepted, got %v", err)
			}
		})
	}
}

func TestSecurityCRD_GameServerOverrideRejected(t *testing.T) {
	cases := []struct {
		name string
		sec  *gameplanev1alpha1.GameServerSecuritySpec
	}{
		{"runAsUser both value and useImageDefault", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: securityCRDIDOverride(securityCRDInt64(1000), true)}},
		{"runAsUser neither value nor useImageDefault", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: &gameplanev1alpha1.SecurityIDOverride{}}},
		{"runAsUser useImageDefault false", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: securityCRDBool(false)}}},
		{"runAsUser value -1", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: securityCRDIDOverride(securityCRDInt64(-1), false)}},
		{"runAsUser value 4294967296", &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: securityCRDIDOverride(securityCRDInt64(4294967296), false)}},
		{"volumeOwnershipCheck bad enum", &gameplanev1alpha1.GameServerSecuritySpec{
			VolumeOwnershipCheck: gameplanev1alpha1.VolumeOwnershipCheck("Sometimes")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			gs := validGameServer(ns, "sec-bad")
			gs.Spec.Security = tc.sec
			err := k8sClient.Create(context.Background(), gs)
			if !apierrors.IsInvalid(err) {
				t.Fatalf("expected Invalid, got %v", err)
			}
		})
	}
}

func TestSecurityCRD_GameServerWithoutSecurityStillApplies(t *testing.T) {
	ns := newNamespace(t)
	gs := validGameServer(ns, "sec-absent")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("GameServer without spec.security rejected: %v", err)
	}
	var got gameplanev1alpha1.GameServer
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "sec-absent"}, &got); err != nil {
		t.Fatalf("get GameServer: %v", err)
	}
	if got.Spec.Security != nil {
		t.Fatalf("expected spec.security to stay absent, got %+v", got.Spec.Security)
	}
}

func TestSecurityCRD_GameTemplateRiskyReasonBound(t *testing.T) {
	newTmpl := func(name, reason string) *gameplanev1alpha1.GameTemplate {
		return &gameplanev1alpha1.GameTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: gameplanev1alpha1.GameTemplateSpec{
				DisplayName: "security risky reason",
				Game:        "busybox",
				Version:     "1",
				Image:       "busybox:1.36",
				Security: &gameplanev1alpha1.GameSecuritySpec{
					Risky: &gameplanev1alpha1.SecurityRiskSpec{
						RunAsUser: &gameplanev1alpha1.SecurityRiskNote{Reason: reason},
					},
				},
			},
		}
	}

	tooLong := newTmpl("sec-risky-reason-201", strings.Repeat("x", 201))
	if err := k8sClient.Create(context.Background(), tooLong); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for 201-character risky reason, got %v", err)
	}

	atLimit := newTmpl("sec-risky-reason-200", strings.Repeat("x", 200))
	if err := k8sClient.Create(context.Background(), atLimit); err != nil {
		t.Fatalf("200-character risky reason rejected: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), atLimit)
	})
}

func TestSecurityCRD_ClusterControlPlaneNamespaceDefault(t *testing.T) {
	const name = "sec-cp-ns-default"
	cluster := &gameplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: gameplanev1alpha1.ClusterSpec{
			DisplayName: "control plane default",
			KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{
				Name: "sec-cp-ns-kubeconfig",
				Key:  "kubeconfig",
			},
		},
	}
	if err := k8sClient.Create(context.Background(), cluster); err != nil {
		t.Fatalf("create cluster without controlPlaneNamespace: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cluster)
	})

	var got gameplanev1alpha1.Cluster
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &got); err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	if got.Spec.ControlPlaneNamespace != "gameplane" {
		t.Fatalf("controlPlaneNamespace default = %q, want %q", got.Spec.ControlPlaneNamespace, "gameplane")
	}
}

func TestSecurityCRD_ClusterControlPlaneNamespaceInvalidRejected(t *testing.T) {
	cluster := &gameplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sec-cp-ns-invalid"},
		Spec: gameplanev1alpha1.ClusterSpec{
			KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{
				Name: "sec-cp-ns-kubeconfig",
				Key:  "kubeconfig",
			},
			ControlPlaneNamespace: "Bad_Namespace",
		},
	}
	if err := k8sClient.Create(context.Background(), cluster); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for controlPlaneNamespace Bad_Namespace, got %v", err)
	}
}
