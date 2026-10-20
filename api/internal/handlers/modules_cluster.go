package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/go-chi/chi/v5"
)

// MountModulesWithRegistry retains central module ownership in combined installs.
// Without a local cluster the caller must select the operator that owns modules.
func MountModulesWithRegistry(r chi.Router, reg *kube.Registry, namespace string) {
	if reg.Default() != nil {
		r.Group(func(local chi.Router) {
			local.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if rejectRemoteCluster(w, req) {
						return
					}
					next.ServeHTTP(w, req)
				})
			})
			MountModules(local, reg.Default(), namespace)
		})
		return
	}
	handle := func(w http.ResponseWriter, req *http.Request) {
		id := strings.TrimSpace(req.URL.Query().Get("cluster"))
		if id == "" || id == reg.DefaultID() || len(req.URL.Query()["cluster"]) != 1 {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("select a registered cluster for modules"))
			return
		}
		permission := "modules:read"
		if req.Method != http.MethodGet {
			permission = "modules:manage"
		}
		u := auth.UserFromContext(req.Context())
		if u == nil || !u.Can(permission, true, id, "") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		// A request owns its handler/client; never mutate shared selection.
		selected := chi.NewRouter()
		MountModules(selected, k, namespace)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chi.NewRouteContext())
		selected.ServeHTTP(w, req.WithContext(ctx))
	}
	r.HandleFunc("/modules", handle)
	r.HandleFunc("/modules/*", handle)
}
