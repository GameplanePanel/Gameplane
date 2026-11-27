package handlers

import (
	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// MountThemes registers the theme marketplace routers (spec 043): the catalog
// routes under /themes and the per-user routes under /users/me/themes. The
// catalog is read-only and open to any signed-in user; the source and install
// routes are added by their own tasks. The /users/me/themes prefix is mounted
// here rather than inside MountUsers so the existing MountUsers signature stays
// unchanged. chi matches the static "me/themes" segment before the /users
// catch-all, so the two subtrees do not overlap.
func MountThemes(r chi.Router, store *db.Store) {
	catalog := &themeCatalogHandler{store: store}
	r.Route("/themes", func(r chi.Router) {
		r.Get("/catalog", catalog.list)
		r.Get("/catalog/{id}", catalog.get)
		r.Get("/catalog/{id}/preview/{mode}", catalog.preview)
	})
	r.Route("/users/me/themes", func(r chi.Router) {})
}
