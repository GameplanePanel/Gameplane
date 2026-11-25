package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// Owners and collaborators of a server read its template without
// templates:read, so the dashboard can show the Mods, Modpacks and Console
// tabs they manage (#1063). Anyone else still needs templates:read.
func TestServerTemplate_OwnerAndCollaboratorWithoutTemplatesRead(t *testing.T) {
	k := fakeKubeClient(catalogWithoutPasswords())
	gs := serverWithConfig("alpha", plainConfig())
	gs.SetAnnotations(map[string]string{
		"gameplane.local/owner-id": "7",
		collaboratorsAnnotation:    "9, 42",
	})
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Create(t.Context(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := mountResourcesRouter(k)
	get := func(u *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(auth.WithUser(t.Context(), u), http.MethodGet, "/servers/alpha/template", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	for name, u := range map[string]*auth.User{
		"collaborator": {ID: 42},
		"owner":        {ID: 7},
	} {
		t.Run(name, func(t *testing.T) {
			rr := get(u)
			var got map[string]any
			if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &got) != nil {
				t.Fatalf("unexpected answer: %d %s", rr.Code, rr.Body)
			}
			md, _ := got["metadata"].(map[string]any)
			if md["name"] != catalogWithoutPasswords().GetName() {
				t.Fatalf("unexpected template: %v", got["metadata"])
			}
		})
	}

	t.Run("server reader without a server grant", func(t *testing.T) {
		u := inventoryUser("local", "*", "servers:read")
		u.ID = 100
		if rr := get(u); rr.Code != http.StatusForbidden {
			t.Fatalf("template read without templates:read or a server grant: %d %s", rr.Code, rr.Body)
		}
	})
}
