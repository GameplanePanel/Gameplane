// Package messages is the registry of localizable API error messages.
//
// Each entry pairs a stable id with its English ICU MessageFormat text, a
// translator note and the default HTTP status. httperr.Message values built
// with New carry the id and arguments in problem responses, so the dashboard
// can localize them. The English text stays the plain-text body, unchanged.
//
// The registry is compile-time constant data. Every English text is parsed
// once at package init, and a parse failure panics, because a malformed
// entry is a programming error that the messages tests guard against.
package messages

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/text/language"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/i18n/icu"
)

// ID is a registry message id, for example "server.not_found".
type ID string

// Entry is one registry message.
type Entry struct {
	ID          ID     `json:"id"`
	English     string `json:"english"`     // ICU MessageFormat
	Description string `json:"description"` // translator note
	Status      int    `json:"-"`           // default HTTP status for New
}

// entries is the registry. Each English text is byte-identical to the
// message the API has always returned.
var entries = []Entry{
	{
		ID:          httperr.IDNamespaceForbidden,
		English:     "namespace not permitted",
		Description: "API error when the user's role does not cover the requested namespace.",
		Status:      http.StatusForbidden,
	},
	{
		ID:          httperr.IDClusterForbidden,
		English:     "cluster not permitted",
		Description: "API error when the ?cluster= query value names a cluster the caller may not use or that is unknown.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          httperr.IDNotFound,
		English:     "not found",
		Description: "API error when the requested resource does not exist.",
		Status:      http.StatusNotFound,
	},
	{
		ID:          httperr.IDAlreadyExists,
		English:     "already exists",
		Description: "API error when creating a resource whose name is already taken.",
		Status:      http.StatusConflict,
	},
	{
		ID:          httperr.IDConflictRetry,
		English:     "conflict, retry",
		Description: "API error when a concurrent write changed the resource; the caller should re-read and retry.",
		Status:      http.StatusConflict,
	},
	{
		ID:          httperr.IDForbidden,
		English:     "forbidden",
		Description: "API error when the caller, or the API's service identity in Kubernetes, is not allowed to perform the operation.",
		Status:      http.StatusForbidden,
	},
	{
		ID:          httperr.IDUnauthorized,
		English:     "unauthorized",
		Description: "API error when the request carries no valid credentials, or Kubernetes rejects the credentials used for the operation.",
		Status:      http.StatusUnauthorized,
	},
	{
		ID:          httperr.IDInvalid,
		English:     "{detail}",
		Description: "API validation error. The argument detail is the validation message from Kubernetes, in English, and describes the caller's input.",
		Status:      http.StatusUnprocessableEntity,
	},
	{
		ID:          httperr.IDBadRequest,
		English:     "bad request",
		Description: "API error for a malformed request that does not fit a more specific message.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          httperr.IDEmptyBody,
		English:     "empty body",
		Description: "API error when a route that expects a JSON body receives none.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          httperr.IDInternal,
		English:     "internal error",
		Description: "API error for an unexpected server failure. The real error is logged on the server only.",
		Status:      http.StatusInternalServerError,
	},
	{
		ID:          httperr.IDRemoteClusterNotImplemented,
		English:     httperr.RemoteClusterNotImplemented,
		Description: "API response for a home-cluster-only route called with a ?cluster= value that names another cluster. No cross-cluster agent exists yet.",
		Status:      http.StatusNotImplemented,
	},
	{
		ID:          "server.auth.invalid_credentials",
		English:     "invalid credentials",
		Description: "Login error. It is generic on purpose, so it never reveals whether an account exists.",
		Status:      http.StatusUnauthorized,
	},
	{
		ID:          "server.generic.400",
		English:     "The request was not accepted.",
		Description: "Generic sentence shown before the detail text for an HTTP 400 response.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.generic.401",
		English:     "You are not signed in.",
		Description: "Generic sentence shown before the detail text for an HTTP 401 response.",
		Status:      http.StatusUnauthorized,
	},
	{
		ID:          "server.generic.403",
		English:     "You do not have permission to do this.",
		Description: "Generic sentence shown before the detail text for an HTTP 403 response.",
		Status:      http.StatusForbidden,
	},
	{
		ID:          "server.generic.404",
		English:     "The requested item was not found.",
		Description: "Generic sentence shown before the detail text for an HTTP 404 response.",
		Status:      http.StatusNotFound,
	},
	{
		ID:          "server.generic.409",
		English:     "The request conflicts with the current state.",
		Description: "Generic sentence shown before the detail text for an HTTP 409 response.",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.generic.413",
		English:     "The request is too large.",
		Description: "Generic sentence shown before the detail text for an HTTP 413 response.",
		Status:      http.StatusRequestEntityTooLarge,
	},
	{
		ID:          "server.generic.422",
		English:     "The request contains invalid values.",
		Description: "Generic sentence shown before the detail text for an HTTP 422 response.",
		Status:      http.StatusUnprocessableEntity,
	},
	{
		ID:          "server.generic.429",
		English:     "Too many requests. Please wait and try again.",
		Description: "Generic sentence shown before the detail text for an HTTP 429 response.",
		Status:      http.StatusTooManyRequests,
	},
	{
		ID:          "server.generic.500",
		English:     "The server hit an internal error.",
		Description: "Generic sentence shown before the detail text for an HTTP 500 response.",
		Status:      http.StatusInternalServerError,
	},
	{
		ID:          "server.generic.501",
		English:     "This is not supported here.",
		Description: "Generic sentence shown before the detail text for an HTTP 501 response.",
		Status:      http.StatusNotImplemented,
	},
	{
		ID:          "server.generic.502",
		English:     "An upstream service returned an error.",
		Description: "Generic sentence shown before the detail text for an HTTP 502 response.",
		Status:      http.StatusBadGateway,
	},
	{
		ID:          "server.generic.503",
		English:     "The service is temporarily unavailable.",
		Description: "Generic sentence shown before the detail text for an HTTP 503 response.",
		Status:      http.StatusServiceUnavailable,
	},
	{
		ID:          "server.i18n.unknown_language",
		English:     "unknown language: {language}",
		Description: "Shown when a user picks a language this release does not offer. language = the language tag that was sent.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.i18n.unlisted_language",
		English:     "language is not available: {language}",
		Description: "Shown when an admin picks an instance default language that is not offered (missing or below the completeness threshold). language = the tag.",
		Status:      http.StatusBadRequest,
	},
}

// parsedEntry is a registry entry with its English text parsed once.
type parsedEntry struct {
	entry Entry
	msg   icu.Message
}

var (
	// byID indexes the registry by id.
	byID map[ID]parsedEntry
	// sorted is the registry sorted by id, returned by All.
	sorted []Entry
)

func init() {
	all := slices.Concat(entries, serverEntries)
	byID = make(map[ID]parsedEntry, len(all))
	for _, e := range all {
		if _, dup := byID[e.ID]; dup {
			panic(fmt.Sprintf("messages: duplicate id %q", e.ID))
		}
		msg, err := icu.Parse(e.English)
		if err != nil {
			panic(fmt.Sprintf("messages: entry %q: parse English: %v", e.ID, err))
		}
		byID[e.ID] = parsedEntry{entry: e, msg: msg}
	}
	sorted = slices.Clone(all)
	slices.SortFunc(sorted, func(a, b Entry) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
}

// All returns a copy of every registry entry, sorted by ID.
func All() []Entry {
	return slices.Clone(sorted)
}

// Lookup returns the registry entry for id.
func Lookup(id ID) (Entry, bool) {
	p, ok := byID[id]
	if !ok {
		return Entry{}, false
	}
	return p.entry, true
}

// New builds a localizable error for id. Its English text is the entry's
// ICU text rendered with args. An unknown id yields a 500 "internal error"
// that keeps the requested id, so the mistake stays visible in responses.
func New(id ID, args map[string]any) *httperr.Message {
	p, ok := byID[id]
	if !ok {
		return &httperr.Message{ID: string(id), Status: http.StatusInternalServerError, English: "internal error"}
	}
	return &httperr.Message{
		ID:      string(id),
		Args:    args,
		Status:  p.entry.Status,
		English: icu.Format(p.msg, language.English, args),
	}
}
