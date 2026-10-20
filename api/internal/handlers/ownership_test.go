package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestStampOwner(t *testing.T) {
	obj := newServerObj("gameplane-games", "alpha")
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 7, Username: "alice"}))
	stampOwner(obj, req)
	ann := obj.GetAnnotations()
	if ann[ownerAnnotation] != "alice" || ann[ownerIDAnnotation] != "7" {
		t.Fatalf("owner annotations = %v", ann)
	}
}

func TestOwnership_Transfer(t *testing.T) {
	store := newTestStore(t)
	uid := seedUser(t, store, "bob", "viewer", "")
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)

	t.Run("rejects an unknown user", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{"userId": 999999}, testAdminUser())
		if rr.Code != 404 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("requires a userId", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{}, testAdminUser())
		if rr.Code != 400 {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("transfers to a valid user", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{"userId": uid}, testAdminUser())
		if rr.Code != 204 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		ann := obj.GetAnnotations()
		if ann[ownerAnnotation] != "bob" {
			t.Errorf("owner = %q, want bob", ann[ownerAnnotation])
		}
	})
}

func TestOwnership_SetCollaborators(t *testing.T) {
	store := newTestStore(t)
	alice := seedUser(t, store, "alice", "viewer", "")
	bob := seedUser(t, store, "bob", "viewer", "")
	charlie := seedUser(t, store, "charlie", "viewer", "")

	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)

	t.Run("unknown userId rejected", func(t *testing.T) {
		rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{999999}}, testAdminUser())
		if rr.Code != 400 {
			t.Fatalf("want 400 got %d", rr.Code)
		}
	})

	t.Run("unknown username rejected", func(t *testing.T) {
		rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"usernames": []string{"nobody"}}, testAdminUser())
		if rr.Code != 400 {
			t.Fatalf("want 400 got %d", rr.Code)
		}
	})

	t.Run("happy path with IDs", func(t *testing.T) {
		rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{bob}}, testAdminUser())
		if rr.Code != 204 {
			t.Fatalf("want 204 got %d body=%s", rr.Code, rr.Body)
		}
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		ann := obj.GetAnnotations()
		if ann[collaboratorsAnnotation] != strconv.FormatInt(bob, 10) {
			t.Errorf("collaborators = %q, want %d", ann[collaboratorsAnnotation], bob)
		}
		if ann[collaboratorNamesAnnotation] != "bob" {
			t.Errorf("collaborator-names = %q, want bob", ann[collaboratorNamesAnnotation])
		}
	})

	t.Run("replace semantics", func(t *testing.T) {
		// First, set to alice.
		rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{alice}}, testAdminUser())
		if rr.Code != 204 {
			t.Fatalf("first PUT: got %d", rr.Code)
		}
		// Then replace with charlie (alice should be gone).
		rr = doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{charlie}}, testAdminUser())
		if rr.Code != 204 {
			t.Fatalf("second PUT: got %d", rr.Code)
		}
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		ann := obj.GetAnnotations()
		if ann[collaboratorsAnnotation] != strconv.FormatInt(charlie, 10) {
			t.Errorf("collaborators = %q, want only %d (alice replaced by charlie)",
				ann[collaboratorsAnnotation], charlie)
		}
		if ann[collaboratorNamesAnnotation] != "charlie" {
			t.Errorf("collaborator-names = %q, want charlie", ann[collaboratorNamesAnnotation])
		}
	})

	t.Run("empty list clears", func(t *testing.T) {
		// Set some collaborators first.
		_ = doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{alice}}, testAdminUser())
		// Then clear.
		rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{}}, testAdminUser())
		if rr.Code != 204 {
			t.Fatalf("clear: got %d", rr.Code)
		}
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		ann := obj.GetAnnotations()
		if ann[collaboratorsAnnotation] != "" {
			t.Errorf("collaborators should be empty, got %q", ann[collaboratorsAnnotation])
		}
	})
}

func TestStampOwner_StripsCollaborators(t *testing.T) {
	obj := newServerObj("gameplane-games", "alpha")
	// Try to spoof collaborators in the request.
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[collaboratorsAnnotation] = "999,888"
	ann[collaboratorNamesAnnotation] = "evil,hacker"
	obj.SetAnnotations(ann)

	req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 7, Username: "alice"}))
	stampOwner(obj, req)

	stamped := obj.GetAnnotations()
	if stamped[collaboratorsAnnotation] != "" {
		t.Errorf("collaborators should be stripped, got %q", stamped[collaboratorsAnnotation])
	}
	if stamped[collaboratorNamesAnnotation] != "" {
		t.Errorf("collaborator-names should be stripped, got %q", stamped[collaboratorNamesAnnotation])
	}
	if stamped[ownerIDAnnotation] != "7" {
		t.Errorf("owner-id should be set to 7, got %q", stamped[ownerIDAnnotation])
	}
}

func TestOwnership_GetOwnedServers(t *testing.T) {
	store := newTestStore(t)
	alice := seedUser(t, store, "alice", "viewer", "")
	bob := seedUser(t, store, "bob", "viewer", "")
	charlie := seedUser(t, store, "charlie", "viewer", "")

	// Create servers with different ownership
	ownedByAlice := newServerObj("gameplane-games", "owned-by-alice")
	ownedByAlice.SetAnnotations(map[string]string{
		ownerIDAnnotation: strconv.FormatInt(alice, 10),
	})

	collaboratorOfBob := newServerObj("gameplane-games", "collab-of-bob")
	collaboratorOfBob.SetAnnotations(map[string]string{
		ownerIDAnnotation:       strconv.FormatInt(bob, 10),
		collaboratorsAnnotation: strconv.FormatInt(alice, 10),
	})

	notOwned := newServerObj("gameplane-games", "not-owned")
	notOwned.SetAnnotations(map[string]string{
		ownerIDAnnotation: strconv.FormatInt(charlie, 10),
	})

	k := fakeKubeClient(ownedByAlice, collaboratorOfBob, notOwned)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)

	t.Run("returns owned servers", func(t *testing.T) {
		rr := doAs(t, r, alice, "GET", "/users/me/servers", nil)
		if rr.Code != 200 {
			t.Fatalf("want 200 got %d: %s", rr.Code, rr.Body)
		}

		var list map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		items, ok := list["items"].([]interface{})
		if !ok {
			t.Fatalf("items not a list")
		}

		// Should have 2 items: the owned server and the collab server
		if len(items) != 2 {
			t.Errorf("want 2 items, got %d", len(items))
		}
	})

	t.Run("filters unrelated servers", func(t *testing.T) {
		rr := doAs(t, r, charlie, "GET", "/users/me/servers", nil)
		if rr.Code != 200 {
			t.Fatalf("want 200 got %d: %s", rr.Code, rr.Body)
		}

		var list map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		items, ok := list["items"].([]interface{})
		if !ok {
			t.Fatalf("items not a list")
		}

		// Charlie should only see the server they own (not-owned)
		if len(items) != 1 {
			t.Errorf("want 1 item, got %d", len(items))
		}
	})
}

// doAs is like do but with a specific user context
func doAs(t *testing.T, r http.Handler, userID int64, method, path string, body interface{}) *httptest.ResponseRecorder {
	var rb io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rb = bytes.NewReader(data)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, path, rb)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: userID}))
	r.ServeHTTP(rr, req)
	return rr
}
