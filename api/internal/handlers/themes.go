package handlers

import (
	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
)

// MountThemes registers the theme catalog under /themes (spec 043). The catalog
// is open to any signed-in user; the admin source routes under /themes/sources
// are guarded by the themes:manage rule in the RBAC table. The per-user routes
// under /users/me/themes are mounted by MountInstalledThemes. chi matches the
// static "me/themes" segment before the /users catch-all, so the two subtrees do
// not overlap.
func MountThemes(r chi.Router, store *db.Store, svc *themes.Service) {
	catalog := &themeCatalogHandler{store: store}
	sources := &themeSourceHandler{store: store, svc: svc}
	r.Route("/themes", func(r chi.Router) {
		r.Get("/catalog", catalog.list)
		r.Get("/catalog/{id}", catalog.get)
		r.Get("/catalog/{id}/preview/{mode}", catalog.preview)
		r.Route("/sources", func(r chi.Router) {
			r.Get("/", sources.list)
			r.Post("/", sources.create)
			r.Put("/{name}", sources.update)
			r.Delete("/{name}", sources.remove)
			r.Post("/{name}/refresh", sources.refresh)
		})
	})
}
