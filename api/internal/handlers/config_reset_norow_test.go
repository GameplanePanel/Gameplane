package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"
)

// A role-mapping reset with no stored auth row must not write one: a zero
// auth config ({"providers":null}) would turn off every login method.
func TestConfig_ResetRoleMapping_NoAuthRowWritesNothing(t *testing.T) {
	srv, store := newConfigServer(t)

	status, body := doReq(t, http.MethodDelete, srv.URL+"/admin/config/auth/role-mappings/operator", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var v string
	err := store.DB.QueryRowContext(t.Context(), `SELECT value FROM config WHERE key = 'auth'`).Scan(&v)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("auth row after reset: value=%q err=%v, want no row", v, err)
	}
}
