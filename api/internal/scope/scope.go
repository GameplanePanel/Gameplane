// Package scope centralizes namespace selection and validation for
// handlers that touch the Kubernetes API.
//
// Resolve answers one question: which namespace is this request for?
// It only validates that the requested namespace is on the configured
// allow-list (`gameplane-games` plus any `GAMEPLANE_EXTRA_NAMESPACES`).
// Whether the *caller* may act in that namespace is an authorization
// decision, made by the rbac middleware against the user's per-namespace
// permission bindings — not here.
//
// Never accept an un-validated namespace from a query string — that's
// CVE-bait. Everything goes through Resolve() or Allowed().
package scope

import (
	"errors"
	"net/http"
	"os"
	"strings"
)

// DefaultNamespace is the primary namespace the API will operate in.
const DefaultNamespace = "gameplane-games"

// ControlPlaneNamespace is the namespace the operator runs in. Pods and
// Kubernetes events there are readable only by cluster administrators on the
// local cluster (see rbac.Middleware and handlers.MountPods). It is set once
// at startup by SetControlPlaneNamespace and is empty in tests that do not set it.
var ControlPlaneNamespace string

// SetControlPlaneNamespace records the operator's namespace. Call it once at
// startup, before the router serves requests.
func SetControlPlaneNamespace(ns string) {
	ControlPlaneNamespace = strings.TrimSpace(ns)
}

// AllowedNamespaces returns the list of namespaces the API will act in.
// Computed once from env; small enough that a linear scan is fine.
var AllowedNamespaces = func() []string {
	extra := strings.Split(os.Getenv("GAMEPLANE_EXTRA_NAMESPACES"), ",")
	out := []string{DefaultNamespace}
	for _, n := range extra {
		n = strings.TrimSpace(n)
		if n != "" && n != DefaultNamespace {
			out = append(out, n)
		}
	}
	return out
}()

// ErrForbiddenNamespace is returned when the requested namespace is
// not on the allow-list.
var ErrForbiddenNamespace = errors.New("namespace not permitted")

// Resolve returns the namespace to use for this request. If the request
// has no `namespace` query param, DefaultNamespace is used. Any provided
// value must be on AllowedNamespaces. Per-caller authorization (which
// namespaces a user may act in) is enforced separately by the rbac
// middleware via the caller's permission bindings.
func Resolve(req *http.Request) (string, error) {
	requested := strings.TrimSpace(req.URL.Query().Get("namespace"))
	if requested == "" {
		return DefaultNamespace, nil
	}
	if !contains(AllowedNamespaces, requested) {
		return "", ErrForbiddenNamespace
	}
	return requested, nil
}

// Allowed reports whether ns is one of the namespaces the API will act
// in. Used when assigning per-namespace role bindings.
func Allowed(ns string) bool {
	return contains(AllowedNamespaces, ns)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
