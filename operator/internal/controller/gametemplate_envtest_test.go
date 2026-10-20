//go:build envtest

package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestTemplate_InUseCountTracksServers — InUseCount reflects the
// number of GameServers that reference the template across
// create/delete events.
func TestTemplate_InUseCountTracksServers(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameTemplateReconciler())

	tmpl := buildGameTemplate(uniqueName("counted"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// 0 servers initially.
	eventually(t, func() (bool, string) {
		got := getTemplateInUseCount(t, tmpl.Name)
		return got == 0, "InUseCount = " + intToStr(got)
	})

	// Two servers using this template.
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp-a", tmpl.Name)); err != nil {
		t.Fatalf("create gs a: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp-b", tmpl.Name)); err != nil {
		t.Fatalf("create gs b: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getTemplateInUseCount(t, tmpl.Name)
		return got == 2, "InUseCount = " + intToStr(got)
	})

	// Delete one — InUseCount drops to 1.
	gs := getGameServer(t, ns, "smp-a")
	if err := k8sClient.Delete(context.Background(), gs); err != nil {
		t.Fatalf("delete gs a: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getTemplateInUseCount(t, tmpl.Name)
		return got == 1, "InUseCount = " + intToStr(got)
	})
}

// TestTemplate_NoChurnAtSteadyState — once InUseCount has converged,
// repeated reconciles do not bump the template's ResourceVersion.
func TestTemplate_NoChurnAtSteadyState(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameTemplateReconciler())

	tmpl := buildGameTemplate(uniqueName("steady"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "one", tmpl.Name)); err != nil {
		t.Fatalf("create gs: %v", err)
	}

	// Wait until InUseCount has converged on 1.
	eventually(t, func() (bool, string) {
		got := getTemplateInUseCount(t, tmpl.Name)
		return got == 1, "InUseCount = " + intToStr(got)
	})

	// Capture RV after a brief settling window, then assert it stays put.
	time.Sleep(300 * time.Millisecond)
	rv := getTemplateResourceVersion(t, tmpl.Name)

	consistently(t, time.Second, func() (bool, string) {
		got := getTemplateResourceVersion(t, tmpl.Name)
		if got != rv {
			return false, "RV bumped " + rv + " → " + got + " (controller is churning Status updates)"
		}
		return true, ""
	})
}

func getTemplateInUseCount(t *testing.T, name string) int32 {
	t.Helper()
	tmpl := getTemplateByName(t, name)
	return tmpl.Status.InUseCount
}

func getTemplateResourceVersion(t *testing.T, name string) string {
	t.Helper()
	tmpl := getTemplateByName(t, name)
	return tmpl.ResourceVersion
}

func TestGameTemplateCategoriesRoundTrip(t *testing.T) {
	ctx := context.Background()
	tmpl := buildGameTemplate("cat-roundtrip")
	tmpl.Spec.Categories = []string{"Sandbox", "Survival", "Building", "Modded", "Creative"}

	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, tmpl) })

	var got gameplanev1alpha1.GameTemplate
	key := client.ObjectKeyFromObject(tmpl)
	if err := k8sClient.Get(ctx, key, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	want := []string{"Sandbox", "Survival", "Building", "Modded", "Creative"}
	if !reflect.DeepEqual(got.Spec.Categories, want) {
		t.Errorf("categories = %v, want %v", got.Spec.Categories, want)
	}
}

func TestGameTemplateCategoriesRejectsTooMany(t *testing.T) {
	ctx := context.Background()
	tmpl := buildGameTemplate("cat-toomany")
	tmpl.Spec.Categories = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} // 9 > MaxItems=8

	err := k8sClient.Create(ctx, tmpl)
	if err == nil {
		_ = k8sClient.Delete(ctx, tmpl)
		t.Fatal("create with 9 categories succeeded, want apiserver rejection")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("err = %v, want Invalid", err)
	}
}

// TestGameTemplateActionCommandsOnlyApplies — an action using Commands
// (a sequence) with no Command set is accepted by the apiserver.
func TestGameTemplateActionCommandsOnlyApplies(t *testing.T) {
	ctx := context.Background()
	tmpl := buildGameTemplate(uniqueName("act-commands"))
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Actions: []gameplanev1alpha1.ServerActionSpec{{
			ID:          "save-cycle",
			DisplayName: "Save Cycle",
			Commands:    []string{"a", "b"},
		}},
	}

	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template with commands-only action: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, tmpl) })

	var got gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tmpl), &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	want := []string{"a", "b"}
	if len(got.Spec.Capabilities.Actions) != 1 || !reflect.DeepEqual(got.Spec.Capabilities.Actions[0].Commands, want) {
		t.Errorf("actions[0].commands = %v, want %v", got.Spec.Capabilities.Actions, want)
	}
}

// TestGameTemplateActionRejectsBothCommandAndCommands — an action
// declaring BOTH command and commands violates the struct's xor
// XValidation rule and is rejected.
func TestGameTemplateActionRejectsBothCommandAndCommands(t *testing.T) {
	ctx := context.Background()
	tmpl := buildGameTemplate(uniqueName("act-both"))
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Actions: []gameplanev1alpha1.ServerActionSpec{{
			ID:          "save-cycle",
			DisplayName: "Save Cycle",
			Command:     "save-all",
			Commands:    []string{"a", "b"},
		}},
	}

	err := k8sClient.Create(ctx, tmpl)
	if err == nil {
		_ = k8sClient.Delete(ctx, tmpl)
		t.Fatal("create with both command and commands succeeded, want apiserver rejection")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("err = %v, want Invalid", err)
	}
}

// TestGameTemplateActionRejectsNeitherCommandNorCommands — an action
// declaring NEITHER command nor commands also violates the xor rule.
func TestGameTemplateActionRejectsNeitherCommandNorCommands(t *testing.T) {
	ctx := context.Background()
	tmpl := buildGameTemplate(uniqueName("act-neither"))
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Actions: []gameplanev1alpha1.ServerActionSpec{{
			ID:          "save-cycle",
			DisplayName: "Save Cycle",
		}},
	}

	err := k8sClient.Create(ctx, tmpl)
	if err == nil {
		_ = k8sClient.Delete(ctx, tmpl)
		t.Fatal("create with neither command nor commands succeeded, want apiserver rejection")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("err = %v, want Invalid", err)
	}
}
