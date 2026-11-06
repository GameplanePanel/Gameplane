package controller

import (
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func passwordRevisionFixture(t *testing.T) (*GameServerReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	t.Helper()
	r, gs, installed := revisionFixture(t)
	installed.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{{Name: "PASSWORD", Type: "password"}}
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	digest, err := r.saveTemplateSnapshot(t.Context(), gs, installed)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = digest
	gs.Spec.Config = map[string]string{"PASSWORD": "still-private"}
	gs.Spec.TemplateRollout.Policy = "Automatic"
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	return r, gs, installed
}

func TestTemplatePasswordDowngradeFailsBeforeValueValidation(t *testing.T) {
	for _, kind := range []string{"string", "int", "bool", "enum", "removed", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			r, gs, installed := passwordRevisionFixture(t)
			pin := gs.Annotations[effectiveTemplateAnnotation]
			installed.Spec.ConfigSchema[0].Type = kind
			if kind == "removed" {
				installed.Spec.ConfigSchema = nil
			}
			if kind == "duplicate" {
				installed.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{{Name: "PASSWORD", Type: "string"}, {Name: "PASSWORD", Type: "password"}}
			}
			if err := r.Update(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			status, err := json.Marshal(gs.Status)
			if err != nil {
				t.Fatal(err)
			}
			if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed || !strings.Contains(string(status), "would expose a stored password") || strings.Contains(string(status), "still-private") {
				t.Fatalf("unsafe admission status: %s", status)
			}
			if gs.Annotations[effectiveTemplateAnnotation] != pin {
				t.Fatal("unsafe revision changed the pin")
			}
			if _, held, err := lifecycleOwner(gs); err != nil || held {
				t.Fatalf("unsafe revision acquired lifecycle ownership: held=%t err=%v", held, err)
			}
		})
	}
}

func TestTemplatePasswordEditAfterAdmissionCannotCommitDowngrade(t *testing.T) {
	r, gs, installed := passwordRevisionFixture(t)
	pin := gs.Annotations[effectiveTemplateAnnotation]
	gs.Spec.Config["PASSWORD"] = ""
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	installed.Spec.ConfigSchema[0].Type = "int"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
		t.Fatalf("empty password should admit target: changed=%t err=%v", changed, err)
	}
	gs.Spec.Config["PASSWORD"] = "concurrent-secret"
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.reconcileTemplateRevision(t.Context(), gs); err == nil || !strings.Contains(err.Error(), "would expose a stored password") || strings.Contains(err.Error(), "concurrent-secret") {
		t.Fatalf("commit did not reject the concurrent secret safely: %v", err)
	}
	if gs.Annotations[effectiveTemplateAnnotation] != pin {
		t.Fatal("unsafe target committed")
	}
}

func TestClonePasswordClassificationProtectsInitialPin(t *testing.T) {
	for _, baseline := range []bool{false, true} {
		t.Run(map[bool]string{false: "admission", true: "admitted-baseline"}[baseline], func(t *testing.T) {
			r, gs, installed := passwordRevisionFixture(t)
			delete(gs.Annotations, effectiveTemplateAnnotation)
			gs.Annotations[protectedConfigKeysAnnotation] = `["PASSWORD"]`
			installed.Spec.ConfigSchema[0].Type = "int"
			if err := r.Update(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			var err error
			if baseline {
				revision, saveErr := r.saveTemplateSnapshot(t.Context(), gs, installed)
				if saveErr != nil {
					t.Fatal(saveErr)
				}
				err = r.initializeTemplateBaseline(t.Context(), gs, lifecycleOperation{lifecycleIdentity: lifecycleIdentity{Token: revision, Baseline: true}})
			} else {
				_, _, err = r.reconcileTemplateRevision(t.Context(), gs)
			}
			if err == nil || !strings.Contains(err.Error(), "would expose a stored password") || strings.Contains(err.Error(), "still-private") {
				t.Fatalf("initial pin accepted a password downgrade: %v", err)
			}
			if gs.Annotations[effectiveTemplateAnnotation] != "" {
				t.Fatal("unsafe initial pin committed")
			}
		})
	}
}

func TestClearedClonePasswordProtectionRetiresWithInitialPin(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[empty], func(t *testing.T) {
			r, gs, installed := passwordRevisionFixture(t)
			delete(gs.Annotations, effectiveTemplateAnnotation)
			gs.Annotations[protectedConfigKeysAnnotation] = `["PASSWORD"]`
			delete(gs.Spec.Config, "PASSWORD")
			if empty {
				gs.Spec.Config["PASSWORD"] = ""
			}
			installed.Spec.ConfigSchema[0].Type = "string"
			if err := r.Update(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
					t.Fatalf("safe initial pin: changed=%t err=%v", changed, err)
				}
			}
			if gs.Annotations[effectiveTemplateAnnotation] == "" || gs.Annotations[protectedConfigKeysAnnotation] != "" {
				t.Fatalf("cleared protection retained after pin: %v", gs.Annotations)
			}
			// The API round-trip omits an empty config map. Recreate it before
			// simulating a later write to the newly public field.
			if gs.Spec.Config == nil {
				gs.Spec.Config = map[string]string{}
			}
			gs.Spec.Config["PASSWORD"] = "new-public-value"
			if err := validateTemplatePasswordTransition(gs, installed, installed); err != nil {
				t.Fatalf("retired protection blocks a later public value: %v", err)
			}
		})
	}
}

func TestMalformedProtectedConfigFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "null", `"PASSWORD"`, `[1]`, `[""]`} {
		gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{protectedConfigKeysAnnotation: raw}}}
		if err := validateTemplatePasswordTransition(gs, nil, &gameplanev1alpha1.GameTemplate{}); err == nil {
			t.Fatalf("accepted malformed protection %q", raw)
		}
	}
}
