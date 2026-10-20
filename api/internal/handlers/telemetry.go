package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/telemetry"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// Notice actions accepted by POST /admin/telemetry/notice.
const (
	noticeSeen        = "seen"
	noticeKeep        = "keep"
	noticeExtendedOff = "extended-off"
	noticeAllOff      = "all-off"
)

// noticeBasicFields and noticeExtendedFields are the exact report fields the
// first-login notice lists (contracts/report-schema.md).
var (
	noticeBasicFields    = []string{"version", "servers", "templates"}
	noticeExtendedFields = []string{"installId", "env.k8s", "env.distro", "env.arch", "env.nodes", "env.deployment", "games", "features"}
)

// TelemetrySettings is the install-time telemetry setting the config and
// notice handlers need: where reports go and how far apart they are.
type TelemetrySettings struct {
	// Dest is the destination the API resolved at startup.
	Dest telemetry.Destination
	// Interval is the spacing between reports; zero means 24h.
	Interval time.Duration
	// Deps is what GET /admin/telemetry collects the preview from; it is the
	// same value the reporter uses. A nil Deps.Store is filled with the
	// handler's store.
	Deps telemetry.Deps
}

// errExtendedOff is returned when an install-ID reset is asked for while the
// extended tier is off: there is no ID to reset.
var errExtendedOff = errors.New("extended telemetry is off: there is no install id to reset")

// MountTelemetry exposes the telemetry status at /admin/telemetry, the install
// ID reset at /admin/telemetry/install-id and the first-login notice at
// /admin/telemetry/notice (spec 022, contracts/api-telemetry-http.md).
func MountTelemetry(r chi.Router, store *db.Store, settings TelemetrySettings) {
	if settings.Deps.Store == nil {
		settings.Deps.Store = store
	}
	h := &telemetryHandler{db: store, settings: settings}
	r.Route("/admin/telemetry", func(r chi.Router) {
		r.Get("/", h.getTelemetry)
		r.Post("/install-id", h.postInstallID)
		r.Get("/notice", h.getNotice)
		r.Post("/notice", h.postNotice)
	})
}

type telemetryHandler struct {
	db       *db.Store
	settings TelemetrySettings
}

// noticeDestination is the destination as shown to an admin: its kind and
// the URL host only, never the path, query or credentials.
type noticeDestination struct {
	Kind string `json:"kind"`
	Host string `json:"host"`
}

// telemetryDestination is noticeDestination for GET /admin/telemetry: host is
// null for the disabled and none kinds.
type telemetryDestination struct {
	Kind string  `json:"kind"`
	Host *string `json:"host"`
}

type telemetryConsent struct {
	Basic    bool   `json:"basic"`
	Extended bool   `json:"extended"`
	Source   string `json:"source"`
}

// telemetryStatus is the delivery status. It never carries a failure detail,
// and the state's signing secret is not reachable from it.
type telemetryStatus struct {
	LastAttemptAt    *string `json:"lastAttemptAt"`
	LastSuccessAt    *string `json:"lastSuccessAt"`
	LastOutcome      string  `json:"lastOutcome"`
	LastIDRotationAt *string `json:"lastIdRotationAt"`
}

type telemetryResponse struct {
	Destination      telemetryDestination `json:"destination"`
	OperatorDisabled bool                 `json:"operatorDisabled"`
	Consent          telemetryConsent     `json:"consent"`
	InstallID        *string              `json:"installId"`
	// Preview is the exact report body the reporter would POST now, or null.
	Preview json.RawMessage `json:"preview"`
	Status  telemetryStatus `json:"status"`
}

// nullString returns nil for the empty string, so it encodes as JSON null.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// destSends reports whether the destination can receive reports at all.
func destSends(d telemetry.Destination) bool {
	return d.Kind != telemetry.KindDisabled && d.Kind != telemetry.KindNone
}

// getTelemetry serves GET /admin/telemetry (config:read). The preview is
// built by telemetry.BuildReport, the function the reporter uses, and encoded
// by telemetryschema.Encode, so it is byte-for-byte what would be POSTed,
// apart from ext.sentAt (SC-006).
func (h *telemetryHandler) getTelemetry(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	st, err := h.db.GetTelemetryState(ctx)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	c, err := telemetry.ReadConsent(ctx, h.db)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	dest := h.settings.Dest
	resp := telemetryResponse{
		Destination:      telemetryDestination{Kind: dest.Kind},
		OperatorDisabled: dest.Kind == telemetry.KindDisabled,
		Consent:          telemetryConsent{Basic: c.Basic, Extended: c.Extended, Source: st.ConsentSource},
		Status: telemetryStatus{
			LastAttemptAt:    nullString(st.LastAttemptAt),
			LastSuccessAt:    nullString(st.LastSuccessAt),
			LastOutcome:      st.LastOutcome,
			LastIDRotationAt: nullString(st.LastIDRotationAt),
		},
	}
	if destSends(dest) {
		resp.Destination.Host = nullString(dest.Host)
	}
	if c.Extended {
		resp.InstallID = nullString(st.InstallID)
	}
	if c.Basic && destSends(dest) {
		now := time.Now().UTC()
		extended := c.Extended && st.InstallID != "" && !telemetry.ExtWithheld(st, dest.URL, now)
		rep, err := telemetry.BuildReport(ctx, h.settings.Deps, extended, now)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		body, err := telemetryschema.Encode(rep)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		resp.Preview = body
	}
	writeJSON(w, resp)
}

// postInstallID serves POST /admin/telemetry/install-id (config:manage). It
// replaces the install ID, keeps the signing secret (so the new ID gets a new
// key) and the schedule, and returns the new ID. It returns 409 when extended
// is off or the operator disabled telemetry.
func (h *telemetryHandler) postInstallID(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	var id string
	err := inTx(ctx, h.db, func(tx *sql.Tx) error {
		if h.settings.Dest.Kind == telemetry.KindDisabled {
			return telemetry.ErrOperatorDisabled
		}
		c, err := telemetry.ReadConsentTx(ctx, tx)
		if err != nil {
			return err
		}
		if !c.Extended {
			return errExtendedOff
		}
		st, err := db.GetTelemetryStateTx(ctx, tx)
		if err != nil {
			return err
		}
		if id, err = telemetry.NewInstallID(); err != nil {
			return err
		}
		if err := db.EnsureSigningSecretTx(ctx, tx); err != nil {
			return err
		}
		return db.UpdateTelemetryConsentTx(ctx, tx, st.ConsentSource, id, st.NextDueAt)
	})
	switch {
	case errors.Is(err, telemetry.ErrOperatorDisabled), errors.Is(err, errExtendedOff):
		httperr.WriteCode(w, req, http.StatusConflict, err)
	case err != nil:
		httperr.Write(w, req, err)
	default:
		writeJSON(w, map[string]string{"installId": id})
	}
}

type noticeFields struct {
	Basic    []string `json:"basic"`
	Extended []string `json:"extended"`
}

// noticeResponse omits everything but pending while the notice isn't pending.
type noticeResponse struct {
	Pending     bool               `json:"pending"`
	Destination *noticeDestination `json:"destination,omitempty"`
	Fields      *noticeFields      `json:"fields,omitempty"`
}

// pending reports whether the notice is pending for u: u holds config:manage,
// a destination is in effect, no admin has made a choice yet (consent_source
// is "default"), and u has not dismissed it.
func (h *telemetryHandler) pending(ctx context.Context, u *auth.User) (bool, error) {
	if !u.Can("config:manage", false, "", "") {
		return false, nil
	}
	if h.settings.Dest.Kind == telemetry.KindDisabled || h.settings.Dest.Kind == telemetry.KindNone {
		return false, nil
	}
	st, err := h.db.GetTelemetryState(ctx)
	if err != nil {
		return false, err
	}
	if st.ConsentSource != db.TelemetryConsentDefault {
		return false, nil
	}
	acked, err := h.db.HasNoticeAck(ctx, u.ID)
	if err != nil {
		return false, err
	}
	return !acked, nil
}

func (h *telemetryHandler) getNotice(w http.ResponseWriter, req *http.Request) {
	u := auth.UserFromContext(req.Context())
	pending, err := h.pending(req.Context(), u)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !pending {
		writeJSON(w, noticeResponse{})
		return
	}
	writeJSON(w, noticeResponse{
		Pending:     true,
		Destination: &noticeDestination{Kind: h.settings.Dest.Kind, Host: h.settings.Dest.Host},
		Fields:      &noticeFields{Basic: noticeBasicFields, Extended: noticeExtendedFields},
	})
}

func (h *telemetryHandler) postNotice(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	u := auth.UserFromContext(ctx)
	if u == nil {
		httperr.WriteMessage(w, req, messages.New("server.unauthorized", nil))
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<10)).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	switch body.Action {
	case noticeSeen, noticeKeep, noticeExtendedOff, noticeAllOff:
	default:
		httperr.WriteMessage(w, req, messages.New("server.telemetry.invalid_notice_action", nil))
		return
	}
	pending, err := h.pending(ctx, u)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !pending {
		if body.Action == noticeSeen {
			w.WriteHeader(http.StatusNoContent) // a no-op, not an error
			return
		}
		httperr.WriteMessage(w, req, messages.New("server.telemetry.notice_not_pending", nil))
		return
	}
	err = inTx(ctx, h.db, func(tx *sql.Tx) error {
		return h.apply(ctx, tx, u.ID, body.Action)
	})
	switch {
	case errors.Is(err, telemetry.ErrOperatorDisabled):
		httperr.WriteCode(w, req, http.StatusConflict, err)
	case err != nil:
		httperr.Write(w, req, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// apply performs one notice action inside tx. seen records no ack; the other
// actions record the caller's ack in the same transaction as their effect.
func (h *telemetryHandler) apply(ctx context.Context, tx *sql.Tx, userID int64, action string) error {
	s := h.settings
	switch action {
	case noticeSeen:
		return telemetry.MarkNoticeSeen(ctx, tx, s.Dest, s.Interval)
	case noticeExtendedOff:
		c, err := telemetry.ReadConsentTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := telemetry.ApplyConsent(ctx, tx, s.Dest, s.Interval, c.Basic, false, db.TelemetryConsentAdmin); err != nil {
			return err
		}
	case noticeAllOff:
		if err := telemetry.ApplyConsent(ctx, tx, s.Dest, s.Interval, false, false, db.TelemetryConsentAdmin); err != nil {
			return err
		}
	}
	if err := db.InsertNoticeAckTx(ctx, tx, userID, action, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return nil
}

// inTx runs fn in a transaction on store and commits when fn succeeds. A
// SQLite store has one connection, so work that must commit together has to
// share the transaction rather than reach for the store.
func inTx(ctx context.Context, store *db.Store, fn func(tx *sql.Tx) error) error {
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
