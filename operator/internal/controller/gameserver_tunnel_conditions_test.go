package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// F-055: the TunnelHostnameIgnored informational condition must follow the
// current spec, like every other condition, instead of staying True once
// set even after the setting it warns about no longer applies.

func tunnelReadyDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{Status: appsv1.DeploymentStatus{ReadyReplicas: 1}}
}

func TestComputeTunnelConditions_HostnameIgnoredClearedWhenHostnameRemoved(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Status: gameplanev1alpha1.GameServerStatus{
			Conditions: []metav1.Condition{{
				Type:               "TunnelHostnameIgnored",
				Status:             metav1.ConditionTrue,
				Reason:             "SettingIgnored",
				Message:            "hostname apply to the backing Service, not tunnel traffic",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	// Hostname cleared; the tunnel stays enabled and ready via frp.
	gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
		Provider: "frp",
		Frp:      &gameplanev1alpha1.FrpTunnelSpec{},
	}

	plan := tunnelPlan{wantTunnel: true}
	conds := computeTunnelConditions(gs, plan, tunnelReadyDeployment())

	if cond := meta.FindStatusCondition(conds, "TunnelHostnameIgnored"); cond != nil {
		t.Errorf("TunnelHostnameIgnored = %+v, want cleared once hostname is unset", cond)
	}
}

func TestComputeTunnelConditions_HostnameIgnoredClearedWhenTunnelDisabled(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Status: gameplanev1alpha1.GameServerStatus{
			Conditions: []metav1.Condition{{
				Type:               "TunnelHostnameIgnored",
				Status:             metav1.ConditionTrue,
				Reason:             "SettingIgnored",
				Message:            "hostname apply to the backing Service, not tunnel traffic",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	gs.Spec.Networking.Hostname = "still-set.example.com"

	plan := tunnelPlan{wantTunnel: false}
	conds := computeTunnelConditions(gs, plan, nil)

	if cond := meta.FindStatusCondition(conds, "TunnelHostnameIgnored"); cond != nil {
		t.Errorf("TunnelHostnameIgnored = %+v, want cleared once the tunnel is disabled", cond)
	}
}

func TestComputeTunnelConditions_HostnameIgnoredSetWhileHostnameConfigured(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Spec.Networking.Hostname = "mc.example.com"
	gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
		Provider: "frp",
		Frp:      &gameplanev1alpha1.FrpTunnelSpec{},
	}

	plan := tunnelPlan{wantTunnel: true}
	conds := computeTunnelConditions(gs, plan, tunnelReadyDeployment())

	cond := meta.FindStatusCondition(conds, "TunnelHostnameIgnored")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("TunnelHostnameIgnored = %+v, want True while hostname is set", cond)
	}
}
