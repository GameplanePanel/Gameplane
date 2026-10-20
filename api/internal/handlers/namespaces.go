package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// namespacesResponse is the JSON body of GET /namespaces.
type namespacesResponse struct {
	Namespaces []string `json:"namespaces"`
}

// MountNamespaces wires GET /namespaces: the namespaces the caller may read
// GameServers in, for the resolved `?cluster=` (defaulting to the local
// cluster). It exists so the web dashboard can fan out GET /servers across
// every namespace it is allowed to see (F-263) instead of only the default
// namespace scope.Resolve falls back to.
//
// The permission check reuses the exact rule rbac.Middleware enforces for
// GET /servers (rbac.ReadPermission + auth.User.Can) rather than
// duplicating it, so the two never drift: a namespace is only listed here
// if a real GET /servers?namespace=<ns> against it would be allowed.
func MountNamespaces(r chi.Router, reg *kube.Registry) {
	r.Get("/namespaces", listNamespacesHandler(reg))
}

func listNamespacesHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		cl, err := scope.ResolveCluster(req, reg)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		u := auth.UserFromContext(req.Context())
		perm, ok := rbac.ReadPermission("servers")
		if !ok {
			// No rule covers GET /servers: nothing is readable anywhere.
			writeJSON(w, namespacesResponse{Namespaces: []string{}})
			return
		}
		allowed := make([]string, 0, len(scope.AllowedNamespaces))
		for _, ns := range scope.AllowedNamespaces {
			if u.Can(perm, rbac.Namespaced(perm), cl, ns) {
				allowed = append(allowed, ns)
			}
		}
		writeJSON(w, namespacesResponse{Namespaces: allowed})
	}
}
