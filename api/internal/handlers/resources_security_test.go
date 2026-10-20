package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func doWithUser(t *testing.T, h http.Handler, method, path string, body any, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != nil {
		req = req.WithContext(auth.WithUser(req.Context(), user))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func testOperatorUser() *auth.User {
	return &auth.User{
		ID:       2,
		Username: "operator_bob",
		Role:     "operator",
		Perms: map[string]map[string]map[string]struct{}{
			scope.DefaultCluster: {
				"*": {
					"servers:read":  {},
					"servers:write": {},
				},
			},
		},
	}
}

func testAdminUser() *auth.User {
	return &auth.User{
		ID:       1,
		Username: "admin_alice",
		Role:     "admin",
		Perms: map[string]map[string]map[string]struct{}{
			scope.DefaultCluster: {
				"*": {"*": {}},
			},
		},
	}
}

func TestResources_Security_CaptureBypassBlocked(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountResourcesRouter(k)

	// Operator without captures:manage attempts to enable capture via generic PUT
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"capture": map[string]any{
				"enabled": true,
			},
		},
	}

	rr := doWithUser(t, r, "PUT", "/servers/alpha", body, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with capture: got %d %s, want 403", rr.Code, rr.Body)
	}

	// Admin is allowed to configure capture
	rr = doWithUser(t, r, "PUT", "/servers/alpha", body, testAdminUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("admin PUT with capture: got %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestResources_Security_ServiceAccountNameOverrideBlocked(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef":        map[string]any{"name": "minecraft"},
			"serviceAccountName": "privileged-controller-sa",
		},
	}

	// Operator cannot set serviceAccountName on PUT
	rr := doWithUser(t, r, "PUT", "/servers/alpha", body, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with serviceAccountName: got %d %s, want 403", rr.Code, rr.Body)
	}

	// Operator cannot set serviceAccountName on POST
	createBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "gamma", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef":        map[string]any{"name": "minecraft"},
			"serviceAccountName": "privileged-controller-sa",
		},
	}
	rr = doWithUser(t, r, "POST", "/servers/", createBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator POST with serviceAccountName: got %d %s, want 403", rr.Code, rr.Body)
	}

	// Admin can set serviceAccountName
	rr = doWithUser(t, r, "PUT", "/servers/alpha", body, testAdminUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("admin PUT with serviceAccountName: got %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestResources_Security_ImageOverrideBlocked(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"image":       "evil.registry.io/stealer:latest",
		},
	}

	// Operator cannot set image override on PUT
	rr := doWithUser(t, r, "PUT", "/servers/alpha", body, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with image: got %d %s, want 403", rr.Code, rr.Body)
	}

	// Operator cannot set image override on POST
	createBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "delta", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"image":       "evil.registry.io/stealer:latest",
		},
	}
	rr = doWithUser(t, r, "POST", "/servers/", createBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator POST with image: got %d %s, want 403", rr.Code, rr.Body)
	}

	// Admin can set image override
	rr = doWithUser(t, r, "PUT", "/servers/alpha", body, testAdminUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("admin PUT with image: got %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestResources_Security_SecretRefExfiltrationBlocked(t *testing.T) {
	// Secret belonging to another subsystem / restic credentials
	resticSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "restic-repo-backups",
				"namespace": "gameplane-games",
			},
			"data": map[string]any{
				"password": "super-secret-restic-password",
			},
		},
	}

	// Server-owned Secret with ownerReference to alpha
	alphaTunnelSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "alpha-tunnel-auth",
				"namespace": "gameplane-games",
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": "gameplane.local/v1alpha1",
						"kind":       "GameServer",
						"name":       "alpha",
						"uid":        "alpha-uid",
					},
				},
			},
			"data": map[string]any{
				"token": "safe-tunnel-token",
			},
		},
	}

	// 2. Foreign same-named Secret with name alpha-files but NO OwnerReference
	foreignSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "alpha-files",
				"namespace": "gameplane-games",
			},
			"data": map[string]any{
				"data": "evil-data",
			},
		},
	}

	// 3. Secret with user-settable label only, NO OwnerReference
	labeledSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "alpha-user-labeled",
				"namespace": "gameplane-games",
				"labels": map[string]any{
					"gameplane.local/server-name": "alpha",
				},
			},
			"data": map[string]any{
				"data": "evil-data",
			},
		},
	}

	// 4. Secret with stale/mismatched OwnerReference UID
	staleSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "alpha-stale",
				"namespace": "gameplane-games",
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": "gameplane.local/v1alpha1",
						"kind":       "GameServer",
						"name":       "alpha",
						"uid":        "old-deleted-uid",
					},
				},
			},
			"data": map[string]any{
				"data": "evil-data",
			},
		},
	}

	server := newServerObj("gameplane-games", "alpha")
	server.SetUID("alpha-uid")

	k := fakeKubeClient(server, resticSecret, alphaTunnelSecret, foreignSecret, labeledSecret, staleSecret)
	r := mountResourcesRouter(k)

	// 1. Operator attempts to reference restic-repo-backups (unowned secret) -> 403
	evilBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"env": []any{
				map[string]any{
					"name": "EXFIL_RESTIC_PW",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{
							"name": "restic-repo-backups",
							"key":  "password",
						},
					},
				},
			},
		},
	}
	rr := doWithUser(t, r, "PUT", "/servers/alpha", evilBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with unowned secret ref: got %d %s, want 403", rr.Code, rr.Body)
	}

	// 2. Operator attempts to reference foreign same-named secret without OwnerReference -> 403
	foreignBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"env": []any{
				map[string]any{
					"name": "FOREIGN_SECRET",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{
							"name": "alpha-files",
							"key":  "data",
						},
					},
				},
			},
		},
	}
	rr = doWithUser(t, r, "PUT", "/servers/alpha", foreignBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with foreign same-named secret ref: got %d %s, want 403", rr.Code, rr.Body)
	}

	// 3. Operator attempts to reference secret with user-settable label without OwnerReference -> 403
	labeledBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"env": []any{
				map[string]any{
					"name": "LABELED_SECRET",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{
							"name": "alpha-user-labeled",
							"key":  "data",
						},
					},
				},
			},
		},
	}
	rr = doWithUser(t, r, "PUT", "/servers/alpha", labeledBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with labeled secret ref: got %d %s, want 403", rr.Code, rr.Body)
	}

	// 4. Operator attempts to reference secret with mismatched UID -> 403
	staleBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"env": []any{
				map[string]any{
					"name": "STALE_SECRET",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{
							"name": "alpha-stale",
							"key":  "data",
						},
					},
				},
			},
		},
	}
	rr = doWithUser(t, r, "PUT", "/servers/alpha", staleBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with stale secret ref: got %d %s, want 403", rr.Code, rr.Body)
	}

	// 5. Operator sets a server-owned Secret with matching UID -> 200 OK
	safeSecretBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"env": []any{
				map[string]any{
					"name": "TUNNEL_AUTH",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{
							"name": "alpha-tunnel-auth",
							"key":  "token",
						},
					},
				},
			},
		},
	}
	rr = doWithUser(t, r, "PUT", "/servers/alpha", safeSecretBody, testOperatorUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("operator PUT with server-owned secret ref: got %d %s, want 200", rr.Code, rr.Body)
	}

	// 3. Operator sets literal environment variable -> 200 OK
	literalBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"env": []any{
				map[string]any{
					"name":  "EULA",
					"value": "TRUE",
				},
			},
		},
	}
	rr = doWithUser(t, r, "PUT", "/servers/alpha", literalBody, testOperatorUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("operator PUT with literal env: got %d %s, want 200", rr.Code, rr.Body)
	}

	// 4. Admin is also refused when setting unowned secret reference -> 403
	rr = doWithUser(t, r, "PUT", "/servers/alpha", evilBody, testAdminUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("admin PUT with arbitrary secret ref: got %d %s, want 403", rr.Code, rr.Body)
	}
}

func TestResources_Security_TunnelCredentialsSecretProtected(t *testing.T) {
	serverAlpha := newServerObj("gameplane-games", "alpha")
	serverAlpha.SetUID("alpha-uid")

	resticSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "restic-repo-backups",
				"namespace": "gameplane-games",
			},
		},
	}

	ownedTunnelSecret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "alpha-tunnel-auth",
				"namespace": "gameplane-games",
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": "gameplane.local/v1alpha1",
						"kind":       "GameServer",
						"name":       "alpha",
						"uid":        "alpha-uid",
					},
				},
			},
		},
	}

	k := fakeKubeClient(serverAlpha, resticSecret, ownedTunnelSecret)
	r := mountResourcesRouter(k)

	// 1. Operator attempts to set unowned Secret reference in spec.networking.tunnel -> 403 Forbidden
	evilBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"networking": map[string]any{
				"tunnel": map[string]any{
					"credentialsSecretRef": map[string]any{
						"name": "restic-repo-backups",
					},
				},
			},
		},
	}
	rr := doWithUser(t, r, "PUT", "/servers/alpha", evilBody, testOperatorUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator PUT with unowned tunnel secret: got %d %s, want 403", rr.Code, rr.Body)
	}

	// 2. Operator sets server-owned Secret reference -> 200 OK
	safeBody := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"networking": map[string]any{
				"tunnel": map[string]any{
					"credentialsSecretRef": map[string]any{
						"name": "alpha-tunnel-auth",
					},
				},
			},
		},
	}
	rr = doWithUser(t, r, "PUT", "/servers/alpha", safeBody, testOperatorUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("operator PUT with server-owned tunnel secret: got %d %s, want 200", rr.Code, rr.Body)
	}

	// 3. Admin is also refused when setting unowned tunnel secret reference -> 403
	rr = doWithUser(t, r, "PUT", "/servers/alpha", evilBody, testAdminUser())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("admin PUT with unowned tunnel secret: got %d %s, want 403", rr.Code, rr.Body)
	}
}
