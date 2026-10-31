package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactServerCannotStartCaptureAndBindsResult(t *testing.T) {
	dir := t.TempDir()
	s := NewServer(t.Context(), dir, 0, "server-uid")
	routes := s.ArtifactRoutes(nil)
	if err := os.WriteFile(filepath.Join(dir, "capture-cap-one.result.json"), []byte(`{"serverUID":"server-uid","captureUID":"capture-uid","captureId":"cap-one","status":"completed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path, uid string
		want              int
	}{{http.MethodPost, "/captures/cap-one/start", "capture-uid", http.StatusNotFound}, {http.MethodGet, "/captures/cap-one/status", "other-uid", http.StatusNotFound}, {http.MethodGet, "/captures/cap-one/status", "capture-uid", http.StatusOK}} {
		req := httptest.NewRequestWithContext(t.Context(), test.method, test.path, nil)
		req.Header.Set("X-Gameplane-Server-UID", "server-uid")
		req.Header.Set("X-Gameplane-Capture-UID", test.uid)
		rr := httptest.NewRecorder()
		routes.ServeHTTP(rr, req)
		if rr.Code != test.want {
			t.Fatalf("%s %s: %d %s", test.method, test.path, rr.Code, rr.Body)
		}
	}
}
