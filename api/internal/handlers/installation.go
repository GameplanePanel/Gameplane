package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// MountInstallation exposes capabilities only through the authenticated router.
func MountInstallation(r chi.Router, store *db.Store, standalone bool, version string) {
	r.Get("/admin/installation", func(w http.ResponseWriter, req *http.Request) {
		raw, found, err := store.ConfigValue(req.Context(), "general")
		if err != nil {
			httperr.Write(w, req, fmt.Errorf("read installation name: %w", err))
			return
		}
		var general generalCfg
		if found {
			if err := json.Unmarshal([]byte(raw), &general); err != nil {
				httperr.Write(w, req, fmt.Errorf("decode installation name: %w", err))
				return
			}
		}
		name := strings.TrimSpace(general.InstanceName)
		if name == "" {
			name = "gameplane"
		}
		writeJSON(w, struct {
			Standalone   bool   `json:"standalone"`
			LocalCluster bool   `json:"localCluster"`
			Version      string `json:"version"`
			InstanceName string `json:"instanceName"`
		}{Standalone: standalone, LocalCluster: !standalone, Version: version, InstanceName: name})
	})
}
