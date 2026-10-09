package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MountInstallation exposes capabilities only through the authenticated router.
func MountInstallation(r chi.Router, standalone bool) {
	r.Get("/admin/installation", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, struct {
			Standalone   bool `json:"standalone"`
			LocalCluster bool `json:"localCluster"`
		}{Standalone: standalone, LocalCluster: !standalone})
	})
}
