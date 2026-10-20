package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func mountModIDsRouter(k *kube.Client) http.Handler {
	r := chi.NewRouter()
	MountModIDs(r, k)
	return r
}

// newIDListTemplateObj builds a GameTemplate declaring
// capabilities.mods.idList — the shape ARK/Project-Zomboid-style modules
// use (mutually exclusive with the file-drop path/loaders shape in
// practice, per ModsSpec's doc comment).
func newIDListTemplateObj(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"game":     "ark-survival-ascended",
			"versions": []any{map[string]any{"id": "v", "default": true}},
			"capabilities": map[string]any{
				"mods": map[string]any{
					"idList": map[string]any{
						"env":  "ASA_START_PARAMS",
						"mode": "append",
					},
				},
			},
		},
	}}
}

func newModIDsServerObj(ns, name, tmpl string, ids []any) *unstructured.Unstructured {
	spec := map[string]any{
		"templateRef": map[string]any{"name": tmpl},
	}
	if ids != nil {
		spec["mods"] = map[string]any{"ids": ids}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       spec,
	}}
}

func TestModIDs_Get_ReturnsCurrentList(t *testing.T) {
	ids := []any{
		map[string]any{"id": "12345", "name": "Structures Plus"},
		map[string]any{"id": "98765"},
	}
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark", ids),
	)
	r := mountModIDsRouter(k)

	rr := do(t, r, "GET", "/servers/alpha/mods/ids", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var got []ModID
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []ModID{{ID: "12345", Name: "Structures Plus"}, {ID: "98765"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestModIDs_Get_EmptyListWhenUnset(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark", nil),
	)
	r := mountModIDsRouter(k)

	rr := do(t, r, "GET", "/servers/alpha/mods/ids", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if rr.Body.String() != "[]\n" {
		t.Errorf("body = %q, want an empty JSON array, not null", rr.Body.String())
	}
}

// TestModIDs_Put_ReplacesWholeList is the core bulk-PUT contract: a PUT
// overwrites spec.mods.ids entirely in one write (no per-id add/remove —
// see MountModIDs' doc comment on why: every env write rolls the
// StatefulSet).
func TestModIDs_Put_ReplacesWholeList(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark",
			[]any{map[string]any{"id": "111"}}),
	)
	r := mountModIDsRouter(k)

	body := []ModID{{ID: "222", Name: "New Mod"}, {ID: "333"}}
	rr := do(t, r, "PUT", "/servers/alpha/mods/ids", body)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}

	gs, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").
		Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	list, _, _ := unstructured.NestedSlice(gs.Object, "spec", "mods", "ids")
	if len(list) != 2 {
		t.Fatalf("stored ids = %+v, want exactly the 2 replaced entries (old %q gone)", list, "111")
	}
	first := list[0].(map[string]any)
	if first["id"] != "222" || first["name"] != "New Mod" {
		t.Errorf("first = %+v", first)
	}
}

// TestModIDs_Put_EmptyBodyClears covers the "no ids" clearing path: an
// empty PUT wipes the server's selection (the operator then projects
// nothing — see modIDListEnv's doc comment).
func TestModIDs_Put_EmptyBodyClears(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark",
			[]any{map[string]any{"id": "111"}}),
	)
	r := mountModIDsRouter(k)

	rr := do(t, r, "PUT", "/servers/alpha/mods/ids", []ModID{})
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if rr.Body.String() != "[]\n" {
		t.Errorf("body = %q, want an empty JSON array", rr.Body.String())
	}
}

func TestModIDs_Put_InvalidID_400(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark", nil),
	)
	r := mountModIDsRouter(k)

	for _, tc := range []struct {
		name string
		body []ModID
	}{
		{"space", []ModID{{ID: "has space"}}},
		{"empty", []ModID{{ID: ""}}},
		{"too long", []ModID{{ID: strings.Repeat("a", 65)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, r, "PUT", "/servers/alpha/mods/ids", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400: %s", rr.Code, rr.Body)
			}
		})
	}
}

func TestModIDs_Put_TooManyIDs_400(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark", nil),
	)
	r := mountModIDsRouter(k)

	ids := make([]ModID, 201)
	for i := range ids {
		ids[i] = ModID{ID: "x"}
	}
	rr := do(t, r, "PUT", "/servers/alpha/mods/ids", ids)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", rr.Code, rr.Body)
	}
}

func TestModIDs_NoIDList_501(t *testing.T) {
	// A template with no idList block (e.g. a regular file-drop game) —
	// mirrors errNoRegistry's "this game doesn't support that" shape.
	versions := []any{map[string]any{"id": "v", "loader": "paper", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("minecraft", nil, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	r := mountModIDsRouter(k)

	rrGet := do(t, r, "GET", "/servers/alpha/mods/ids", nil)
	if rrGet.Code != http.StatusNotImplemented {
		t.Fatalf("GET got %d, want 501: %s", rrGet.Code, rrGet.Body)
	}
	rrPut := do(t, r, "PUT", "/servers/alpha/mods/ids", []ModID{{ID: "1"}})
	if rrPut.Code != http.StatusNotImplemented {
		t.Fatalf("PUT got %d, want 501: %s", rrPut.Code, rrPut.Body)
	}
}

func TestModIDs_NoTemplateRef_501(t *testing.T) {
	k := fakeKubeClient(newModIDsServerObj("gameplane-games", "alpha", "", nil))
	r := mountModIDsRouter(k)
	rr := do(t, r, "GET", "/servers/alpha/mods/ids", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d, want 501: %s", rr.Code, rr.Body)
	}
}

func TestModIDs_UnknownServer_404(t *testing.T) {
	k := fakeKubeClient()
	r := mountModIDsRouter(k)
	rr := do(t, r, "GET", "/servers/ghost/mods/ids", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rr.Code, rr.Body)
	}
}
