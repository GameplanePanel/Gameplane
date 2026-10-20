package rbac

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

func TestMiddleware_ViewerSetsOwnLanguage(t *testing.T) {
	called := false
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "PUT", "/users/me/language", nil)
	req = req.WithContext(auth.WithUser(req.Context(), clusterWide(RoleViewer, "servers:read")))
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !called {
		t.Fatalf("code=%d called=%v", rr.Code, called)
	}
}
