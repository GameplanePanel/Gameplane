package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// MountRoles wires the role-management surface: the permission catalog
// plus CRUD over custom roles. RBAC gates GET on roles:read and writes
// on roles:manage.
func MountRoles(r chi.Router, store *db.Store) {
	h := &roleHandler{db: store}
	r.Route("/roles", func(r chi.Router) {
		r.Get("/", h.list)
		// Register the literal path before the {name} param so it isn't
		// captured as a role named "permissions".
		r.Get("/permissions", h.catalog)
		r.Post("/", h.create)
		r.Patch("/{name}", h.update)
		r.Delete("/{name}", h.del)
	})
}

type roleHandler struct {
	db *db.Store
}

type roleDTO struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	Permissions []string `json:"permissions"`
}

func (h *roleHandler) catalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"groups": rbac.Catalog})
}

func (h *roleHandler) list(w http.ResponseWriter, req *http.Request) {
	roles, err := h.loadRoles(req)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, roles)
}

// loadRoles returns every role with its permission set, in name order.
func (h *roleHandler) loadRoles(req *http.Request) ([]roleDTO, error) {
	rows, err := h.db.DB.QueryContext(req.Context(),
		`SELECT name, description, builtin FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byName := map[string]*roleDTO{}
	out := []roleDTO{}
	for rows.Next() {
		var d roleDTO
		var builtin int
		if err := rows.Scan(&d.Name, &d.Description, &builtin); err != nil {
			return nil, err
		}
		d.Builtin = builtin != 0
		d.Permissions = []string{}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		byName[out[i].Name] = &out[i]
	}

	pRows, err := h.db.DB.QueryContext(req.Context(),
		`SELECT role_name, permission FROM role_permissions`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pRows.Close() }()
	for pRows.Next() {
		var name, perm string
		if err := pRows.Scan(&name, &perm); err != nil {
			return nil, err
		}
		if d := byName[name]; d != nil {
			d.Permissions = append(d.Permissions, perm)
		}
	}
	if err := pRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		sort.Strings(out[i].Permissions)
	}
	return out, nil
}

type roleCreateReq struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (h *roleHandler) create(w http.ResponseWriter, req *http.Request) {
	var body roleCreateReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return
	}
	if !identifierRE.MatchString(body.Name) {
		httperr.WriteMessage(w, req, messages.New("server.roles.invalid_role_name", nil))
		return
	}
	if msg, ok := validatePermissions(body.Permissions); !ok {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New(msg))
		return
	}

	tx, err := h.db.DB.BeginTx(req.Context(), nil)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(req.Context(),
		`INSERT INTO roles(name, description, builtin) VALUES (?, ?, 0)`,
		body.Name, body.Description); err != nil {
		// A duplicate name is a client error, not a 500.
		httperr.WriteMessage(w, req, messages.New("server.roles.role_already_exists", nil))
		return
	}
	if err := insertPermissions(req, tx, body.Name, body.Permissions); err != nil {
		httperr.Write(w, req, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSONCreated(w, roleDTO{Name: body.Name, Description: body.Description, Permissions: dedupSorted(body.Permissions)})
}

type roleUpdateReq struct {
	Description *string   `json:"description,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

func (h *roleHandler) update(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	builtin, found, err := h.roleBuiltin(req, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !found {
		httperr.WriteMessage(w, req, messages.New("server.roles.role_not_found", nil))
		return
	}
	// The admin role is the cluster's last-resort authority — its wildcard
	// must stay intact, so it can't be edited. operator/viewer are editable.
	if builtin && name == rbac.RoleAdmin {
		httperr.WriteMessage(w, req, messages.New("server.roles.admin_not_editable", nil))
		return
	}

	var body roleUpdateReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return
	}
	if body.Permissions != nil {
		if msg, ok := validatePermissions(*body.Permissions); !ok {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New(msg))
			return
		}
		// Lock to serialize with writes so the last-user-manager check can't race.
		unlock := h.db.LockUserManagement()
		defer unlock()
		if !remoteWidePermissionsAllowed(*body.Permissions) {
			var remoteBindings int
			if err := h.db.DB.QueryRowContext(req.Context(),
				`SELECT COUNT(*) FROM user_role_bindings WHERE role_name = ? AND namespace = '*' AND cluster <> ?`,
				name, scope.DefaultCluster).Scan(&remoteBindings); err != nil {
				httperr.Write(w, req, err)
				return
			}
			if remoteBindings > 0 {
				httperr.WriteMessage(w, req, messages.New("server.roles.remote_bindings_block_control_plane", nil))
				return
			}
		}
		if msg, err := h.userManagementGuard(req, name, *body.Permissions); err != nil {
			httperr.Write(w, req, err)
			return
		} else if msg != "" {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New(msg))
			return
		}
	}

	tx, err := h.db.DB.BeginTx(req.Context(), nil)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if body.Description != nil {
		if _, err := tx.ExecContext(req.Context(),
			`UPDATE roles SET description = ?, updated_at = ? WHERE name = ?`,
			*body.Description, db.NowTimestamp(), name); err != nil {
			httperr.Write(w, req, err)
			return
		}
	}
	if body.Permissions != nil {
		if _, err := tx.ExecContext(req.Context(),
			`DELETE FROM role_permissions WHERE role_name = ?`, name); err != nil {
			httperr.Write(w, req, err)
			return
		}
		if err := insertPermissions(req, tx, name, *body.Permissions); err != nil {
			httperr.Write(w, req, err)
			return
		}
		if _, err := tx.ExecContext(req.Context(),
			`UPDATE roles SET updated_at = ? WHERE name = ?`, db.NowTimestamp(), name); err != nil {
			httperr.Write(w, req, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperr.Write(w, req, err)
		return
	}

	roles, err := h.loadRoles(req)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	for _, r := range roles {
		if r.Name == name {
			writeJSON(w, r)
			return
		}
	}
	httperr.WriteMessage(w, req, messages.New("server.roles.role_not_found", nil))
}

func (h *roleHandler) del(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	// Keep the in-use check and deletion atomic with supplemental grants.
	// Otherwise deleting/recreating a role could widen an orphaned binding.
	unlock := h.db.LockUserManagement()
	defer unlock()
	builtin, found, err := h.roleBuiltin(req, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !found {
		httperr.WriteMessage(w, req, messages.New("server.roles.role_not_found", nil))
		return
	}
	if builtin {
		httperr.WriteMessage(w, req, messages.New("server.roles.builtin_not_deletable", nil))
		return
	}
	var inUse int
	if err := h.db.DB.QueryRowContext(req.Context(),
		`SELECT COUNT(*) FROM user_role_bindings WHERE role_name = ?`, name).Scan(&inUse); err != nil {
		httperr.Write(w, req, err)
		return
	}
	if inUse > 0 {
		httperr.WriteMessage(w, req, messages.New("server.roles.role_in_use", nil))
		return
	}

	tx, err := h.db.DB.BeginTx(req.Context(), nil)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	// sqlite has no FK cascade, so drop the permission rows explicitly.
	if _, err := tx.ExecContext(req.Context(), `DELETE FROM role_permissions WHERE role_name = ?`, name); err != nil {
		httperr.Write(w, req, err)
		return
	}
	if _, err := tx.ExecContext(req.Context(), `DELETE FROM roles WHERE name = ?`, name); err != nil {
		httperr.Write(w, req, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *roleHandler) roleBuiltin(req *http.Request, name string) (builtin, found bool, err error) {
	var b int
	err = h.db.DB.QueryRowContext(req.Context(), `SELECT builtin FROM roles WHERE name = ?`, name).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return b != 0, true, nil
}

// validatePermissions rejects the "*" wildcard (reserved for the
// built-in admin role) and any key not in the catalog.
func validatePermissions(perms []string) (string, bool) {
	for _, p := range perms {
		if p == "*" {
			return "the wildcard permission cannot be granted to a custom role", false
		}
		if !rbac.ValidPermission(p) {
			return "unknown permission: " + p, false
		}
	}
	return "", true
}

// userManagementGuard applies the user-administration lockout guards
// (the same ones users.update applies to a primary-role change) to a
// permission change on role. It returns a refusal message when the new
// permissions drop users:manage from a role that grants it today and
// either the role is the caller's own primary role, or every user who can
// manage users holds that role. An empty message means the change may
// proceed.
func (h *roleHandler) userManagementGuard(req *http.Request, role string, perms []string) (string, error) {
	if permsGrantUserManagement(perms) {
		return "", nil
	}
	grantsNow, err := h.db.RoleGrantsUserManagement(req.Context(), role)
	if err != nil {
		return "", err
	}
	if !grantsNow {
		return "", nil
	}
	if caller := auth.UserFromContext(req.Context()); caller != nil && caller.Role == role {
		return "cannot remove your own user-management access", nil
	}
	total, err := h.db.UserManagerCount(req.Context())
	if err != nil {
		return "", err
	}
	others, err := h.db.UserManagerCountExcludingRole(req.Context(), role)
	if err != nil {
		return "", err
	}
	if total > 0 && others == 0 {
		return "cannot remove user management from the role every user manager holds", nil
	}
	return "", nil
}

// permsGrantUserManagement reports whether a permission list includes
// users:manage or the "*" wildcard.
func permsGrantUserManagement(perms []string) bool {
	for _, p := range perms {
		if p == "users:manage" || p == "*" {
			return true
		}
	}
	return false
}

func insertPermissions(req *http.Request, tx *sql.Tx, role string, perms []string) error {
	for _, p := range dedupSorted(perms) {
		if _, err := tx.ExecContext(req.Context(),
			`INSERT INTO role_permissions(role_name, permission) VALUES (?, ?)`, role, p); err != nil {
			return err
		}
	}
	return nil
}

func dedupSorted(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
