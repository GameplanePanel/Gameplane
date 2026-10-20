//go:build envtest

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/GameplanePanel/gameplane/i18n/pack"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

// withModuleReconcilerRecorder is withModuleReconciler with an event recorder
// and an explicit namespace for the translation ConfigMap.
func withModuleReconcilerRecorder(fake *fakeOCI, ns string, rec *events.FakeRecorder) setupReconciler {
	return func(mgr manager.Manager) error {
		return (&ModuleReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			Namespace:     ns,
			NewFetcher:    fakeOCIFetcher(fake),
			EventRecorder: rec,
		}).SetupWithManager(mgr)
	}
}

// installLocaleModule publishes a bundle with the given extra files, creates
// the source and the Module, and returns the Module name.
func installLocaleModule(t *testing.T, fake *fakeOCI, extra map[string][]byte) string {
	t.Helper()
	const ref = "local/test/minecraft-java"
	bundle := fixtureBundle("minecraft-java", "1.0.0", "Minecraft (Java)")
	for name, data := range extra {
		bundle.files[name] = data
	}
	fake.putBundle(ref, "1.0.0", bundle)

	srcName := uniqueName("modsrc")
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "minecraft-java",
		Reference:     ref,
		Versions:      []string{"1.0.0"},
		LatestVersion: "1.0.0",
	}})
	modName := uniqueName("mod")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: srcName},
			Name:   "minecraft-java",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)
	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		return got.Status.Phase == gameplanev1alpha1.ModulePhaseReady, "phase=" + got.Status.Phase + " err=" + got.Status.LastError
	})
	return modName
}

func localesConfigMap(ns, modName string) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: modName + "-locales"}, cm)
	return cm, err
}

func templateLocalesCondition(t *testing.T, modName string) *metav1.Condition {
	t.Helper()
	tmpl := getTemplateByName(t, modName)
	return meta.FindStatusCondition(tmpl.Status.Conditions, gameplanev1alpha1.GameTemplateConditionLocalesValid)
}

// TestModule_PublishesValidLocalesAndReportsInvalidOnes covers a bundle with a
// French layer holding one valid and one invalid entry.
func TestModule_PublishesValidLocalesAndReportsInvalidOnes(t *testing.T) {
	ns := newNamespace(t)
	fake := newFakeOCI()
	rec := events.NewFakeRecorder(32)
	startMgr(t, ns, withModuleReconcilerRecorder(fake, ns, rec))

	modName := installLocaleModule(t, fake, map[string][]byte{
		"locales/fr.json": []byte(`{
			"template.displayName": {"defaultMessage": "Minecraft (Java FR)"},
			"module.displayName": {"defaultMessage": "{extra}"}
		}`),
	})

	var cm *corev1.ConfigMap
	eventually(t, func() (bool, string) {
		var err error
		cm, err = localesConfigMap(ns, modName)
		return err == nil, fmt.Sprint(err)
	})
	entries, err := pack.ParseEntries([]byte(cm.Data["fr.json"]))
	if err != nil {
		t.Fatalf("parse fr.json: %v", err)
	}
	if len(entries) != 1 || entries["template.displayName"].DefaultMessage != "Minecraft (Java FR)" {
		t.Errorf("fr.json entries = %v; want only the valid one", entries)
	}

	tmpl := getTemplateByName(t, modName)
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != tmpl.UID {
		t.Errorf("ownerReferences = %+v; want the GameTemplate %s", cm.OwnerReferences, tmpl.UID)
	}

	eventually(t, func() (bool, string) {
		c := templateLocalesCondition(t, modName)
		if c == nil {
			return false, "no LocalesValid condition"
		}
		return c.Status == metav1.ConditionFalse && c.Reason == "InvalidEntries", fmt.Sprintf("%+v", c)
	})

	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Warning") || !strings.Contains(ev, "fr:") {
			t.Errorf("event = %q", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no Warning event recorded")
	}
}

// TestModule_LocalesOverSizeBoundAreLeftOutInTagOrder checks the 900 KiB bound.
func TestModule_LocalesOverSizeBoundAreLeftOutInTagOrder(t *testing.T) {
	ns := newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, ns, withModuleReconcilerRecorder(fake, ns, events.NewFakeRecorder(32)))

	layer := []byte(fmt.Sprintf(`{"template.displayName":{"defaultMessage":%q}}`, strings.Repeat("x", 500*1024)))
	modName := installLocaleModule(t, fake, map[string][]byte{
		"locales/de.json": layer,
		"locales/fr.json": layer,
	})

	eventually(t, func() (bool, string) {
		c := templateLocalesCondition(t, modName)
		if c == nil {
			return false, "no LocalesValid condition"
		}
		return c.Reason == "LocalesTooLarge", fmt.Sprintf("%+v", c)
	})
	cm, err := localesConfigMap(ns, modName)
	if err != nil {
		t.Fatalf("ConfigMap: %v", err)
	}
	if _, ok := cm.Data["de.json"]; !ok || len(cm.Data) != 1 {
		t.Errorf("ConfigMap keys = %d; want only de.json", len(cm.Data))
	}
}

// TestModule_WithoutLocalesHasNoConfigMap checks a bundle with no locale
// layers: the Module is Ready and nothing is written.
func TestModule_WithoutLocalesHasNoConfigMap(t *testing.T) {
	ns := newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, ns, withModuleReconcilerRecorder(fake, ns, events.NewFakeRecorder(8)))

	modName := installLocaleModule(t, fake, nil)

	if _, err := localesConfigMap(ns, modName); !apierrors.IsNotFound(err) {
		t.Errorf("ConfigMap err = %v; want NotFound", err)
	}
	if c := templateLocalesCondition(t, modName); c != nil {
		t.Errorf("unexpected LocalesValid condition %+v", c)
	}
}

// TestModule_OversizedLocaleIsReportedByTag checks that a locale layer over the
// per-file cap reaches the reconciler as an oversized tag and shows up as
// LocalesTooLarge naming the language.
func TestModule_OversizedLocaleIsReportedByTag(t *testing.T) {
	ns := newNamespace(t)
	fake := newFakeOCI()
	rec := events.NewFakeRecorder(32)
	startMgr(t, ns, withModuleReconcilerRecorder(fake, ns, rec))

	modName := installLocaleModule(t, fake, map[string][]byte{
		"locales/de.json": oci.OversizedLocale,
	})

	eventually(t, func() (bool, string) {
		c := templateLocalesCondition(t, modName)
		if c == nil {
			return false, "no LocalesValid condition"
		}
		return c.Status == metav1.ConditionFalse && c.Reason == "LocalesTooLarge" && strings.Contains(c.Message, "de"), fmt.Sprintf("%+v", c)
	})
	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Warning") || !strings.Contains(ev, "de") {
			t.Errorf("event = %q", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no Warning event recorded")
	}
}
