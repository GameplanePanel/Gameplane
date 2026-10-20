package db

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
)

// RotateInstallID is the reporter's reaction to a 409 id_claimed (spec 022
// FR-037, T084).
func TestRotateInstallID_ReplacesIDKeepsSecretAndStampsTheRotation(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if err := s.SetInstallID(ctx, txTestInstallID); err != nil {
		t.Fatalf("set install id: %v", err)
	}
	secret, err := s.EnsureSigningSecret(ctx)
	if err != nil {
		t.Fatalf("ensure secret: %v", err)
	}

	const next = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	if err := s.RotateInstallID(ctx, next, "2026-10-06T09:00:00Z"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	st, err := s.GetTelemetryState(ctx)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.InstallID != next || st.LastIDRotationAt != "2026-10-06T09:00:00Z" {
		t.Fatalf("state = %+v, want the new id and the rotation stamp", st)
	}
	if again, err := s.EnsureSigningSecret(ctx); err != nil || !bytes.Equal(again, secret) {
		t.Fatalf("signing secret changed by the rotation: %v", err)
	}
}

func TestRotateInstallID_RefusesWithoutAnIDOrARow(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	// A fresh install has no ID until the notice is seen, so there is nothing
	// to replace and nothing may be written.
	if err := s.RotateInstallID(ctx, txTestInstallID, "2026-10-06T09:00:00Z"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rotate without an id = %v, want sql.ErrNoRows", err)
	}
	if st, err := s.GetTelemetryState(ctx); err != nil || st.InstallID != "" || st.LastIDRotationAt != "" {
		t.Fatalf("state = %+v, %v, want it untouched", st, err)
	}

	closed := newRBACStore(t)
	if err := closed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := closed.RotateInstallID(ctx, txTestInstallID, "x"); err == nil {
		t.Fatal("rotate on a closed database: want an error")
	}
}
