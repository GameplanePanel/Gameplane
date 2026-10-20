package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestInstallationReportsPanelVersionWithoutWorkloadCluster(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		t.Run(map[bool]string{false: "combined", true: "standalone"}[standalone], func(t *testing.T) {
			r := chi.NewRouter()
			MountInstallation(r, standalone, "v0.2.3-review")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/installation", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			var got struct {
				Standalone   bool   `json:"standalone"`
				LocalCluster bool   `json:"localCluster"`
				Version      string `json:"version"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Standalone != standalone || got.LocalCluster == standalone || got.Version != "v0.2.3-review" {
				t.Fatalf("unexpected installation metadata: %+v", got)
			}
		})
	}
}
