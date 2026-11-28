package handlers

import (
	"context"
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
		writeJSON(w, struct {
			Standalone   bool   `json:"standalone"`
			LocalCluster bool   `json:"localCluster"`
			Version      string `json:"version"`
			InstanceName string `json:"instanceName"`
		}{Standalone: standalone, LocalCluster: !standalone, Version: version, InstanceName: installationDisplayName(req.Context(), store)})
	})
}

// The optional display name is shared by the dashboard and valid public links.
// A configuration failure must leave both available with the default brand.
func installationDisplayName(ctx context.Context, store *db.Store) string {
	raw, found, err := store.ConfigValue(ctx, "general")
	if err != nil {
		slog.Warn("installation name: read general config", "err", err)
	} else if found {
		var general generalCfg
		if err := json.Unmarshal([]byte(raw), &general); err != nil {
			slog.Warn("installation name: decode general config", "err", err)
		} else if name := strings.TrimSpace(general.InstanceName); name != "" {
			return name
		}
	}
	return "gameplane"
}
