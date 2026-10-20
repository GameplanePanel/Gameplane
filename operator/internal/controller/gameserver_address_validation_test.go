package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// F-056: spec.networking.address is validated (netip.ParseAddr) before being
// handed to the address manager, and an invalid value is surfaced through
// the AddressAssignment condition instead of silently forwarded.

func TestIsValidAddress(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
	}{
		{"ipv4", "203.0.113.5", true},
		{"ipv6", "2001:db8::1", true},
		{"not-an-ip", "not-an-ip", false},
		{"empty", "", false},
		{"hostname", "example.com", false},
		{"cidr-not-address", "203.0.113.0/24", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidAddress(tc.addr); got != tc.want {
				t.Errorf("isValidAddress(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

func TestPlanAddressPreference_InvalidAddressCaughtBeforeManagerFlavor(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:  "LoadBalancer",
				Address: "not-an-ip",
			},
		},
	}

	plan := planAddressPreference(gs, addressManagerMetalLB)
	if plan.Outcome != addressPlanInvalidAddress {
		t.Fatalf("Outcome = %q, want %q", plan.Outcome, addressPlanInvalidAddress)
	}
}

func TestPlanAddressPreference_ValidAddressStillTranslated(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:  "LoadBalancer",
				Address: "203.0.113.5",
			},
		},
	}

	plan := planAddressPreference(gs, addressManagerMetalLB)
	if plan.Outcome != addressPlanTranslated {
		t.Fatalf("Outcome = %q, want %q", plan.Outcome, addressPlanTranslated)
	}
}

func TestPlanAddressPreference_InvalidAddressNeverReachesServiceAnnotations(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:  "LoadBalancer",
				Address: "not-an-ip",
			},
		},
	}
	plan := planAddressPreference(gs, addressManagerMetalLB)
	if annotations := plan.serviceAnnotations(); annotations != nil {
		t.Errorf("serviceAnnotations() = %v, want nil for an invalid address (must never reach the address manager)", annotations)
	}
}

func TestAddressAssignmentCondition_InvalidAddress(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			Networking: gameplanev1alpha1.GameServerNetworking{
				Expose:  "LoadBalancer",
				Address: "not-an-ip",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	plan := addressPlan{
		Manager: "metallb",
		Address: "not-an-ip",
		Outcome: addressPlanInvalidAddress,
	}
	conds := addressAssignmentCondition(nil, gs, plan, nil, addressFailureReason{}, "")
	if len(conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conds))
	}
	if conds[0].Reason != "InvalidAddress" {
		t.Errorf("Reason = %q, want InvalidAddress", conds[0].Reason)
	}
	if conds[0].Status != metav1.ConditionFalse {
		t.Errorf("Status = %q, want False", conds[0].Status)
	}
}
