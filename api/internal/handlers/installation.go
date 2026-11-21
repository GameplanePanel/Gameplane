package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// MountInstallation exposes capabilities only through the authenticated router.
func MountInstallation(r chi.Router, store *db.Store, standalone bool, version string) {
	r.Get("/admin/installation", func(w http.ResponseWriter, req *http.Request) {
		// The display name is optional: a config failure must not hide the
		// static capabilities that the rest of the dashboard depends on.
		name := "gameplane"
		raw, found, err := store.ConfigValue(req.Context(), "general")
		if err != nil {
			slog.Warn("installation name: read general config", "err", err)
		} else if found {
			var general generalCfg
			if err := json.Unmarshal([]byte(raw), &general); err != nil {
				slog.Warn("installation name: decode general config", "err", err)
			} else if configuredName := strings.TrimSpace(general.InstanceName); configuredName != "" {
				name = configuredName
			}
		}
		writeJSON(w, struct {
			Standalone   bool   `json:"standalone"`
			LocalCluster bool   `json:"localCluster"`
			Version      string `json:"version"`
			InstanceName string `json:"instanceName"`
		}{Standalone: standalone, LocalCluster: !standalone, Version: version, InstanceName: name})
	})
}
