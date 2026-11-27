package handlers

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidServerName(t *testing.T) {
	for _, name := range []string{"sdtd", "mc-1", strings.Repeat("a", 57)} {
		if !validServerName(name) {
			t.Errorf("validServerName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "7d2d", "my.server", "MC", strings.Repeat("a", 58)} {
		if validServerName(name) {
			t.Errorf("validServerName(%q) = true, want false", name)
		}
	}
}

func TestCreateAndCloneRejectUnusableServerNames(t *testing.T) {
	k := fakeKubeClient(ownedServerObj("alpha", nil))
	router := annotationRouter(k)

	body := ownedServerObj("7d2d", nil)
	if rr := doWithUser(t, router, http.MethodPost, "/servers/", body.Object, testAdminUser()); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create 7d2d: %d %s, want 422", rr.Code, rr.Body)
	}
	if rr := doWithUser(t, router, http.MethodPost, "/servers/alpha:clone", map[string]any{"newName": "7d2d"}, testAdminUser()); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("clone to 7d2d: %d %s, want 422", rr.Code, rr.Body)
	}
	if _, err := k.GetServer(t.Context(), "gameplane-games", "7d2d"); err == nil {
		t.Fatal("a server named 7d2d was created")
	}
}
