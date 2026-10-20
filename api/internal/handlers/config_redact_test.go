package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const redactNS = "gameplane-games"

// redactTemplate is a GameTemplate with one plain field and two password
// fields, one of them required.
func redactTemplate() *unstructured.Unstructured {
	tmpl := templateObj("minecraft", nil)
	tmpl.Object["spec"] = map[string]any{"configSchema": []any{
		map[string]any{"name": "MOTD", "type": "string"},
		map[string]any{"name": "RCON_PASSWORD", "type": "password"},
		map[string]any{"name": "ADMIN_PASSWORD", "type": "password", "required": true},
	}}
	return tmpl
}

func plainConfig() map[string]any {
	return map[string]any{"MOTD": "hello", "RCON_PASSWORD": "s3cret", "ADMIN_PASSWORD": "adm1n"}
}

func serverWithConfig(name string, cfg map[string]any) *unstructured.Unstructured {
	gs := newServerObj(redactNS, name)
	gs.Object["spec"].(map[string]any)["config"] = cfg
	return gs
}

func specConfig(t *testing.T, obj map[string]any) map[string]any {
	t.Helper()
	spec, _ := obj["spec"].(map[string]any)
	cfg, _ := spec["config"].(map[string]any)
	return cfg
}

func configFromBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return specConfig(t, obj)
}

func storedConfig(t *testing.T, k *kube.Client, name string) map[string]any {
	t.Helper()
	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get stored %s: %v", name, err)
	}
	cfg, _, _ := unstructured.NestedMap(obj.Object, "spec", "config")
	return cfg
}

func assertNoSecretLeak(t *testing.T, body string) {
	t.Helper()
	for _, secret := range []string{"s3cret", "adm1n", "n3w"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
}

func sameConfig(a, b map[string]any) bool {
	return (len(a) == 0 && len(b) == 0) || reflect.DeepEqual(a, b)
}

func TestRedact_GetAndListHidePasswordValues(t *testing.T) {
	k := fakeKubeClient(redactTemplate(), serverWithConfig("alpha", plainConfig()))
	r := mountResourcesRouter(k)
	want := map[string]any{"MOTD": "hello", "RCON_PASSWORD": configRedactedMarker, "ADMIN_PASSWORD": configRedactedMarker}

	rr := do(t, r, "GET", "/servers/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	if got := configFromBody(t, rr.Body.Bytes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("get config = %v, want %v", got, want)
	}

	rr = do(t, r, "GET", "/servers/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list decode: %v %s", err, rr.Body)
	}
	if got := specConfig(t, list.Items[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("list config = %v, want %v", got, want)
	}

	if got := storedConfig(t, k, "alpha"); !reflect.DeepEqual(got, plainConfig()) {
		t.Fatalf("stored config was modified: %v", got)
	}
}

func TestRedact_EmptyPasswordIsNotMasked(t *testing.T) {
	k := fakeKubeClient(redactTemplate(), serverWithConfig("alpha", map[string]any{"MOTD": "hi", "RCON_PASSWORD": ""}))
	rr := do(t, mountResourcesRouter(k), "GET", "/servers/alpha", nil)
	got := configFromBody(t, rr.Body.Bytes())
	if got["RCON_PASSWORD"] != "" || got["MOTD"] != "hi" {
		t.Fatalf("config = %v, want empty password left empty", got)
	}
}

func TestRedact_UnreadableTemplateFailsClosed(t *testing.T) {
	t.Run("template missing", func(t *testing.T) {
		k := fakeKubeClient(serverWithConfig("alpha", plainConfig()))
		rr := do(t, mountResourcesRouter(k), "GET", "/servers/alpha", nil)
		assertNoSecretLeak(t, rr.Body.String())
		for key, val := range configFromBody(t, rr.Body.Bytes()) {
			if val != configRedactedMarker {
				t.Fatalf("%s = %v, want marker", key, val)
			}
		}
	})
	t.Run("no templateRef", func(t *testing.T) {
		gs := serverWithConfig("alpha", plainConfig())
		delete(gs.Object["spec"].(map[string]any), "templateRef")
		k := fakeKubeClient(redactTemplate(), gs)
		rr := do(t, mountResourcesRouter(k), "GET", "/servers/alpha", nil)
		assertNoSecretLeak(t, rr.Body.String())
		if got := configFromBody(t, rr.Body.Bytes()); got["MOTD"] != configRedactedMarker {
			t.Fatalf("MOTD = %v, want marker", got["MOTD"])
		}
	})
}

func updateBody(cfg map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": redactNS},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}, "config": cfg},
	}
}

func TestRedact_UpdateRules(t *testing.T) {
	marker := configRedactedMarker
	cases := []struct {
		name         string
		withTemplate bool
		in           map[string]any
		want         map[string]any
	}{
		{
			name: "marker keeps stored values", withTemplate: true,
			in:   map[string]any{"MOTD": "new", "RCON_PASSWORD": marker, "ADMIN_PASSWORD": marker},
			want: map[string]any{"MOTD": "new", "RCON_PASSWORD": "s3cret", "ADMIN_PASSWORD": "adm1n"},
		},
		{
			name: "different value replaces", withTemplate: true,
			in:   map[string]any{"MOTD": "hello", "RCON_PASSWORD": "n3w", "ADMIN_PASSWORD": marker},
			want: map[string]any{"MOTD": "hello", "RCON_PASSWORD": "n3w", "ADMIN_PASSWORD": "adm1n"},
		},
		{
			name: "empty clears an optional field but not a required one", withTemplate: true,
			in:   map[string]any{"MOTD": "hello", "RCON_PASSWORD": "", "ADMIN_PASSWORD": ""},
			want: map[string]any{"MOTD": "hello", "RCON_PASSWORD": "", "ADMIN_PASSWORD": "adm1n"},
		},
		{
			name: "omitted required password is restored, omitted optional is removed", withTemplate: true,
			in:   map[string]any{"MOTD": "x"},
			want: map[string]any{"MOTD": "x", "ADMIN_PASSWORD": "adm1n"},
		},
		{
			name: "marker on a non-password key restores the stored value", withTemplate: true,
			in:   map[string]any{"MOTD": marker, "RCON_PASSWORD": marker, "ADMIN_PASSWORD": marker},
			want: map[string]any{"MOTD": "hello", "RCON_PASSWORD": "s3cret", "ADMIN_PASSWORD": "adm1n"},
		},
		{
			name: "unreadable template: markers keep every stored value", withTemplate: false,
			in:   map[string]any{"MOTD": marker, "RCON_PASSWORD": marker, "ADMIN_PASSWORD": marker},
			want: plainConfig(),
		},
		{
			name: "unreadable template: a new value still replaces and empty keeps", withTemplate: false,
			in:   map[string]any{"MOTD": marker, "RCON_PASSWORD": "n3w", "ADMIN_PASSWORD": ""},
			want: map[string]any{"MOTD": "hello", "RCON_PASSWORD": "n3w", "ADMIN_PASSWORD": "adm1n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := []runtime.Object{serverWithConfig("alpha", plainConfig())}
			if tc.withTemplate {
				objs = append(objs, redactTemplate())
			}
			k := fakeKubeClient(objs...)
			rr := do(t, mountResourcesRouter(k), "PUT", "/servers/alpha", updateBody(tc.in))
			if rr.Code != http.StatusOK {
				t.Fatalf("put: %d %s", rr.Code, rr.Body)
			}
			assertNoSecretLeak(t, rr.Body.String())
			if got := storedConfig(t, k, "alpha"); !sameConfig(got, tc.want) {
				t.Fatalf("stored config = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRedact_CreateDropsMarkerAndRedactsResponse(t *testing.T) {
	k := fakeKubeClient(redactTemplate())
	r := mountResourcesRouter(k)
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "beta"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"config":      map[string]any{"MOTD": "hi", "RCON_PASSWORD": "s3cret", "ADMIN_PASSWORD": configRedactedMarker},
		},
	}
	rr := do(t, r, "POST", "/servers/", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	if got := configFromBody(t, rr.Body.Bytes()); got["RCON_PASSWORD"] != configRedactedMarker {
		t.Fatalf("response RCON_PASSWORD = %v, want marker", got["RCON_PASSWORD"])
	}
	stored := storedConfig(t, k, "beta")
	if stored["RCON_PASSWORD"] != "s3cret" {
		t.Fatalf("stored RCON_PASSWORD = %v, want s3cret", stored["RCON_PASSWORD"])
	}
	if _, ok := stored["ADMIN_PASSWORD"]; ok {
		t.Fatalf("marker value was stored: %v", stored)
	}
}

func TestRedact_CloneResponseHidesPasswordsButCopiesStoredValues(t *testing.T) {
	k := fakeKubeClient(redactTemplate(), serverWithConfig("alpha", plainConfig()))
	r := mountLifecycleRouter(k)
	rr := do(t, r, "POST", "/servers/alpha:clone", map[string]any{"newName": "beta"})
	if rr.Code != http.StatusOK {
		t.Fatalf("clone: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	if got := storedConfig(t, k, "beta"); !reflect.DeepEqual(got, plainConfig()) {
		t.Fatalf("clone stored config = %v, want the source's values", got)
	}
}

func TestRedact_OwnedServersHidePasswordValues(t *testing.T) {
	gs := serverWithConfig("alpha", plainConfig())
	gs.SetAnnotations(map[string]string{ownerIDAnnotation: "7"})
	k := fakeKubeClient(redactTemplate(), gs)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, newTestStore(t))
	rr := doWithUser(t, r, "GET", "/users/me/servers", nil, &auth.User{ID: 7, Username: "alice"})
	if rr.Code != http.StatusOK {
		t.Fatalf("owned: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("owned decode: %v %s", err, rr.Body)
	}
	if got := specConfig(t, list.Items[0]); got["RCON_PASSWORD"] != configRedactedMarker || got["MOTD"] != "hello" {
		t.Fatalf("owned config = %v", got)
	}
}

func TestRedact_FleetServersHidePasswordValues(t *testing.T) {
	previous := scope.AllowedNamespaces
	scope.AllowedNamespaces = []string{scope.DefaultNamespace}
	t.Cleanup(func() { scope.AllowedNamespaces = previous })
	gs := fleetObject("GameServer", scope.DefaultNamespace, "alpha", "uid-a")
	gs.Object["spec"] = map[string]any{"templateRef": map[string]any{"name": "minecraft"}, "config": plainConfig()}
	reg := clusterTestRegistry(fleetTestClient(gs, redactTemplate()))
	u := inventoryUser("local", scope.DefaultNamespace, "servers:read")
	rr := inventoryRequest(t, fleetRouter(reg), u, http.MethodGet, "/fleet/servers")
	if rr.Code != http.StatusOK {
		t.Fatalf("fleet: %d %s", rr.Code, rr.Body)
	}
	assertNoSecretLeak(t, rr.Body.String())
	var result fleetResult[fleetResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatalf("fleet decode: %v %s", err, rr.Body)
	}
	if got := specConfig(t, result.Items[0].Resource.Object); got["RCON_PASSWORD"] != configRedactedMarker || got["MOTD"] != "hello" {
		t.Fatalf("fleet config = %v", got)
	}
}

// lockedBuffer is a Flusher ResponseWriter whose body can be read while the
// SSE handler is still writing.
type lockedBuffer struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
}

func (b *lockedBuffer) Header() http.Header { return b.header }
func (b *lockedBuffer) WriteHeader(_ int)   {}
func (b *lockedBuffer) Flush()              {}
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRedact_EventsStreamHidesPasswordValues(t *testing.T) {
	k := fleetTestClient(redactTemplate())
	fw := watch.NewFake()
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependWatchReactor("gameservers",
		func(_ clienttesting.Action) (bool, watch.Interface, error) { return true, fw, nil })
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)

	ctx, cancel := context.WithCancel(auth.WithUser(t.Context(), testAdminUser()))
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/events", nil)
	w := &lockedBuffer{header: http.Header{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		eventsHandler(reg)(w, req)
	}()

	gs := serverWithConfig("alpha", plainConfig())
	added := make(chan struct{})
	const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"
	gs.SetAnnotations(map[string]string{lastApplied: `{"spec":{"config":{"OLD_PASSWORD":"historical-secret"}}}`})
	go func() {
		fw.Add(gs)
		close(added)
	}()
	select {
	case <-added:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("events handler never started the gameservers watch")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(w.String(), "data:") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no SSE frame was written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	out := w.String()
	assertNoSecretLeak(t, out)
	if strings.Contains(out, lastApplied) || strings.Contains(out, "historical-secret") {
		t.Fatalf("event exposed last-applied configuration: %s", out)
	}
	if gs.GetAnnotations()[lastApplied] == "" {
		t.Fatal("watch object's last-applied annotation was mutated in place")
	}
	if !strings.Contains(out, configRedactedMarker) {
		t.Fatalf("frame has no redaction marker: %s", out)
	}
	if got, _, _ := unstructured.NestedString(gs.Object, "spec", "config", "RCON_PASSWORD"); got != "s3cret" {
		t.Fatalf("watch object was mutated in place: %q", got)
	}
}

func TestRedact_UpdateMarkerRestoresStoredValueOnAnyKey(t *testing.T) {
	k := fakeKubeClient(redactTemplate(), serverWithConfig("alpha", plainConfig()))
	in := map[string]any{"MOTD": configRedactedMarker, "RCON_PASSWORD": configRedactedMarker, "ADMIN_PASSWORD": configRedactedMarker}
	rr := do(t, mountResourcesRouter(k), "PUT", "/servers/alpha", updateBody(in))
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body)
	}
	if got := storedConfig(t, k, "alpha"); !sameConfig(got, plainConfig()) {
		t.Fatalf("stored config = %v, want %v", got, plainConfig())
	}
}

func TestRedact_UpdateMarkerForAbsentStoredKeyIsDropped(t *testing.T) {
	k := fakeKubeClient(redactTemplate(), serverWithConfig("alpha", plainConfig()))
	in := map[string]any{"MOTD": "hello", "EXTRA": configRedactedMarker, "RCON_PASSWORD": configRedactedMarker, "ADMIN_PASSWORD": configRedactedMarker}
	rr := do(t, mountResourcesRouter(k), "PUT", "/servers/alpha", updateBody(in))
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body)
	}
	got := storedConfig(t, k, "alpha")
	if _, present := got["EXTRA"]; present {
		t.Fatalf("marker for an absent key was stored: %v", got)
	}
	if !sameConfig(got, plainConfig()) {
		t.Fatalf("stored config = %v, want %v", got, plainConfig())
	}
}

func TestRedact_UpdateMarkerPreservesNonStringStoredValue(t *testing.T) {
	stored := map[string]any{
		"MOTD": "hello", "RCON_PASSWORD": "s3cret", "ADMIN_PASSWORD": "adm1n",
		"MAX_PLAYERS": int64(20), "OPS": []any{"a", "b"},
	}
	k := fakeKubeClient(redactTemplate(), serverWithConfig("alpha", stored))
	in := map[string]any{
		"MOTD": "hello", "RCON_PASSWORD": configRedactedMarker, "ADMIN_PASSWORD": configRedactedMarker,
		"MAX_PLAYERS": configRedactedMarker, "OPS": configRedactedMarker,
	}
	rr := do(t, mountResourcesRouter(k), "PUT", "/servers/alpha", updateBody(in))
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body)
	}
	if got := storedConfig(t, k, "alpha"); !sameConfig(got, stored) {
		t.Fatalf("stored config = %v, want %v", got, stored)
	}
}

// TestRedact_EventsStreamHonoursTemplateChangeBetweenEvents guards against a
// cache outliving one event: a field that becomes password-type after the first
// event must be redacted in the second.
func TestRedact_EventsStreamHonoursTemplateChangeBetweenEvents(t *testing.T) {
	tmpl := redactTemplate()
	tmpl.Object["spec"] = map[string]any{"configSchema": []any{
		map[string]any{"name": "TOKEN", "type": "string"},
	}}
	k := fleetTestClient(tmpl)
	fw := watch.NewFake()
	k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependWatchReactor("gameservers",
		func(_ clienttesting.Action) (bool, watch.Interface, error) { return true, fw, nil })
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)

	ctx, cancel := context.WithCancel(auth.WithUser(t.Context(), testAdminUser()))
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/events", nil)
	w := &lockedBuffer{header: http.Header{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		eventsHandler(reg)(w, req)
	}()

	waitFrames := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for strings.Count(w.String(), "data:") < n {
			if time.Now().After(deadline) {
				cancel()
				t.Fatalf("want %d SSE frames, got: %s", n, w.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	go fw.Add(serverWithConfig("alpha", map[string]any{"TOKEN": "tok-first"}))
	waitFrames(1)

	updated := tmpl.DeepCopy()
	updated.Object["spec"] = map[string]any{"configSchema": []any{
		map[string]any{"name": "TOKEN", "type": "password"},
	}}
	if _, err := k.Dynamic.Resource(kube.GVRs["templates"]).Update(t.Context(), updated, metav1.UpdateOptions{}); err != nil {
		cancel()
		t.Fatalf("update template: %v", err)
	}

	go fw.Modify(serverWithConfig("alpha", map[string]any{"TOKEN": "tok-second"}))
	waitFrames(2)
	cancel()
	<-done

	out := w.String()
	if strings.Contains(out, "tok-second") {
		t.Fatalf("second event leaked a value that became a password: %s", out)
	}
	if !strings.Contains(out, configRedactedMarker) {
		t.Fatalf("second event has no redaction marker: %s", out)
	}
}
