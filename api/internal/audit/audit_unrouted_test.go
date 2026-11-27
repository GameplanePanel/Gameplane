package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func TestTruncateUTF8(t *testing.T) {
	if got := truncateUTF8("abc", 10); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := truncateUTF8(strings.Repeat("a", 600), 512); len(got) != 512 {
		t.Fatalf("len = %d, want 512", len(got))
	}
	// "é" is 2 bytes; cutting at 3 would split the second rune.
	if got := truncateUTF8("ééé", 3); got != "é" {
		t.Fatalf("got %q, want %q", got, "é")
	}
}

func routerWithAudit(t *testing.T) (*db.Store, http.Handler) {
	t.Helper()
	s := newStore(t)
	r := chi.NewRouter()
	r.Use(Middleware(New(s)))
	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)
	r.Post("/known", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Post("/known/*", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Route("/sub", func(sr chi.Router) {
		sr.Post("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	return s, r
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	return rec
}

func TestRouterUnknownRootPathWritesNoRow(t *testing.T) {
	s, h := routerWithAudit(t)
	if rec := serve(h, http.MethodPost, "/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if n := countEvents(t, s); n != 0 {
		t.Fatalf("rows = %d, want 0", n)
	}
}

func TestRouterUnknownSubrouterPathWritesNoRow(t *testing.T) {
	s, h := routerWithAudit(t)
	if rec := serve(h, http.MethodPost, "/sub/garbage"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := serve(h, http.MethodDelete, "/sub/ok"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if n := countEvents(t, s); n != 0 {
		t.Fatalf("rows = %d, want 0", n)
	}
}

func TestRouterRoutedAnonymousPostWritesOneRow(t *testing.T) {
	s, h := routerWithAudit(t)
	serve(h, http.MethodPost, "/known")
	serve(h, http.MethodPost, "/sub/ok")
	if n := countEvents(t, s); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	var actor string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT actor FROM audit_events WHERE path = '/known'`).Scan(&actor); err != nil {
		t.Fatalf("query: %v", err)
	}
	if actor != "anonymous" {
		t.Fatalf("actor = %q", actor)
	}
}

func TestRouterTruncationReachesStoredRow(t *testing.T) {
	s, h := routerWithAudit(t)
	serve(h, http.MethodPost, "/known/"+strings.Repeat("a", 600)+"?name="+strings.Repeat("b", 600))
	var path, target string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT path, COALESCE(target,'') FROM audit_events`).Scan(&path, &target); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(path) != 512 || len(target) != 512 {
		t.Fatalf("path=%d target=%d, want 512 each", len(path), len(target))
	}
}

func TestRouterMethodNotAllowedKeepsAllowHeader(t *testing.T) {
	_, h := routerWithAudit(t)
	rec := serve(h, http.MethodDelete, "/sub/ok")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Values("Allow"); len(got) != 1 || got[0] != http.MethodPost {
		t.Fatalf("Allow = %v, want [POST]", got)
	}
}
