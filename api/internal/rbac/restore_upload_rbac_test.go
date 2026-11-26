package rbac

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

// TestMiddleware_RestoreUploadNeedsBackupsRestore proves POST /restores:upload
// is reachable with backups:restore and refused without it. The verb suffix
// must not fall through to the default deny for every user.
func TestMiddleware_RestoreUploadNeedsBackupsRestore(t *testing.T) {
	cases := []struct {
		name  string
		user  *auth.User
		want  int
		allow bool
	}{
		{"with backups:restore", clusterWide(RoleOperator, "backups:restore"), http.StatusNoContent, true},
		{"without backups:restore", clusterWide(RoleViewer, "backups:read"), http.StatusForbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/restores:upload", nil)
			req = req.WithContext(auth.WithUser(req.Context(), tc.user))
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("code=%d, want %d", rr.Code, tc.want)
			}
			if called != tc.allow {
				t.Fatalf("inner handler called=%v, want %v", called, tc.allow)
			}
		})
	}
}
