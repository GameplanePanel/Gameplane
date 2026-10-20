// Package httperr maps internal errors to safe, generic messages for
// the HTTP response while preserving the full error in the server log.
//
// The default path previously echoed err.Error() directly, which leaked
// K8s API paths, database column names, FS paths, and other detail to
// unauthenticated or minimally-authed callers.
//
// Every response is available in two shapes. The default is the plain-text
// body http.Error has always written. A caller whose Accept header asks for
// application/problem+json (RFC 9457) instead receives a problem document
// that also carries a stable messageId and the message arguments, so the
// dashboard can localize the text. The plain-text body is unchanged.
package httperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// RemoteClusterNotImplemented is the response body for a home-cluster-only
// route called with a `?cluster=` selector naming another cluster. There is
// no cross-cluster agent yet to serve such a request on the remote cluster.
const RemoteClusterNotImplemented = "not implemented: no cross-cluster agent yet; this route only serves the home cluster"

// problemJSON is the media type of an RFC 9457 problem document.
const problemJSON = "application/problem+json"

// Message is a localizable error: a registry id, its arguments, the HTTP
// status and the English text (the plain-text body, unchanged).
type Message struct {
	ID      string
	Args    map[string]any
	Status  int
	English string
}

// Error implements error with the English text.
func (m *Message) Error() string { return m.English }

// Message ids httperr itself emits (registered in api/internal/messages).
const (
	IDNamespaceForbidden          = "server.namespace_forbidden"            // "namespace not permitted"
	IDClusterForbidden            = "server.cluster_forbidden"              // "cluster not permitted"
	IDNotFound                    = "server.not_found"                      // "not found"
	IDAlreadyExists               = "server.already_exists"                 // "already exists"
	IDConflictRetry               = "server.conflict_retry"                 // "conflict, retry"
	IDForbidden                   = "server.forbidden"                      // "forbidden"
	IDUnauthorized                = "server.unauthorized"                   // "unauthorized"
	IDInvalid                     = "server.invalid"                        // "{detail}" — apierrors Invalid message (or "invalid")
	IDBadRequest                  = "server.bad_request"                    // "bad request"
	IDEmptyBody                   = "server.empty_body"                     // "empty body"
	IDInternal                    = "server.internal"                       // "internal error"
	IDRemoteClusterNotImplemented = "server.remote_cluster_not_implemented" // RemoteClusterNotImplemented
)

// GenericID is the id of the generic sentence registered for an HTTP
// status, shown before the detail text in the dashboard.
func GenericID(status int) string {
	return fmt.Sprintf("server.generic.%d", status)
}

// KnownIDs lists every id above (used by the messages registry test).
func KnownIDs() []string {
	return []string{
		IDNamespaceForbidden,
		IDClusterForbidden,
		IDNotFound,
		IDAlreadyExists,
		IDConflictRetry,
		IDForbidden,
		IDUnauthorized,
		IDInvalid,
		IDBadRequest,
		IDEmptyBody,
		IDInternal,
		IDRemoteClusterNotImplemented,
	}
}

// WantsProblem reports whether the request's Accept header asks for an
// application/problem+json response. Every Accept value is parsed; a range
// counts only when its media type is exactly application/problem+json
// (wildcards never match) and its q parameter is above zero. A missing or
// unparseable q means 1 and 0 respectively.
func WantsProblem(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, header := range r.Header.Values("Accept") {
		for _, rng := range strings.Split(header, ",") {
			parts := strings.Split(rng, ";")
			if !strings.EqualFold(strings.TrimSpace(parts[0]), problemJSON) {
				continue
			}
			q := 1.0
			for _, param := range parts[1:] {
				key, value, ok := strings.Cut(param, "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
					continue
				}
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil {
					q = 0
				} else {
					q = parsed
				}
			}
			if q > 0 {
				return true
			}
		}
	}
	return false
}

// problemBody is the RFC 9457 document written for WantsProblem callers.
// Field order is the wire order.
type problemBody struct {
	Type      string         `json:"type"`
	Status    int            `json:"status"`
	Title     string         `json:"title"`
	Detail    string         `json:"detail"`
	MessageID string         `json:"messageId"`
	Args      map[string]any `json:"args"`
}

// writeError is the single response writer behind every helper here. It
// writes the problem document for WantsProblem callers and otherwise the
// byte-identical http.Error plain-text body.
func writeError(w http.ResponseWriter, r *http.Request, status int, text, id string, args map[string]any) {
	if !WantsProblem(r) {
		http.Error(w, text, status)
		return
	}
	if args == nil {
		args = map[string]any{}
	}
	body := problemBody{
		Type:      "about:blank",
		Status:    status,
		Title:     http.StatusText(status),
		Detail:    strings.TrimSuffix(text, "\n"),
		MessageID: id,
		Args:      args,
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		// The arguments could not be marshaled; fall back to the plain body
		// rather than send a partial document.
		http.Error(w, text, status)
		return
	}
	w.Header().Set("Content-Type", problemJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, buf.String())
}

// WriteRemoteClusterNotImplemented answers 501 Not Implemented with
// RemoteClusterNotImplemented as the body. It deliberately bypasses
// WriteCode's >=500 masking (which replaces the body with the generic
// http.StatusText): that masking exists to keep unvetted upstream error
// text away from the caller, whereas this message is a fixed, caller-safe
// constant with no upstream error in it, and the reason is what tells the
// caller why the route declined.
func WriteRemoteClusterNotImplemented(w http.ResponseWriter) {
	http.Error(w, RemoteClusterNotImplemented, http.StatusNotImplemented)
}

// WriteCode writes a specific HTTP status with the supplied (safe)
// message. Use this when the handler has already classified the
// condition itself — for example, surfacing a CRD finalizer's
// in-use blocker as a 409.
//
// For a 4xx, err.Error() is echoed verbatim: by convention every existing
// 4xx caller has already classified the condition and hand-built a message
// safe to show the caller (e.g. "ref is required", "cluster already
// exists"). If err is a *Message, its English text, id and args are used
// instead, so a problem-document caller also gets the registry id. A 4xx
// without a *Message gets the id server.generic.<status>.
//
// For a >=500 status that contract does not hold — several callers wrap a
// raw upstream error (e.g. a mod-registry GET failure) that was never
// vetted for caller-safety, and a *url.Error embeds the full request URL,
// query string included, which for a provider like Steam carries an
// admin's API key. So >=500 never echoes err.Error(): the generic
// http.StatusText for the code is written to the client, and the real
// error is still logged in full server-side.
func WriteCode(w http.ResponseWriter, req *http.Request, status int, err error) {
	if status >= 500 {
		slog.Error("handler error",
			"method", req.Method, "path", req.URL.Path, "err", err)
		writeError(w, req, status, http.StatusText(status), GenericID(status), nil)
		return
	}
	slog.Debug("handler client error",
		"method", req.Method, "path", req.URL.Path, "status", status, "err", err)
	var m *Message
	if errors.As(err, &m) {
		writeError(w, req, status, m.English, m.ID, m.Args)
		return
	}
	writeError(w, req, status, err.Error(), GenericID(status), nil)
}

// WriteMessage writes a localizable Message. The English text is the
// plain-text body, and a problem-document caller also gets m.ID and m.Args.
// A nil message or a status of 0 becomes 500. For a status >=500 the body is
// the generic http.StatusText, except for IDRemoteClusterNotImplemented,
// whose fixed text is safe to show.
func WriteMessage(w http.ResponseWriter, r *http.Request, m *Message) {
	if m == nil {
		m = &Message{ID: IDInternal, Status: http.StatusInternalServerError, English: "internal error"}
	}
	status := m.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	text, id, args := m.English, m.ID, m.Args
	if status >= 500 && m.ID != IDRemoteClusterNotImplemented {
		text = http.StatusText(status)
		id = GenericID(status)
		args = nil
	}
	writeError(w, r, status, text, id, args)
}

// Write responds with the appropriate HTTP status for the given error
// and a generic message. The actual error is logged at Error level so
// operators can still debug. A *Message anywhere in the error chain is
// rendered with its own status, id and arguments.
func Write(w http.ResponseWriter, req *http.Request, err error) {
	status, msg, id, args := classify(err)
	if status >= 500 {
		slog.Error("handler error",
			"method", req.Method, "path", req.URL.Path, "err", err)
	} else {
		slog.Debug("handler client error",
			"method", req.Method, "path", req.URL.Path, "status", status, "err", err)
	}
	writeError(w, req, status, msg, id, args)
}

// classify returns (status, safeMessage, messageID, args) for a range of
// common errors. The default is 500 "internal error" — never leak unknown
// errors.
func classify(err error) (int, string, string, map[string]any) {
	var m *Message
	if errors.As(err, &m) {
		if m.Status == 0 {
			return http.StatusInternalServerError, "internal error", IDInternal, nil
		}
		if m.Status >= 500 && m.ID != IDRemoteClusterNotImplemented {
			// Same masking as WriteCode and WriteMessage: a 5xx never
			// echoes the message text or its arguments.
			return m.Status, http.StatusText(m.Status), GenericID(m.Status), nil
		}
		return m.Status, m.English, m.ID, m.Args
	}
	switch {
	case errors.Is(err, scope.ErrForbiddenNamespace):
		return http.StatusForbidden, "namespace not permitted", IDNamespaceForbidden, nil
	case errors.Is(err, scope.ErrForbiddenCluster):
		// 400, not 403: an unknown ?cluster= is a malformed request (like an
		// unrecognized query param value), not an authorization decision —
		// that mirrors rbac.Middleware's direct 400 for the same error.
		return http.StatusBadRequest, "cluster not permitted", IDClusterForbidden, nil
	case apierrors.IsNotFound(err):
		return http.StatusNotFound, "not found", IDNotFound, nil
	case errors.Is(err, db.ErrShareLinkNotFound):
		return http.StatusNotFound, "not found", IDNotFound, nil
	case apierrors.IsAlreadyExists(err):
		return http.StatusConflict, "already exists", IDAlreadyExists, nil
	case apierrors.IsConflict(err):
		// A resourceVersion conflict is an expected, transient condition
		// under concurrent writers (e.g. the operator's reconciler racing
		// an API-initiated status update) — not a server bug. Surface it
		// as a retryable 409 rather than falling through to the opaque
		// 500 default below.
		return http.StatusConflict, "conflict, retry", IDConflictRetry, nil
	case apierrors.IsForbidden(err):
		return http.StatusForbidden, "forbidden", IDForbidden, nil
	case apierrors.IsUnauthorized(err):
		return http.StatusUnauthorized, "unauthorized", IDUnauthorized, nil
	case apierrors.IsInvalid(err):
		// Validation errors are safe to surface — they describe the
		// user's input, not server internals.
		msg := apiErrMessage(err)
		return http.StatusUnprocessableEntity, msg, IDInvalid, map[string]any{"detail": msg}
	case apierrors.IsBadRequest(err):
		return http.StatusBadRequest, "bad request", IDBadRequest, nil
	case errors.Is(err, io.EOF):
		return http.StatusBadRequest, "empty body", IDEmptyBody, nil
	}
	return http.StatusInternalServerError, "internal error", IDInternal, nil
}

func apiErrMessage(err error) string {
	var se *apierrors.StatusError
	if errors.As(err, &se) && se.ErrStatus.Message != "" {
		return se.ErrStatus.Message
	}
	return "invalid"
}
