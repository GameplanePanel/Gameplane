package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Mirror the operator's immutable snapshot wire format, without fetching the
// current catalog template when preparing a pinned server fixture.
func pinRedactionTemplate(t *testing.T, k *kube.Client, gs, tmpl *unstructured.Unstructured) *corev1.ConfigMap {
	t.Helper()
	if gs.GetUID() == "" {
		gs.SetUID(types.UID("uid-" + gs.GetName()))
	}
	data, err := json.Marshal(map[string]any{"metadata": map[string]any{"name": tmpl.GetName()}, "spec": tmpl.Object["spec"]})
	if err != nil {
		t.Fatal(err)
	}
	revision := fmt.Sprintf("%x", sha256.Sum256(data))
	identity := sha256.Sum256([]byte(string(gs.GetUID()) + "/" + gs.GetNamespace() + "/" + gs.GetName()))
	immutable := true
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: fmt.Sprintf("gpt-%x-%s", identity[:8], revision[:32]), Namespace: gs.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: gs.GetName(), UID: gs.GetUID(), Controller: &immutable}},
	}, Immutable: &immutable, Data: map[string]string{"template.json": string(data)}}
	if _, err := k.Typed.CoreV1().ConfigMaps(gs.GetNamespace()).Create(t.Context(), cm, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	annotations := gs.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[kube.EffectiveTemplateRevisionAnnotation] = revision
	gs.SetAnnotations(annotations)
	return cm
}

func catalogWithoutPasswords() *unstructured.Unstructured {
	tmpl := redactTemplate()
	tmpl.Object["spec"] = map[string]any{"configSchema": []any{
		map[string]any{"name": "MOTD", "type": "string"},
		map[string]any{"name": "RCON_PASSWORD", "type": "string"},
	}}
	return tmpl
}

func TestRedact_PinnedTemplateSurvivesCatalogPasswordRemoval(t *testing.T) {
	k := fakeKubeClient(catalogWithoutPasswords())
	gs := serverWithConfig("alpha", plainConfig())
	pinRedactionTemplate(t, k, gs, redactTemplate())
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Create(t.Context(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := mountResourcesRouter(k)
	for _, path := range []string{"/servers/alpha", "/servers/"} {
		rr := do(t, router, http.MethodGet, path, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body)
		}
		assertNoSecretLeak(t, rr.Body.String())
		if !strings.Contains(rr.Body.String(), "hello") {
			t.Fatalf("%s failed closed despite a valid snapshot: %s", path, rr.Body)
		}
	}
	for _, incoming := range []map[string]any{
		{"MOTD": "hello", "RCON_PASSWORD": configRedactedMarker, "ADMIN_PASSWORD": ""},
		{"MOTD": "hello", "RCON_PASSWORD": configRedactedMarker},
	} {
		body := updateBody(incoming)
		body["metadata"].(map[string]any)["uid"] = string(gs.GetUID())
		rr := do(t, router, http.MethodPut, "/servers/alpha", body)
		if rr.Code != http.StatusOK {
			t.Fatalf("put: %d %s", rr.Code, rr.Body)
		}
		assertNoSecretLeak(t, rr.Body.String())
		if got := storedConfig(t, k, "alpha"); !reflect.DeepEqual(got, plainConfig()) {
			t.Fatalf("PUT lost the pinned template's required password: %v", got)
		}
	}
}

func TestRedact_PinnedTemplateCacheSeparatesServersAndRevisions(t *testing.T) {
	k := fakeKubeClient(catalogWithoutPasswords())
	old := serverWithConfig("old", plainConfig())
	pinRedactionTemplate(t, k, old, redactTemplate())
	current := serverWithConfig("current", plainConfig())
	pinRedactionTemplate(t, k, current, catalogWithoutPasswords())
	cache := newConfigRuleCache(k)
	cache.redact(t.Context(), current)
	cache.redact(t.Context(), old)
	if cfg := specConfig(t, old.Object); cfg["ADMIN_PASSWORD"] != configRedactedMarker || cfg["RCON_PASSWORD"] != configRedactedMarker {
		t.Fatalf("current schema escaped into the older server: %v", cfg)
	}
	// The same server adopting another revision within a request must use its
	// new schema, while a same-name replacement must re-prove snapshot ownership.
	next := serverWithConfig("current", plainConfig())
	next.SetUID(current.GetUID())
	pinRedactionTemplate(t, k, next, redactTemplate())
	cache.redact(t.Context(), next)
	if specConfig(t, next.Object)["RCON_PASSWORD"] != configRedactedMarker {
		t.Fatal("revision change reused the old cache entry")
	}
	replacement := current.DeepCopy()
	replacement.SetUID("replacement-uid")
	cache.redact(t.Context(), replacement)
	if specConfig(t, replacement.Object)["MOTD"] != configRedactedMarker {
		t.Fatal("replacement server reused another UID's verified snapshot")
	}
	otherNamespace := current.DeepCopy()
	otherNamespace.SetNamespace("another-namespace")
	cache.redact(t.Context(), otherNamespace)
	if specConfig(t, otherNamespace.Object)["MOTD"] != configRedactedMarker {
		t.Fatal("another namespace reused a verified snapshot")
	}
}

func TestRedact_InvalidPinnedTemplateFailsClosedWithoutCatalogFallback(t *testing.T) {
	for _, mode := range []string{"missing", "malformed-revision", "uppercase-revision", "missing-uid", "mutable", "foreign-owner", "wrong-owner-name", "wrong-owner-kind", "wrong-owner-api", "deleting", "corrupt-content", "wrong-template", "wrong-namespace", "malformed-schema"} {
		t.Run(mode, func(t *testing.T) {
			k := fakeKubeClient(catalogWithoutPasswords())
			gs := serverWithConfig("alpha", plainConfig())
			snapshotTemplate := redactTemplate()
			if mode == "wrong-template" {
				snapshotTemplate.SetName("other-template")
			}
			if mode == "malformed-schema" {
				snapshotTemplate.Object["spec"].(map[string]any)["configSchema"] = "invalid"
			}
			cm := pinRedactionTemplate(t, k, gs, snapshotTemplate)
			switch mode {
			case "missing":
				if err := k.Typed.CoreV1().ConfigMaps(cm.Namespace).Delete(t.Context(), cm.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
			case "malformed-revision":
				gs.SetAnnotations(map[string]string{kube.EffectiveTemplateRevisionAnnotation: "../invalid"})
			case "uppercase-revision":
				gs.SetAnnotations(map[string]string{kube.EffectiveTemplateRevisionAnnotation: strings.ToUpper(gs.GetAnnotations()[kube.EffectiveTemplateRevisionAnnotation])})
			case "missing-uid":
				gs.SetUID("")
			case "mutable":
				cm.Immutable = nil
			case "foreign-owner":
				cm.OwnerReferences[0].UID = "other-server"
			case "wrong-owner-name":
				cm.OwnerReferences[0].Name = "other-server"
			case "wrong-owner-kind":
				cm.OwnerReferences[0].Kind = "Backup"
			case "wrong-owner-api":
				cm.OwnerReferences[0].APIVersion = "foreign.example/v1alpha1"
			case "deleting":
				now := metav1.Now()
				cm.DeletionTimestamp = &now
			case "corrupt-content":
				cm.Data["template.json"] = `{"metadata":{"name":"minecraft"},"spec":{}}`
			case "wrong-namespace":
				gs.SetNamespace("another-namespace")
			}
			if mode != "missing" {
				if _, err := k.Typed.CoreV1().ConfigMaps(cm.Namespace).Update(t.Context(), cm, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			newConfigRuleCache(k).redact(t.Context(), gs)
			for key, value := range specConfig(t, gs.Object) {
				if value != configRedactedMarker {
					t.Fatalf("%s leaked despite invalid snapshot: %v", key, value)
				}
			}
			if len(k.Dynamic.(*dynamicfake.FakeDynamicClient).Actions()) != 0 {
				t.Fatal("invalid pin fell back to current catalog")
			}
		})
	}
}

func TestRedact_PinnedSnapshotFailureIsNotCached(t *testing.T) {
	k := fakeKubeClient(catalogWithoutPasswords())
	gs := serverWithConfig("alpha", plainConfig())
	cm := pinRedactionTemplate(t, k, gs, redactTemplate())
	if err := k.Typed.CoreV1().ConfigMaps(cm.Namespace).Delete(t.Context(), cm.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	cache := newConfigRuleCache(k)
	missing := gs.DeepCopy()
	cache.redact(t.Context(), missing)
	if specConfig(t, missing.Object)["MOTD"] != configRedactedMarker {
		t.Fatal("missing snapshot did not fail closed")
	}
	if _, err := k.Typed.CoreV1().ConfigMaps(cm.Namespace).Create(t.Context(), cm, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	cache.redact(t.Context(), gs)
	if cfg := specConfig(t, gs.Object); cfg["MOTD"] != "hello" || cfg["RCON_PASSWORD"] != configRedactedMarker {
		t.Fatalf("recovered snapshot did not supply its schema: %v", cfg)
	}
}

func TestRedact_PinnedCloneRejectsPasswordDowngradeBeforeCreate(t *testing.T) {
	for _, mode := range []string{"removed", "retyped", "missing-target", "missing-source", "unchanged-passwords", "empty-passwords"} {
		t.Run(mode, func(t *testing.T) {
			catalog := redactTemplate()
			fields := catalog.Object["spec"].(map[string]any)["configSchema"].([]any)
			switch mode {
			case "removed":
				catalog.Object["spec"].(map[string]any)["configSchema"] = fields[:2]
			case "retyped":
				fields[1].(map[string]any)["type"] = "string"
			case "empty-passwords":
				catalog = catalogWithoutPasswords()
			}
			k := fakeKubeClient(catalog)
			gs := serverWithConfig("alpha", plainConfig())
			if mode == "empty-passwords" {
				gs.Object["spec"].(map[string]any)["config"] = map[string]any{"MOTD": "hello", "RCON_PASSWORD": "", "ADMIN_PASSWORD": ""}
			}
			cm := pinRedactionTemplate(t, k, gs, redactTemplate())
			if mode == "missing-target" {
				if err := k.Dynamic.Resource(kube.GVRs["templates"]).Delete(t.Context(), catalog.GetName(), metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-source" {
				if err := k.Typed.CoreV1().ConfigMaps(cm.Namespace).Delete(t.Context(), cm.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Create(t.Context(), gs, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			rr := do(t, mountLifecycleRouter(k), http.MethodPost, "/servers/alpha:clone", map[string]any{"newName": "beta"})
			assertNoSecretLeak(t, rr.Body.String())
			if mode == "unchanged-passwords" || mode == "empty-passwords" {
				if rr.Code != http.StatusOK {
					t.Fatalf("safe clone: %d %s", rr.Code, rr.Body)
				}
				if !reflect.DeepEqual(storedConfig(t, k, "beta"), specConfig(t, gs.Object)) {
					t.Fatal("safe clone changed config")
				}
				return
			}
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "current template") {
				t.Fatalf("unsafe clone: %d %s", rr.Code, rr.Body)
			}
			if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Get(t.Context(), "beta", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("rejected clone persisted: %v", err)
			}
		})
	}
}

func TestRedact_PinnedFleetAndEventsUseSelectedClusterSnapshot(t *testing.T) {
	previous := scope.AllowedNamespaces
	scope.AllowedNamespaces = []string{scope.DefaultNamespace}
	t.Cleanup(func() { scope.AllowedNamespaces = previous })
	k := fleetTestClient(catalogWithoutPasswords())
	gs := serverWithConfig("alpha", plainConfig())
	gs.SetNamespace(scope.DefaultNamespace)
	pinRedactionTemplate(t, k, gs, redactTemplate())
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(gs.GetNamespace()).Create(t.Context(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	home := fleetTestClient(catalogWithoutPasswords())
	reg := clusterTestRegistry(home)
	reg.Set("remote", k)
	rr := inventoryRequest(t, fleetRouter(reg), inventoryUser("remote", gs.GetNamespace(), "servers:read"), http.MethodGet, "/fleet/servers?cluster=remote")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "hello") {
		t.Fatalf("fleet: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	if len(home.Typed.(*kubefake.Clientset).Actions()) != 0 {
		t.Fatal("remote redaction consulted the home cluster")
	}
	fw := watch.NewFake()
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependWatchReactor("gameservers", func(clienttesting.Action) (bool, watch.Interface, error) { return true, fw, nil })
	ctx, cancel := context.WithCancel(auth.WithUser(t.Context(), inventoryUser("remote", gs.GetNamespace(), "servers:read")))
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/events?cluster=remote", nil)
	w := &lockedBuffer{header: http.Header{}}
	done := make(chan struct{})
	go func() { defer close(done); eventsHandler(reg)(w, req) }()
	go fw.Add(gs)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(w.String(), "data:") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if out := w.String(); !strings.Contains(out, "data:") || !strings.Contains(out, "hello") {
		t.Fatalf("missing pinned event: %s", out)
	}
	assertNoSecretLeak(t, w.String())
	if specConfig(t, gs.Object)["ADMIN_PASSWORD"] != "adm1n" {
		t.Fatal("SSE redacted the shared watch object in place")
	}
}
