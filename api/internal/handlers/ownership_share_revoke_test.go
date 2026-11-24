package handlers

import (
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// A transfer to another owner revokes the server's active share links, so the
// previous owner's links stop resolving.
func TestOwnership_TransferRevokesShareLinks(t *testing.T) {
	store := newTestStore(t)
	uid := seedUser(t, store, "bob", "viewer", "")
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)

	raw, _, err := store.CreateShareLink(t.Context(), scope.DefaultCluster, "gameplane-games", "alpha", 1, true, nil)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}
	if _, err := store.LookupShareLink(t.Context(), raw); err != nil {
		t.Fatalf("link should resolve before the transfer: %v", err)
	}

	rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{"userId": uid}, testAdminUser())
	if rr.Code != 204 {
		t.Fatalf("transfer: got %d %s", rr.Code, rr.Body)
	}
	if _, err := store.LookupShareLink(t.Context(), raw); err == nil {
		t.Fatal("link still resolves after the server changed owner")
	}
}
