//go:build envtest

package controller

import (
	"testing"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestTemplatePerformanceSchema(t *testing.T) {
	for _, tc := range []struct {
		signal, unit, threshold string
		valid                   bool
	}{
		{"tick_rate", "per_second", "18", true},
		{"tick_duration", "milliseconds", "50.5", true},
		{"frame_rate", "per_second", "0", true},
		{"seed", "per_second", "1", false},
		{"tick_rate", "seconds", "1", false},
		{"tick_duration", "per_second", "1", false},
		{"tick_rate", "per_second", "NaN", false},
		{"tick_rate", "per_second", "-1", false},
		{"tick_rate", "per_second", "1e999", false},
	} {
		t.Run(tc.signal+"-"+tc.unit+"-"+tc.threshold, func(t *testing.T) {
			tmpl := buildGameTemplate(uniqueName("performance"))
			tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 25575}
			if tmpl.Spec.Capabilities == nil {
				tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
			}
			threshold := tc.threshold
			tmpl.Spec.Capabilities.Status = &gameplanev1alpha1.StatusSpec{Metrics: []gameplanev1alpha1.StatusMetricSpec{{ID: "performance", DisplayName: "Performance", Command: "stats", Regex: `(?P<value>\S+)`, Performance: &gameplanev1alpha1.PerformanceMetricSpec{Signal: tc.signal, Unit: tc.unit, DegradedThreshold: &threshold}}}}
			err := k8sClient.Create(t.Context(), tmpl)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				deleteCleanup(t, tmpl)
			} else if !apierrors.IsInvalid(err) {
				t.Fatalf("wanted schema refusal, got %v", err)
			}
		})
	}
}
