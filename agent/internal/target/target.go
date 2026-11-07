// Package target binds versioned agent routes to an immutable resource UID.
package target

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Guard rejects a route for any resource other than the one that started this
// agent (GameServer or Pod, according to the route). Legacy routes stay available
// for local callers during rolling upgrades.
func Guard(resourceUID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if resourceUID == "" || chi.URLParam(req, "uid") != resourceUID {
				http.NotFound(w, req)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}
