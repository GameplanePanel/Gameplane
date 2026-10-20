package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
	"github.com/GameplanePanel/gameplane/api/internal/telemetry"
)

// MountConfig exposes the admin config store at /admin/config.
//
// Each AdminSettings section is persisted as a single JSON blob keyed by
// section name. The set of valid sections is closed (see newValidators)
// so the API never round-trips arbitrary keys, which keeps the surface
// small enough to audit and bounds the value column's worst case.
//
// helmOIDCPresent reports whether the Helm-flag OIDC provider is
// configured — it counts as an always-enabled provider for validateAuth's
// lockout guard.
//
// gameDataStorageClass is the StorageClass name passed via the Helm
// value operator.gameDataStorage.storageClassName to the API's
// --game-data-storage-class CLI flag (empty if using cluster default).
// Returned read-only in installTimeSettings.gameDataStorageClass.
//
// helmPolicy is the Helm-seeded OIDC provider policy, containing groupsClaim,
// defaultRole, and roleMappings. If non-nil, its values are returned read-only
// in installTimeSettings.oidcHelmProvider (the raw seed, not merged with
// helmOverride overrides). Passed as *auth.ProviderPolicy from main.go.
func MountConfig(r chi.Router, store *db.Store, auditor *audit.Auditor, helmOIDCPresent bool, gameDataStorageClass string, helmPolicy *auth.ProviderPolicy) {
	MountConfigWithTelemetry(r, store, auditor, helmOIDCPresent, gameDataStorageClass, helmPolicy, TelemetrySettings{})
}

// MountConfigWithTelemetry is MountConfig with the install's telemetry
// setting, which the "telemetry" section's save hook needs to refuse a save
// when the operator disabled telemetry and to schedule the first report.
// MountConfig passes the zero value (no destination restriction, 24h).
func MountConfigWithTelemetry(r chi.Router, store *db.Store, auditor *audit.Auditor, helmOIDCPresent bool, gameDataStorageClass string, helmPolicy *auth.ProviderPolicy, tel TelemetrySettings) {
	h := &configHandler{
		db:                   store,
		auditor:              auditor,
		validators:           newValidators(helmOIDCPresent),
		gameDataStorageClass: gameDataStorageClass,
		helmPolicy:           helmPolicy,
		telemetry:            tel,
	}
	r.Route("/admin/config", func(r chi.Router) {
		r.Get("/", h.getAll)
		r.Put("/{section}", h.put)
		r.Delete("/auth/role-mappings/{role}", h.resetRoleMapping)
	})
}

type configHandler struct {
	db                   *db.Store
	auditor              *audit.Auditor
	validators           map[string]func([]byte) (json.RawMessage, error)
	gameDataStorageClass string
	helmPolicy           *auth.ProviderPolicy
	telemetry            TelemetrySettings
}

func (h *configHandler) getAll(w http.ResponseWriter, req *http.Request) {
	rows, err := h.db.DB.QueryContext(req.Context(),
		`SELECT key, value FROM config`)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	defer func() { _ = rows.Close() }()

	out := map[string]json.RawMessage{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			httperr.Write(w, req, err)
			return
		}
		// Skip rows for sections we no longer recognize; the table
		// allows arbitrary keys at the schema level but the API
		// surface only ever exposes the validated set.
		if _, ok := h.validators[key]; !ok {
			continue
		}
		out[key] = json.RawMessage(value)
	}

	// Add installTimeSettings: a read-only snapshot of install-time configuration.
	// Present if gameDataStorageClass is set or helmPolicy is configured.
	installSettings := h.buildInstallTimeSettings()
	if installSettings != nil {
		out["installTimeSettings"] = installSettings
	}

	writeJSON(w, out)
}

func (h *configHandler) put(w http.ResponseWriter, req *http.Request) {
	section := chi.URLParam(req, "section")
	validate, ok := h.validators[section]
	if !ok {
		httperr.WriteMessage(w, req, messages.New("server.config.unknown_section", nil))
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		httperr.WriteMessage(w, req, messages.New("server.config.read_body", nil))
		return
	}
	if len(body) == 0 {
		httperr.WriteMessage(w, req, messages.New("server.empty_body", nil))
		return
	}
	canon, err := validate(body)
	if err != nil {
		var m *httperr.Message
		if errors.As(err, &m) {
			httperr.WriteMessage(w, req, m)
			return
		}
		httperr.WriteCode(w, req, http.StatusUnprocessableEntity, err)
		return
	}

	// For "auth" section, detect helmOverride changes before persisting.
	var auditEvents []struct {
		role   string
		groups string
	}
	if section == "auth" {
		auditEvents = h.detectAuthAuditEvents(req.Context(), canon)
	}

	if section == "telemetry" {
		// The telemetry section's post-save hook runs in the same
		// transaction as the config write (see saveTelemetry).
		if err := h.saveTelemetry(req.Context(), canon); err != nil {
			if errors.Is(err, telemetry.ErrOperatorDisabled) {
				httperr.WriteCode(w, req, http.StatusConflict, err)
				return
			}
			httperr.Write(w, req, err)
			return
		}
	} else if _, err := h.db.DB.ExecContext(req.Context(),
		`INSERT INTO config(key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     value      = excluded.value,
		     updated_at = excluded.updated_at`,
		section, string(canon), db.NowTimestamp(),
	); err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Emit audit events for any helmOverride changes.
	for _, evt := range auditEvents {
		reason := fmt.Sprintf("oidc role mapping override set: role=%s groups=%s", evt.role, evt.groups)
		if err := h.auditor.WriteSync(req.Context(), http.MethodPut, "/admin/config/auth", evt.role, reason, http.StatusOK); err != nil {
			// An audit failure must not fail the request, but it must not be
			// silent either: the override still took effect.
			slog.Error("config: oidc role mapping override audit write failed",
				"role", evt.role, "err", err)
		}
	}

	writeJSON(w, map[string]any{"section": section, "value": canon})
}

// saveTelemetry persists the validated telemetry section through
// telemetry.ApplyConsent, so one transaction writes the config row, sets
// consent_source to "admin", creates or clears the install ID and opens or
// closes the report schedule. It returns telemetry.ErrOperatorDisabled when
// the operator disabled telemetry at install time.
func (h *configHandler) saveTelemetry(ctx context.Context, canon json.RawMessage) error {
	var c telemetryCfg
	if err := json.Unmarshal(canon, &c); err != nil {
		return fmt.Errorf("decode telemetry config: %w", err)
	}
	return inTx(ctx, h.db, func(tx *sql.Tx) error {
		return telemetry.ApplyConsent(ctx, tx, h.telemetry.Dest, h.telemetry.Interval,
			c.SendMetrics, c.Extended, db.TelemetryConsentAdmin)
	})
}

// resetRoleMapping removes a single role's override from helmOverride.roleMappings
// in the "auth" config row, restoring the Helm-seeded value for that role.
// It is idempotent: returns 200 even if the role had no override.
func (h *configHandler) resetRoleMapping(w http.ResponseWriter, req *http.Request) {
	roleParam := chi.URLParam(req, "role")
	validRoles := map[string]bool{"admin": true, "operator": true, "viewer": true}
	if !validRoles[roleParam] {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "validation failed",
			"details": "role must be one of admin, operator, viewer",
		})
		return
	}

	// Fetch the current auth config row.
	var authJSON string
	err := h.db.DB.QueryRowContext(req.Context(),
		`SELECT value FROM config WHERE key = 'auth'`).Scan(&authJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httperr.Write(w, req, err)
		return
	}

	// Parse the auth config. If the row doesn't exist, start with an empty config.
	var c authCfg
	if err == nil {
		if err := json.Unmarshal([]byte(authJSON), &c); err != nil {
			httperr.Write(w, req, err)
			return
		}
	}

	// Check if the role had an override before removal (for audit event).
	hadOverride := c.HelmOverride != nil &&
		c.HelmOverride.RoleMappings != nil &&
		((roleParam == "admin" && c.HelmOverride.RoleMappings.Admin != nil) ||
			(roleParam == "operator" && c.HelmOverride.RoleMappings.Operator != nil) ||
			(roleParam == "viewer" && c.HelmOverride.RoleMappings.Viewer != nil))

	// Remove the role's key from helmOverride.roleMappings if it exists.
	if c.HelmOverride != nil && c.HelmOverride.RoleMappings != nil {
		switch roleParam {
		case "admin":
			c.HelmOverride.RoleMappings.Admin = nil
		case "operator":
			c.HelmOverride.RoleMappings.Operator = nil
		case "viewer":
			c.HelmOverride.RoleMappings.Viewer = nil
		}

		// If all roles are now nil, remove the roleMappings field.
		if c.HelmOverride.RoleMappings.Admin == nil &&
			c.HelmOverride.RoleMappings.Operator == nil &&
			c.HelmOverride.RoleMappings.Viewer == nil {
			c.HelmOverride.RoleMappings = nil
		}

		// If helmOverride is now empty, remove it.
		if c.HelmOverride.RoleMappings == nil {
			c.HelmOverride = nil
		}
	}

	// Marshal the potentially modified config back to JSON.
	canon, err := json.Marshal(c)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Persist the updated config (even if unchanged, for idempotency).
	if _, err := h.db.DB.ExecContext(req.Context(),
		`INSERT INTO config(key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     value      = excluded.value,
		     updated_at = excluded.updated_at`,
		"auth", string(canon), db.NowTimestamp(),
	); err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Emit audit event only if there was an actual change.
	if hadOverride {
		reason := fmt.Sprintf("oidc role mapping override reset: role=%s", roleParam)
		if err := h.auditor.WriteSync(req.Context(), http.MethodDelete, "/admin/config/auth/role-mappings/"+roleParam, roleParam, reason, http.StatusOK); err != nil {
			// An audit failure must not fail the request, but it must not be
			// silent either: the reset still took effect.
			slog.Error("config: oidc role mapping reset audit write failed",
				"role", roleParam, "err", err)
		}
	}

	// Return the updated auth section in the same envelope as put().
	writeJSON(w, map[string]any{"section": "auth", "value": json.RawMessage(canon)})
}

// detectAuthAuditEvents compares the old auth config (from the database) with the
// new one (canonicalized request body) to detect changes to helmOverride.roleMappings.
// Returns a list of roles that were actually changed, along with their new group lists
// (or "none" for empty lists). Emits an audit event only if a role's override list
// changed (was set, updated, or removed).
func (h *configHandler) detectAuthAuditEvents(ctx context.Context, newCanon json.RawMessage) []struct {
	role   string
	groups string
} {
	// Read the old auth config from the database.
	var oldAuthJSON string
	err := h.db.DB.QueryRowContext(ctx,
		`SELECT value FROM config WHERE key = 'auth'`).Scan(&oldAuthJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// On query error, play it safe and emit no audit events.
		return nil
	}

	// Parse both configs.
	var oldCfg, newCfg authCfg
	if err == nil {
		// Old config exists; parse it.
		if err := json.Unmarshal([]byte(oldAuthJSON), &oldCfg); err != nil {
			return nil
		}
	}
	// New config is already validated and canonicalized.
	if err := json.Unmarshal(newCanon, &newCfg); err != nil {
		return nil
	}

	// Extract the old and new override mappings (or nil if absent).
	var oldOverrides, newOverrides *authRoleMappingsPtr
	if oldCfg.HelmOverride != nil {
		oldOverrides = oldCfg.HelmOverride.RoleMappings
	}
	if newCfg.HelmOverride != nil {
		newOverrides = newCfg.HelmOverride.RoleMappings
	}

	var changes []struct {
		role   string
		groups string
	}

	// Check each role independently.
	for _, role := range []string{"admin", "operator", "viewer"} {
		oldList := getRoleList(oldOverrides, role)
		newList := getRoleList(newOverrides, role)

		// Only emit if there was an actual change.
		if !roleListsEqual(oldList, newList) {
			groupsStr := formatGroupsForAudit(newList)
			changes = append(changes, struct {
				role   string
				groups string
			}{role, groupsStr})
		}
	}

	return changes
}

// getRoleList extracts the list of groups for a given role from an authRoleMappingsPtr,
// returning nil if the role or the pointer is nil.
func getRoleList(m *authRoleMappingsPtr, role string) *[]string {
	if m == nil {
		return nil
	}
	switch role {
	case "admin":
		return m.Admin
	case "operator":
		return m.Operator
	case "viewer":
		return m.Viewer
	}
	return nil
}

// roleListsEqual compares two role lists, treating nil and non-nil empty lists as different
// (per M3: an empty non-nil list is a valid override meaning "nobody").
func roleListsEqual(a, b *[]string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(*a) != len(*b) {
		return false
	}
	for i := range *a {
		if (*a)[i] != (*b)[i] {
			return false
		}
	}
	return true
}

// formatGroupsForAudit converts a group list to the audit format: comma-joined for
// non-empty lists, or the literal string "none" for an empty list.
func formatGroupsForAudit(groups *[]string) string {
	if groups == nil {
		// This shouldn't be called with nil, but handle it defensively.
		return "none"
	}
	if len(*groups) == 0 {
		return "none"
	}
	return strings.Join(*groups, ",")
}

// buildInstallTimeSettings constructs the read-only installTimeSettings
// response section from Helm-seeded values. Returns nil if there is nothing
// to report (no gameDataStorageClass and no helmPolicy configured).
func (h *configHandler) buildInstallTimeSettings() json.RawMessage {
	if h.gameDataStorageClass == "" && h.helmPolicy == nil {
		return nil
	}

	// Build the outer map that will be JSON-encoded
	settings := map[string]any{}

	// gameDataStorageClass is always included if it has a value or if we have
	// install-time settings to report (even if empty string); include it whenever
	// installTimeSettings exists.
	// Note: always include the field if installTimeSettings is present;
	// per the contract, gameDataStorageClass is a sibling of oidcHelmProvider.
	if h.gameDataStorageClass != "" || h.helmPolicy != nil {
		settings["gameDataStorageClass"] = h.gameDataStorageClass
	}

	// Include the Helm OIDC provider snapshot if Helm OIDC is configured.
	// This is the raw seed, not merged with helmOverride.
	if h.helmPolicy != nil {
		oidcHelm := map[string]any{
			"groupsClaim": h.helmPolicy.GroupsClaim,
			"defaultRole": h.helmPolicy.DefaultRole,
		}
		// Include roleMappings if it was configured (non-nil); nil means
		// no role mappings were set up via CLI flags.
		if h.helmPolicy.RoleMappings != nil {
			oidcHelm["roleMappings"] = map[string][]string{
				"admin":    h.helmPolicy.RoleMappings.Admin,
				"operator": h.helmPolicy.RoleMappings.Operator,
				"viewer":   h.helmPolicy.RoleMappings.Viewer,
			}
		}
		settings["oidcHelmProvider"] = oidcHelm
	}

	// Marshal to JSON and return as RawMessage.
	data, err := json.Marshal(settings)
	if err != nil {
		// Should never happen; if it does, silently omit installTimeSettings
		// rather than failing the entire request.
		return nil
	}
	return json.RawMessage(data)
}

// newValidators owns both the closed allowlist of section names and the
// per-section schema. Each validator returns the canonicalized JSON that
// gets persisted — it isn't a passthrough of the request body, so
// unknown fields silently drop instead of accumulating in the database.
func newValidators(helmOIDCPresent bool) map[string]func([]byte) (json.RawMessage, error) {
	return map[string]func([]byte) (json.RawMessage, error){
		"general":                           validateGeneral,
		"auth":                              validateAuth(helmOIDCPresent),
		"notifications":                     validateNotifications,
		"telemetry":                         validateTelemetry,
		"i18n":                              validateI18n,
		registry.ConfigSectionModRegistries: validateModRegistries,
	}
}

type generalCfg struct {
	InstanceName     string `json:"instanceName"`
	ExternalURL      string `json:"externalURL"`
	DefaultNamespace string `json:"defaultNamespace"`
}

func validateGeneral(body []byte) (json.RawMessage, error) {
	var c generalCfg
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	// instanceName is optional; notifications fall back to "Gameplane" when empty.
	if c.DefaultNamespace == "" {
		return nil, fmt.Errorf("defaultNamespace is required")
	}
	if !dnsLabelRE.MatchString(c.DefaultNamespace) {
		return nil, fmt.Errorf("defaultNamespace must match RFC1123 label")
	}
	if c.ExternalURL != "" {
		u, err := url.Parse(c.ExternalURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("externalURL must be an http(s) URL")
		}
	}
	return json.Marshal(c)
}

// authRoleMappings mirrors auth.RoleMappings: per dashboard role, the IdP
// group values that grant it.
type authRoleMappings struct {
	Admin    []string `json:"admin,omitempty"`
	Operator []string `json:"operator,omitempty"`
	Viewer   []string `json:"viewer,omitempty"`
}

// authHelmOverride carries the optional per-role override lists for the
// synthetic Helm-configured OIDC provider. Each role's list (if present, even
// as []) replaces the Helm-seeded list for that role; nil/absent means the
// Helm-seeded list stands. Pointers to slices preserve the empty-list-vs-nil
// distinction for provenance signaling.
type authHelmOverride struct {
	RoleMappings *authRoleMappingsPtr `json:"roleMappings,omitempty"`
}

// authRoleMappingsPtr parallels authRoleMappings but uses pointers to slices
// to preserve empty-list-vs-nil distinction (key presence/absence signals
// provenance).
type authRoleMappingsPtr struct {
	Admin    *[]string `json:"admin,omitempty"`
	Operator *[]string `json:"operator,omitempty"`
	Viewer   *[]string `json:"viewer,omitempty"`
}

type authProvider struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"` // "local" | "oidc" | "google" | "github"
	DisplayName string `json:"displayName,omitempty"`
	Enabled     bool   `json:"enabled"`
	// Non-local kinds: the issuer + client id are public identifiers and
	// live here for UI visibility; only the clientSecret hides in the
	// ConfigRef Secret (default gameplane-auth-<name>).
	Issuer    string `json:"issuer,omitempty"`
	ClientID  string `json:"clientID,omitempty"`
	ConfigRef string `json:"configRef,omitempty"` // K8s Secret name
	// Group→role mapping (non-local kinds only; mirrors auth.Provider).
	Scopes       []string          `json:"scopes,omitempty"`
	GroupsClaim  string            `json:"groupsClaim,omitempty"`
	RoleMappings *authRoleMappings `json:"roleMappings,omitempty"`
	DefaultRole  string            `json:"defaultRole,omitempty"`
}

type authCfg struct {
	Providers    []authProvider    `json:"providers"`
	HelmOverride *authHelmOverride `json:"helmOverride,omitempty"`
}

var validAuthKinds = map[string]bool{"local": true, "oidc": true, "google": true, "github": true}

var validDefaultRoles = map[string]bool{"": true, "viewer": true, "operator": true, "admin": true, "deny": true}

// validateHelmRoleMapping checks the role mappings in helmOverride.roleMappings,
// trimming whitespace from each group string in place. Reuses the same validation
// rule as validateProviderMapping: each role list (if present) must be an array
// of non-blank strings. Empty non-nil lists are valid and meaningful.
func validateHelmRoleMapping(ov *authRoleMappingsPtr) error {
	if ov == nil {
		return nil
	}
	for role, groups := range map[string]*[]string{
		"admin":    ov.Admin,
		"operator": ov.Operator,
		"viewer":   ov.Viewer,
	} {
		if groups == nil {
			continue
		}
		for j, g := range *groups {
			trimmed := strings.TrimSpace(g)
			if trimmed == "" {
				return fmt.Errorf("helmOverride.roleMappings.%s[%d] must not be empty", role, j)
			}
			(*groups)[j] = trimmed
		}
	}
	return nil
}

// validateProviderMapping checks the group→role mapping fields of one
// provider entry, trimming scope tokens and the groups claim in place so
// the canonical blob stores clean values. Local providers carry none of
// these fields.
func validateProviderMapping(i int, p *authProvider) error {
	if p.Kind == "local" {
		if len(p.Scopes) > 0 || p.GroupsClaim != "" || p.RoleMappings != nil || p.DefaultRole != "" {
			return fmt.Errorf("providers[%d]: scopes, groupsClaim, roleMappings, and defaultRole are not valid for the local provider", i)
		}
		return nil
	}
	for j, s := range p.Scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			return fmt.Errorf("providers[%d].scopes[%d] must not be empty", i, j)
		}
		if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
			return fmt.Errorf("providers[%d].scopes[%d] must be a single scope token without whitespace", i, j)
		}
		p.Scopes[j] = s
	}
	if p.GroupsClaim != "" {
		claim := strings.TrimSpace(p.GroupsClaim)
		if claim == "" {
			return fmt.Errorf("providers[%d].groupsClaim must not be blank", i)
		}
		p.GroupsClaim = claim
	}
	if !validDefaultRoles[p.DefaultRole] {
		return fmt.Errorf("providers[%d].defaultRole must be one of viewer|operator|admin|deny", i)
	}
	if p.DefaultRole != "" && p.RoleMappings == nil {
		return fmt.Errorf("providers[%d].defaultRole requires roleMappings", i)
	}
	if p.RoleMappings != nil {
		for role, groups := range map[string][]string{
			"admin":    p.RoleMappings.Admin,
			"operator": p.RoleMappings.Operator,
			"viewer":   p.RoleMappings.Viewer,
		} {
			for j, g := range groups {
				if strings.TrimSpace(g) == "" {
					return fmt.Errorf("providers[%d].roleMappings.%s[%d] must not be empty", i, role, j)
				}
			}
		}
	}
	return nil
}

// validateAuth returns the auth-section validator. helmOIDCPresent makes
// the Helm-flag provider count as always-enabled for the lockout guard —
// it can't be disabled from the dashboard.
func validateAuth(helmOIDCPresent bool) func([]byte) (json.RawMessage, error) {
	return func(body []byte) (json.RawMessage, error) {
		var c authCfg
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("invalid json: %w", err)
		}
		seen := map[string]bool{}
		anyEnabled := helmOIDCPresent
		locals := 0
		for i, p := range c.Providers {
			if p.Name == "" {
				return nil, fmt.Errorf("providers[%d].name is required", i)
			}
			if seen[p.Name] {
				return nil, fmt.Errorf("providers[%d].name duplicate: %s", i, p.Name)
			}
			seen[p.Name] = true
			if !validAuthKinds[p.Kind] {
				return nil, fmt.Errorf("providers[%d].kind must be one of local|oidc|google|github", i)
			}
			if p.ConfigRef != "" && !dnsLabelRE.MatchString(p.ConfigRef) {
				return nil, fmt.Errorf("providers[%d].configRef must match RFC1123 label", i)
			}
			if p.Kind == "local" {
				locals++
			} else {
				// Non-local names become URL path segments
				// (/auth/oidc/{name}/…) and default Secret names — bound
				// them to DNS labels. "helm" is the synthetic Helm-flag
				// provider's reserved slug.
				if !dnsLabelRE.MatchString(p.Name) {
					return nil, fmt.Errorf("providers[%d].name must be a lowercase DNS label", i)
				}
				if p.Name == "helm" {
					return nil, fmt.Errorf(`providers[%d].name "helm" is reserved for the Helm-configured provider`, i)
				}
				u, err := url.Parse(p.Issuer)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return nil, fmt.Errorf("providers[%d].issuer must be an http(s) URL", i)
				}
				if p.ClientID == "" {
					return nil, fmt.Errorf("providers[%d].clientID is required", i)
				}
			}
			// Index into the slice (not the loop copy) so the trims
			// applied by the helper survive into the canonical blob.
			if err := validateProviderMapping(i, &c.Providers[i]); err != nil {
				return nil, err
			}
			if p.Enabled {
				anyEnabled = true
			}
		}
		if locals > 1 {
			return nil, fmt.Errorf("at most one local provider is allowed")
		}
		// Saving a config where nothing can authenticate would lock every
		// admin out at their next logout — refuse it here rather than
		// trust each client to guard the toggle.
		if !anyEnabled {
			return nil, fmt.Errorf("at least one identity provider must stay enabled")
		}
		// Validate helmOverride.roleMappings if present.
		if c.HelmOverride != nil && c.HelmOverride.RoleMappings != nil {
			if err := validateHelmRoleMapping(c.HelmOverride.RoleMappings); err != nil {
				return nil, err
			}
		}
		return json.Marshal(c)
	}
}

// Backup destinations used to live in this admin-config blob; they're
// now first-class labelled Secrets served by handlers/destinations.go.
// The single source of truth is the cluster — no parallel registry.

type notifSink struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"` // "discord" | "slack" | "smtp" | "webhook" | "ntfy"
	Enabled   bool     `json:"enabled"`
	ConfigRef string   `json:"configRef,omitempty"` // K8s Secret name holding the sink's credentials
	Events    []string `json:"events,omitempty"`    // subset of notify.AllEvents; empty = notify.DefaultOn
	Language  string   `json:"language,omitempty"`  // BCP 47 tag of a listed language; empty = instance default
}

type notifCfg struct {
	Sinks []notifSink `json:"sinks"`
}

var validSinkKinds = map[string]bool{"discord": true, "slack": true, "smtp": true, "webhook": true, "ntfy": true}

func validateNotifications(body []byte) (json.RawMessage, error) {
	var c notifCfg
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	seen := map[string]bool{}
	for i, s := range c.Sinks {
		if s.Name == "" {
			return nil, fmt.Errorf("sinks[%d].name is required", i)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("sinks[%d].name duplicate: %s", i, s.Name)
		}
		seen[s.Name] = true
		if !validSinkKinds[s.Kind] {
			return nil, fmt.Errorf("sinks[%d].kind must be one of discord|slack|smtp|webhook|ntfy", i)
		}
		if s.Language != "" && !languageListed(s.Language) {
			return nil, messages.New("server.i18n.unknown_language", map[string]any{"language": s.Language})
		}
		// configRef stays optional so sink rows persisted before the
		// delivery pipeline existed keep loading; the dispatcher skips
		// enabled sinks without one and the UI flags them.
		if s.ConfigRef != "" && !dnsLabelRE.MatchString(s.ConfigRef) {
			return nil, fmt.Errorf("sinks[%d].configRef must match RFC1123 label", i)
		}
		seenEv := map[string]bool{}
		for _, ev := range s.Events {
			if !notify.ValidEvent(ev) {
				return nil, fmt.Errorf("sinks[%d].events: unknown event %q", i, ev)
			}
			if seenEv[ev] {
				return nil, fmt.Errorf("sinks[%d].events: duplicate %q", i, ev)
			}
			seenEv[ev] = true
		}
	}
	return json.Marshal(c)
}

type telemetryCfg struct {
	SendMetrics bool `json:"sendMetrics"`
	Extended    bool `json:"extended"`
}

func validateTelemetry(body []byte) (json.RawMessage, error) {
	var c telemetryCfg
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	// Turning basic off turns extended off with it, and the save succeeds
	// (FR-003, spec Q7).
	if !c.SendMetrics {
		c.Extended = false
	}
	return json.Marshal(c)
}

// i18nCfg is the instance language section: the default language for users
// who have not chosen one, and the login page before sign-in.
type i18nCfg struct {
	DefaultLanguage string `json:"defaultLanguage"`
}

// validateI18n requires defaultLanguage to be a tag this release offers. An
// unlisted tag returns a localizable *httperr.Message, so the save reports the
// same error id to the dashboard in either language.
func validateI18n(body []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	var c i18nCfg
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	if c.DefaultLanguage == "" {
		return nil, fmt.Errorf("defaultLanguage is required")
	}
	if !languageListed(c.DefaultLanguage) {
		return nil, messages.New("server.i18n.unlisted_language", map[string]any{"language": c.DefaultLanguage})
	}
	return json.Marshal(c)
}

// modRegistryEntry is one provider's admin-managed mod-registry
// declaration: which provider, and (optionally) a non-default Secret name
// holding its key. It carries no secret material — the value lives in a
// labelled Secret managed by /admin/registries/{provider}/secret
// (registry_secret.go) and is never round-tripped through this blob.
type modRegistryEntry struct {
	Provider  string `json:"provider"`
	ConfigRef string `json:"configRef,omitempty"`
}

type modRegistriesCfg struct {
	Registries []modRegistryEntry `json:"registries"`
}

func validateModRegistries(body []byte) (json.RawMessage, error) {
	var c modRegistriesCfg
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	seen := map[string]bool{}
	for i, e := range c.Registries {
		if !registry.KeyedProviders[e.Provider] {
			return nil, fmt.Errorf("registries[%d].provider must be one of curseforge|steam|nexus", i)
		}
		if seen[e.Provider] {
			return nil, fmt.Errorf("registries[%d].provider duplicate: %s", i, e.Provider)
		}
		seen[e.Provider] = true
		if e.ConfigRef != "" && !dnsLabelRE.MatchString(e.ConfigRef) {
			return nil, fmt.Errorf("registries[%d].configRef must match RFC1123 label", i)
		}
	}
	return json.Marshal(c)
}

// The "updates" section used to persist a mutable release channel here,
// but nothing ever consumed it — Gameplane upgrades happen via Helm. The
// channel is now the chart's informational updates.channel value, served
// read-only on /cluster/info. getAll skips the legacy DB row because the
// key is no longer in sectionValidators.
