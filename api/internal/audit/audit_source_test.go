package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// srcLegacyHash reproduces the hash rule for rows without a source (the rule before spec 040):
// each field as "len:value", Reason only when non-empty, chained after the previous hash.
func srcLegacyHash(prev string, e Event) string {
	var buf bytes.Buffer
	field := func(s string) {
		fmt.Fprintf(&buf, "%d:", len(s))
		buf.WriteString(s)
	}
	for _, s := range []string{e.TS, e.Actor, e.Method, e.Path, e.Target, strconv.Itoa(e.Status), e.IP} {
		field(s)
	}
	if e.Reason != "" {
		field(e.Reason)
	}
	sum := sha256.New()
	sum.Write([]byte(prev))
	sum.Write(buf.Bytes())
	return hex.EncodeToString(sum.Sum(nil))
}

func TestSourcelessRowsHashAsBefore(t *testing.T) {
	cases := []Event{
		{TS: "2026-01-01T00:00:00Z", Actor: "alice", Method: "POST", Path: "/servers/a/start", Target: "a", Status: 202, IP: "10.0.0.1"},
		{TS: "2026-01-01T00:00:01Z", Actor: "bob", Method: "DELETE", Path: "/servers/a", Status: 403, IP: "10.0.0.2", Reason: "forbidden"},
	}
	for _, e := range cases {
		if got, want := computeHash("prev", e), srcLegacyHash("prev", e); got != want {
			t.Errorf("hash of %+v = %s, want %s", e, got, want)
		}
	}
}

func TestSourceIsHashedAndDistinctFromReason(t *testing.T) {
	base := Event{TS: "2026-01-01T00:00:00Z", Actor: "alice", Method: "POST", Path: "/sftp/v1/login", Target: "local", Status: 200}
	withReason := base
	withReason.Reason = "sftp"
	withSource := base
	withSource.Source = "sftp"
	if computeHash("", withReason) == computeHash("", withSource) {
		t.Error("Reason=sftp and Source=sftp hash identically")
	}
	if computeHash("", base) == computeHash("", withSource) {
		t.Error("a source does not change the hash")
	}
}

func TestSourceChainVerifies(t *testing.T) {
	store := newStore(t)
	a := New(store)
	ctx := t.Context()
	events := []Event{
		{TS: "2026-01-01T00:00:00Z", Actor: "alice", Method: "POST", Path: "/servers/a/files/write", Target: "local/gameplane-games/a /x", Status: 200, IP: "10.0.0.1"},
		{TS: "2026-01-01T00:00:01Z", Actor: "alice", Method: "PUT", Path: "/servers/a/files/stream", Target: "local/gameplane-games/a /y", Status: 200, IP: "10.0.0.2", Source: "sftp"},
		{TS: "2026-01-01T00:00:02Z", Actor: "bob", Method: "POST", Path: "/servers/a/start", Status: 500, IP: "10.0.0.3", Reason: "denied"},
	}
	for _, e := range events {
		if err := a.insertChainedEvent(ctx, e); err != nil {
			t.Fatalf("insert %+v: %v", e, err)
		}
	}
	res, err := a.Verify(ctx)
	if err != nil || !res.OK || res.Checked != int64(len(events)) {
		t.Fatalf("Verify = %+v, %v; want OK with %d rows", res, err, len(events))
	}

	ids := srcRowIDs(t, store)
	if len(ids) != len(events) {
		t.Fatalf("rows = %d, want %d", len(ids), len(events))
	}
	var nulls int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE source IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 2 {
		t.Errorf("rows with NULL source = %d, want 2 (dashboard rows stay NULL)", nulls)
	}

	// Rewriting a dashboard row's source must break the chain at that row.
	if _, err := store.DB.ExecContext(ctx, `UPDATE audit_events SET source = 'sftp' WHERE id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	res, err = a.Verify(ctx)
	if err != nil || res.OK || res.FirstBadID != ids[0] {
		t.Errorf("Verify after source tamper = %+v, %v; want break at row %d", res, err, ids[0])
	}
}

func TestSourceReadBackFromPageAndStream(t *testing.T) {
	store := newStore(t)
	a := New(store)
	ctx := t.Context()
	if err := a.insertChainedEvent(ctx, Event{TS: "2026-01-01T00:00:00Z", Actor: "alice", Method: "POST", Path: "/a", Status: 200}); err != nil {
		t.Fatal(err)
	}
	if err := a.insertChainedEvent(ctx, Event{TS: "2026-01-01T00:00:01Z", Actor: "alice", Method: "POST", Path: "/b", Status: 200, Source: "sftp"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/audit", nil)
	page, err := a.Page(req, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourcesOf(page); got != "|sftp" && got != "sftp|" {
		t.Errorf("Page sources = %q", got)
	}

	var streamed []Event
	if err := a.Stream(ctx, StreamFilter{}, func(e Event) error {
		streamed = append(streamed, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 2 || streamed[0].Source != "" || streamed[1].Source != "sftp" {
		t.Errorf("Stream sources = %+v", streamed)
	}
}

func TestSourceInSinkPayloads(t *testing.T) {
	e := Event{TS: "2026-01-01T00:00:00Z", Actor: "alice", Method: "POST", Path: "/sftp/v1/login", Status: 200, Source: "sftp"}

	var withSource bytes.Buffer
	encodeEvent(&withSource, e)
	if !strings.Contains(withSource.String(), `"source":"sftp"`) {
		t.Errorf("S3 line = %s, want source", withSource.String())
	}
	plain := e
	plain.Source = ""
	var without bytes.Buffer
	encodeEvent(&without, plain)
	if strings.Contains(without.String(), "source") {
		t.Errorf("S3 line without source = %s", without.String())
	}

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	sink := NewWebhookSink(srv.URL, "")
	sink.post(t.Context(), e)
	if !strings.Contains(got, `"source":"sftp"`) {
		t.Errorf("webhook body = %s, want source", got)
	}
	sink.post(t.Context(), plain)
	if strings.Contains(got, "source") {
		t.Errorf("webhook body without source = %s", got)
	}
}

func TestMiddlewareRecordsSourceAndTarget(t *testing.T) {
	store := newStore(t)
	a := New(store)

	sftpHandler := Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.SetSource(r.Context(), "sftp")
		auth.SetAuditTarget(r.Context(), "local/gameplane-games/srv /data/world")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/srv/files/write?name=ignored", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{Username: "alice"}))
	sftpHandler.ServeHTTP(httptest.NewRecorder(), req)

	dashboardHandler := Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/srv/files/write?name=ignored", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{Username: "alice"}))
	dashboardHandler.ServeHTTP(httptest.NewRecorder(), req)

	rows, err := store.DB.QueryContext(t.Context(), `SELECT actor, target, COALESCE(source,'') FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type row struct{ actor, target, source string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.actor, &r.target, &r.source); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []row{
		{"alice", "local/gameplane-games/srv /data/world", "sftp"},
		{"alice", "ignored", ""},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

func TestWriteSyncAsDerivesSourceFromSFTPCaller(t *testing.T) {
	store := newStore(t)
	a := New(store)
	sftpCtx := auth.WithSFTPCaller(t.Context(), auth.SFTPCaller{Cluster: "local", ServiceTokenID: 1})
	if err := a.WriteSyncAs(sftpCtx, "alice", http.MethodPost, "/sftp/v1/login", "local/gameplane-games/srv", "", http.StatusOK); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteSync(t.Context(), http.MethodPost, "/users/me/sftp/password", "", "", http.StatusOK); err != nil {
		t.Fatal(err)
	}
	events := []Event{}
	if err := a.Stream(t.Context(), StreamFilter{}, func(e Event) error {
		events = append(events, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Actor != "alice" || events[0].Source != "sftp" || events[0].Target != "local/gameplane-games/srv" {
		t.Errorf("SFTP row = %+v", events[0])
	}
	if events[1].Source != "" {
		t.Errorf("dashboard WriteSync row source = %q, want empty", events[1].Source)
	}
}

func TestShouldLogSkipsSFTPSessionCheck(t *testing.T) {
	check := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sftp/v1/sessions/check", nil)
	if shouldLog(check) {
		t.Error("sessions/check is audited, want skipped")
	}
	write := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/srv/files/write", nil)
	if !shouldLog(write) {
		t.Error("file write is not audited")
	}
}

// srcRowIDs returns the ids of every audit row, oldest first.
func srcRowIDs(t *testing.T, store *db.Store) []int64 {
	t.Helper()
	rows, err := store.DB.QueryContext(t.Context(), `SELECT id FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// sourcesOf joins the Source of each event with "|" for a compact comparison.
func sourcesOf(events []Event) string {
	parts := make([]string, 0, len(events))
	for _, e := range events {
		parts = append(parts, e.Source)
	}
	return strings.Join(parts, "|")
}
