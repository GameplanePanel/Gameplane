package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func ociBody(name string) map[string]any {
	return map[string]any{
		"name": name,
		"type": "oci",
		"oci": map[string]any{
			"url":     "ghcr.io/gameplanepanel/gameplane-modules",
			"modules": []any{map[string]any{"name": "minecraft-java"}},
		},
	}
}

func TestSourceCreate(t *testing.T) {
	t.Run("oci happy path", func(t *testing.T) {
		r := mountModulesRouter(fakeKubeClient())
		rr := do(t, r, "POST", "/modules/sources", ociBody("upstream"))
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		var created unstructured.Unstructured
		if err := json.NewDecoder(rr.Body).Decode(&created.Object); err != nil {
			t.Fatalf("decode: %v", err)
		}
		typ, _, _ := unstructured.NestedString(created.Object, "spec", "type")
		url, _, _ := unstructured.NestedString(created.Object, "spec", "oci", "url")
		if typ != "oci" || url != "ghcr.io/gameplanepanel/gameplane-modules" {
			t.Fatalf("spec = %v", created.Object["spec"])
		}
	})

	t.Run("oci with keyed verify", func(t *testing.T) {
		r := mountModulesRouter(fakeKubeClient())
		body := ociBody("signed")
		body["verify"] = map[string]any{"key": map[string]any{"name": "cosign-pub"}}
		rr := do(t, r, "POST", "/modules/sources", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		var created unstructured.Unstructured
		_ = json.NewDecoder(rr.Body).Decode(&created.Object)
		name, _, _ := unstructured.NestedString(created.Object, "spec", "verify", "key", "name")
		if name != "cosign-pub" {
			t.Fatalf("spec.verify = %v", created.Object["spec"])
		}
	})

	t.Run("oci with keyless verify", func(t *testing.T) {
		r := mountModulesRouter(fakeKubeClient())
		body := ociBody("keyless")
		body["verify"] = map[string]any{"keyless": map[string]any{
			"issuer":   "https://token.actions.githubusercontent.com",
			"identity": "github.com/org/repo/.github/workflows/release.yml@refs/heads/main",
		}}
		rr := do(t, r, "POST", "/modules/sources", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		var created unstructured.Unstructured
		_ = json.NewDecoder(rr.Body).Decode(&created.Object)
		issuer, _, _ := unstructured.NestedString(created.Object, "spec", "verify", "keyless", "issuer")
		identity, _, _ := unstructured.NestedString(created.Object, "spec", "verify", "keyless", "identity")
		if issuer != "https://token.actions.githubusercontent.com" ||
			identity != "github.com/org/repo/.github/workflows/release.yml@refs/heads/main" {
			t.Fatalf("spec.verify = %v", created.Object["spec"])
		}
	})

	t.Run("git with options", func(t *testing.T) {
		r := mountModulesRouter(fakeKubeClient())
		rr := do(t, r, "POST", "/modules/sources", map[string]any{
			"name": "community",
			"type": "git",
			"git": map[string]any{
				"url":       "https://github.com/gameplane-gg/community-modules",
				"ref":       "stable",
				"subPath":   "modules",
				"secretRef": map[string]any{"name": "gh-creds"},
			},
			"allow":           []any{"minecraft-*"},
			"refreshInterval": "30m",
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		var created unstructured.Unstructured
		_ = json.NewDecoder(rr.Body).Decode(&created.Object)
		ref, _, _ := unstructured.NestedString(created.Object, "spec", "git", "ref")
		secret, _, _ := unstructured.NestedString(created.Object, "spec", "git", "secretRef", "name")
		allow, _, _ := unstructured.NestedStringSlice(created.Object, "spec", "allow")
		if ref != "stable" || secret != "gh-creds" || len(allow) != 1 {
			t.Fatalf("spec = %v", created.Object["spec"])
		}
	})

	t.Run("upload needs no config", func(t *testing.T) {
		r := mountModulesRouter(fakeKubeClient())
		rr := do(t, r, "POST", "/modules/sources", map[string]any{"name": "uploads", "type": "upload"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("validation failures", func(t *testing.T) {
		r := mountModulesRouter(fakeKubeClient())
		validOCI := map[string]any{"url": "r", "modules": []any{map[string]any{"name": "m"}}}
		cases := []map[string]any{
			{"name": "x", "type": "oci"},                                          // missing oci config
			{"name": "x", "type": "oci", "oci": map[string]any{"url": "r"}},       // no modules
			{"name": "x", "type": "git", "git": map[string]any{}},                 // no url
			{"name": "x", "type": "wat"},                                          // unknown type
			{"name": "BAD_NAME", "type": "upload"},                                // bad name
			{"name": "x", "type": "upload", "local": map[string]any{"path": "p"}}, // mismatched config
			// verify shape/type-gate rules (mirror the CRD CEL).
			{"name": "x", "type": "oci", "oci": validOCI, "verify": map[string]any{ // both key + keyless
				"key":     map[string]any{"name": "k"},
				"keyless": map[string]any{"issuer": "i", "identity": "d"},
			}},
			{"name": "x", "type": "oci", "oci": validOCI, "verify": map[string]any{}},                        // neither key nor keyless
			{"name": "x", "type": "oci", "oci": validOCI, "verify": map[string]any{"key": map[string]any{}}}, // key without name
			{"name": "x", "type": "oci", "oci": validOCI, "verify": map[string]any{ // keyless missing identity
				"keyless": map[string]any{"issuer": "i"},
			}},
			{"name": "x", "type": "git", "git": map[string]any{"url": "r"}, // verify not allowed on git
				"verify": map[string]any{"key": map[string]any{"name": "k"}}},
			{"name": "x", "type": "http", "http": map[string]any{"url": "r"}, // verify not allowed on http
				"verify": map[string]any{"key": map[string]any{"name": "k"}}},
		}
		for i, body := range cases {
			if rr := do(t, r, "POST", "/modules/sources", body); rr.Code != http.StatusBadRequest {
				t.Errorf("case %d: got %d %s", i, rr.Code, rr.Body)
			}
		}
	})
}

func TestSourceUpdate(t *testing.T) {
	k := fakeKubeClient(newSource("upstream", nil))
	r := mountModulesRouter(k)

	rr := do(t, r, "PUT", "/modules/sources/upstream", map[string]any{
		"type": "http",
		"http": map[string]any{"url": "https://example.com/mods.tar.gz"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var updated unstructured.Unstructured
	_ = json.NewDecoder(rr.Body).Decode(&updated.Object)
	url, _, _ := unstructured.NestedString(updated.Object, "spec", "http", "url")
	if url != "https://example.com/mods.tar.gz" {
		t.Fatalf("spec = %v", updated.Object["spec"])
	}

	if rr := do(t, r, "PUT", "/modules/sources/ghost", map[string]any{"type": "upload"}); rr.Code != http.StatusNotFound {
		t.Fatalf("missing source: got %d", rr.Code)
	}
	if rr := do(t, r, "PUT", "/modules/sources/upstream", map[string]any{"type": "git", "git": map[string]any{}}); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid spec: got %d", rr.Code)
	}
}

// TestSourceUpdateVerifyRoundTrip documents that PUT is whole-spec replace:
// a verify block survives an edit only because the client re-sends it (the
// API does not merge — rule 10). Re-sending verify keeps it; omitting it
// clears it.
func TestSourceUpdateVerifyRoundTrip(t *testing.T) {
	k := fakeKubeClient(newSource("upstream", nil))
	r := mountModulesRouter(k)

	withVerify := map[string]any{
		"type":   "oci",
		"oci":    map[string]any{"url": "ghcr.io/x", "modules": []any{map[string]any{"name": "m"}}},
		"verify": map[string]any{"key": map[string]any{"name": "cosign-pub"}},
	}
	rr := do(t, r, "PUT", "/modules/sources/upstream", withVerify)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var updated unstructured.Unstructured
	_ = json.NewDecoder(rr.Body).Decode(&updated.Object)
	if name, _, _ := unstructured.NestedString(updated.Object, "spec", "verify", "key", "name"); name != "cosign-pub" {
		t.Fatalf("verify not persisted: %v", updated.Object["spec"])
	}

	// Re-PUT without verify: replace semantics drop it (no server-side merge).
	rr = do(t, r, "PUT", "/modules/sources/upstream", map[string]any{
		"type": "oci",
		"oci":  map[string]any{"url": "ghcr.io/x", "modules": []any{map[string]any{"name": "m"}}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var cleared unstructured.Unstructured
	_ = json.NewDecoder(rr.Body).Decode(&cleared.Object)
	if _, found, _ := unstructured.NestedMap(cleared.Object, "spec", "verify"); found {
		t.Fatalf("verify should be gone after omitted PUT: %v", cleared.Object["spec"])
	}
}

func TestSourceDelete(t *testing.T) {
	t.Run("blocked while modules reference it", func(t *testing.T) {
		k := fakeKubeClient(
			newSource("upstream", nil),
			newModule("mc", map[string]any{"source": map[string]any{"name": "upstream"}, "name": "minecraft"}),
		)
		r := mountModulesRouter(k)
		rr := do(t, r, "DELETE", "/modules/sources/upstream", nil)
		if rr.Code != http.StatusConflict {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("deletes when unreferenced", func(t *testing.T) {
		k := fakeKubeClient(newSource("upstream", nil))
		r := mountModulesRouter(k)
		rr := do(t, r, "DELETE", "/modules/sources/upstream", nil)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})
}
