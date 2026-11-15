package main

import (
	"encoding/json"
	"reflect"
	"testing"

	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/mcp-server/internal/kube"
)

// securityRoundTripSpec and securityRoundTripStatus are the spec.security and
// status.security fixtures for TestGameServer_SecurityFieldsUnredacted. They
// cover every shape the GameServer security fields can take: an explicit
// uid override, a useImageDefault id override, and the reconciler's desired
// and applied read model with a pending flag.
func securityRoundTripSpec() map[string]any {
	return map[string]any{
		"runAsUser":  map[string]any{"value": int64(1000)},
		"runAsGroup": map[string]any{"useImageDefault": true},
		"fsGroup":    map[string]any{"value": int64(2000)},
	}
}

func securityRoundTripStatus() map[string]any {
	applied := map[string]any{
		"runAsUser":  map[string]any{"value": int64(1000), "source": "Override"},
		"runAsGroup": map[string]any{"source": "ImageDefaultOverride"},
		"fsGroup":    map[string]any{"value": int64(2000), "source": "Override"},
	}
	return map[string]any{
		"phase": "Running",
		"security": map[string]any{
			"desired": applied,
			"applied": applied,
			"pending": true,
		},
	}
}

// securityRoundTripClient builds a kube.Client whose only GameServer carries
// spec.security and status.security. It is separate from the shared
// testClientWithFixtures object, which stays untouched.
func securityRoundTripClient(t *testing.T) *kube.Client {
	t.Helper()
	scheme := kube.NewScheme()
	gs := newUnstructuredCRD("GameServer", "games", "secure-server", securityRoundTripStatus())
	gs.Object["spec"] = map[string]any{"security": securityRoundTripSpec()}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKindMap(), gs)
	return kube.NewFrom(k8sfake.NewSimpleClientset(), dyn, scheme)
}

// decodeJSONObject round-trips a value through JSON so that numeric fields
// compare as float64 on both sides, the way the tool's text output decodes.
func decodeJSONObject(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return out
}

// assertSecurityUnredacted checks that obj carries the same spec.security and
// status.security subtrees as the fixture, unchanged.
func assertSecurityUnredacted(t *testing.T, label string, obj map[string]any) {
	t.Helper()
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		t.Fatalf("%s: spec missing or not an object: %v", label, obj["spec"])
	}
	status, ok := obj["status"].(map[string]any)
	if !ok {
		t.Fatalf("%s: status missing or not an object: %v", label, obj["status"])
	}
	wantSpec := decodeJSONObject(t, securityRoundTripSpec())
	wantStatusSecurity := decodeJSONObject(t, securityRoundTripStatus()["security"])
	if !reflect.DeepEqual(spec["security"], any(wantSpec)) {
		t.Errorf("%s: spec.security = %v, want %v", label, spec["security"], wantSpec)
	}
	if !reflect.DeepEqual(status["security"], any(wantStatusSecurity)) {
		t.Errorf("%s: status.security = %v, want %v", label, status["security"], wantStatusSecurity)
	}
}

// TestGameServer_SecurityFieldsUnredacted asserts that the read-only generic
// tools return a GameServer's spec.security and status.security exactly as
// stored. The only transform in this server is managedFields stripping, so no
// security field may be dropped, masked or rewritten on the way out.
func TestGameServer_SecurityFieldsUnredacted(t *testing.T) {
	c := securityRoundTripClient(t)
	cs := connectInMemory(t, newMCPServer(c))

	t.Run("get_gameplane_resource", func(t *testing.T) {
		text := callToolText(t, cs, "get_gameplane_resource", map[string]any{
			"kind": "GameServer", "namespace": "games", "name": "secure-server",
		})
		var obj map[string]any
		if err := json.Unmarshal([]byte(text), &obj); err != nil {
			t.Fatalf("decode get output: %v (text %q)", err, text)
		}
		assertSecurityUnredacted(t, "get", obj)
	})

	t.Run("list_gameplane_resources", func(t *testing.T) {
		text := callToolText(t, cs, "list_gameplane_resources", map[string]any{
			"kind": "GameServer", "namespace": "games",
		})
		var list struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &list); err != nil {
			t.Fatalf("decode list output: %v (text %q)", err, text)
		}
		if len(list.Items) != 1 {
			t.Fatalf("list: got %d items, want 1 (text %q)", len(list.Items), text)
		}
		assertSecurityUnredacted(t, "list", list.Items[0])
	})
}
