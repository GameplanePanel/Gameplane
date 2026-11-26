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
		ID:          "server.backup.download.busy",
		English:     "Too many downloads are running. Try again in a minute.",
		Description: "API error from the backup archive download (backup_archives.go:download) when the namespace already has the maximum number of active exports (backups.maxConcurrentExports).",
		Status:      http.StatusTooManyRequests,
	},
	{
		ID:          "server.backup.download.failed",
		English:     "The download could not start. Try again.",
		Description: "API error from the backup archive download (backup_archives.go:awaitExport) when the export worker reports Failed or Expired. The worker's reason is logged and audited, never shown to the client.",
		Status:      http.StatusBadGateway,
	},
	{
		ID:          "server.backup.download.not_ready",
		English:     "This backup has not finished yet.",
		Description: "API error from the backup archive download (backup_archives.go:download) when the Backup is not in the Succeeded phase.",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.backup.download.snapshot_strategy",
		English:     "Snapshot backups stay inside the storage system and cannot be downloaded.",
		Description: "API error from the backup archive download (backup_archives.go:download) when the Backup uses the volume-snapshot strategy, which has no restic snapshot to export.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.backup.download.start_timeout",
		English:     "The download did not start in time. Try again.",
		Description: "API error from the backup archive download (backup_archives.go:awaitExport) when the export is not Ready within the start timeout.",
		Status:      http.StatusGatewayTimeout,
	},
	{
		ID:          "server.restore.confirm.bad_request",
		English:     "The confirm request is not valid.",
		Description: "API error from the restore confirm route (backup_archives.go:confirm) when the body does not carry a positive generation.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.restore.confirm.not_ready",
		English:     "This restore is not waiting for confirmation. Refresh and try again.",
		Description: "API error from the restore confirm route (backup_archives.go:confirm) when the Restore is not in AwaitingConfirmation or its generation does not match the request.",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.restore.upload.bad_request",
		English:     "The upload request is not valid: {message}",
		Description: "API error from the upload route (backup_archives.go:upload) for a bad format, file name, server or template. {message} names the problem.",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.restore.upload.failed",
		English:     "The upload could not be received. Try again.",
		Description: "API error from the upload route (backup_archives.go:upload) when the restore cannot take the archive. The reason from the operator or the archive receiver is logged, never shown to the client.",
		Status:      http.StatusBadGateway,
	},
	{
		ID:          "server.restore.upload.length_required",
		English:     "The upload needs a Content-Length header.",
		Description: "API error from the upload route (backup_archives.go:upload) when the request has no usable Content-Length.",
		Status:      http.StatusLengthRequired,
	},
	{
		ID:          "server.restore.upload.no_storage_class",
		English:     "No storage class is available to check uploads. Ask an administrator to configure one.",
		Description: "API error from the upload route (backup_archives.go:upload) when the operator reports NoStagingStorageClass: no storage class is configured or defaulted for the temporary staging volume.",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.restore.upload.server_busy",
		English:     "A backup, restore or wipe is running on this server. Try again when it finishes.",
		Description: "API error from the upload route (backup_archives.go:upload) when a non-terminal Backup or Restore targets the server, or a data wipe is running.",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.restore.upload.too_large",
		English:     "The archive is larger than the upload limit.",
		Description: "API error from the upload route (backup_archives.go:upload) when the declared Content-Length is larger than backups.maxUploadSize.",
		Status:      http.StatusRequestEntityTooLarge,
	},
	{
		ID:          "server.mcp_tokens.invalid_name",
		English:     "name must be 1 to 64 characters",
		Description: "API error when a personal MCP token name is empty or longer than 64 characters (mcp_tokens.go:writeMCPTokenErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.mcp_tokens.invalid_lifetime",
		English:     "lifetime must be 7, 30, 90 or 365 days",
		Description: "API error when a personal MCP token is created with an expiry other than 7, 30, 90 or 365 days (mcp_tokens.go:writeMCPTokenErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.mcp_tokens.duplicate_name",
		English:     "a token with this name already exists",
		Description: "API error when the user already has an active personal MCP token with the same name (mcp_tokens.go:writeMCPTokenErr).",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.mcp_tokens.limit_reached",
		English:     "you already have 10 active tokens; revoke one before creating another",
		Description: "API error when a user already holds the maximum of 10 active personal MCP tokens (mcp_tokens.go:writeMCPTokenErr).",
		Status:      http.StatusUnprocessableEntity,
	},
	{
		ID:          "server.mcp_tokens.not_found",
		English:     "token not found",
		Description: "API error when a personal MCP token to revoke does not exist, is already revoked, or belongs to another user (mcp_tokens.go:writeMCPTokenErr).",
		Status:      http.StatusNotFound,
	},
	{
		ID:          "server.sftp.login_failed",
		English:     "sftp login failed",
		Description: "API error from the SFTP login (sftp_login.go:login) for every refusal cause; the body is identical whatever failed.",
		Status:      http.StatusUnauthorized,
	},
	{
		ID:          "server.sftp.key_invalid",
		English:     "not a valid SSH public key",
		Description: "API error when an SFTP public key cannot be parsed as one authorized_keys line (sftp.go:writeSFTPErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.key_type_unsupported",
		English:     "unsupported key type; use ed25519, ecdsa or RSA 3072 bits or more",
		Description: "API error when an SFTP public key uses an unsupported algorithm (sftp.go:writeSFTPErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.key_weak",
		English:     "RSA keys must be at least 3072 bits",
		Description: "API error when an SFTP RSA public key is shorter than 3072 bits (sftp.go:writeSFTPErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.key_name_invalid",
		English:     "name must be 1 to 64 characters",
		Description: "API error when an SFTP key name is empty or longer than 64 characters (sftp.go:writeSFTPErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.key_name_in_use",
		English:     "a key with this name already exists",
		Description: "API error when the user already has an SFTP key with the same name (sftp.go:writeSFTPErr).",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.sftp.key_duplicate",
		English:     "this key is already registered",
		Description: "API error when an SFTP public key is already registered. It never says to whom (sftp.go:writeSFTPErr).",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.sftp.key_limit_reached",
		English:     "you already have 20 SFTP keys; remove one before adding another",
		Description: "API error when a user already holds the maximum of 20 SFTP keys (sftp.go:writeSFTPErr).",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.sftp.key_not_found",
		English:     "SFTP key not found",
		Description: "API error when an SFTP key to delete does not exist or belongs to another user (sftp.go:writeSFTPErr).",
		Status:      http.StatusNotFound,
	},
	{
		ID:          "server.sftp.login_name_invalid",
		English:     "SFTP username must be 1 to 64 characters without spaces, \"/\" or \"+\"",
		Description: "API error when a chosen SFTP username is empty, too long or has a space, control character, slash or plus sign (sftp.go:writeSFTPErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.login_name_taken",
		English:     "this SFTP username is already used by another account",
		Description: "API error when a chosen SFTP username is another account's username, SFTP username or display name (sftp.go:writeSFTPErr).",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.sftp.password_length_invalid",
		English:     "length must be 20 to 40",
		Description: "API error when a generated SFTP password length is outside 20 to 40 (sftp.go:writeSFTPErr).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.password_not_found",
		English:     "no SFTP password is set",
		Description: "API error when the user revokes an SFTP password that is not set (sftp.go:revokePassword).",
		Status:      http.StatusNotFound,
	},
	{
		ID:          "server.sftp.token_limit_reached",
		English:     "this cluster already has 2 active SFTP tokens; revoke one before creating another",
		Description: "API error when a cluster already holds the maximum of 2 active SFTP service tokens (sftp_cluster.go:createToken).",
		Status:      http.StatusConflict,
	},
	{
		ID:          "server.sftp.token_not_found",
		English:     "SFTP token not found",
		Description: "API error when an SFTP service token to revoke does not exist, is already revoked, or belongs to another cluster (sftp_cluster.go:revokeToken).",
		Status:      http.StatusNotFound,
	},
	{
		ID:          "server.sftp.endpoint_invalid",
		English:     "address must be a host or host:port",
		Description: "API error when the SFTP endpoint address of a remote cluster is not a host or host:port (sftp_cluster.go:putEndpoint).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.fingerprint_invalid",
		English:     "host key fingerprint must be a SHA256 fingerprint",
		Description: "API error when the SFTP host key fingerprint of a remote cluster is not an OpenSSH SHA256 fingerprint (sftp_cluster.go:putEndpoint).",
		Status:      http.StatusBadRequest,
	},
	{
		ID:          "server.sftp.endpoint_local",
		English:     "this cluster's SFTP endpoint is set in the chart values",
		Description: "API error when an admin tries to set the SFTP endpoint of the panel's own cluster through the API (sftp_cluster.go:putEndpoint).",
		Status:      http.StatusBadRequest,
	},
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
		ID:          "server.cluster.security_settings_missing",
		English:     "the security settings ConfigMap is missing on this cluster; run helm upgrade to recreate it",
		Description: "API error from the cluster security settings API (security_settings.go) when the gameplane-security-settings ConfigMap is not found on the cluster. Nothing is written.",
		Status:      http.StatusConflict,
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
	{
		ID:          "server.files.starting",
		English:     "files are starting, try again in a moment",
		Description: "API error when a stopped or asleep server's file-only pod is not ready within 60 s (ws/ensure_files.go:ensureFiles); retry after a short wait.",
		Status:      http.StatusServiceUnavailable,
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
