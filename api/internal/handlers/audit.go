package handlers

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// MountAudit registers the audit log API handlers.
func MountAudit(r chi.Router, a *audit.Auditor) {
	r.Get("/admin/audit", func(w http.ResponseWriter, req *http.Request) {
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		// Clamp the untrusted limit value before passing to the store layer.
		if limit <= 0 || limit > audit.MaxAuditPageSize {
			limit = audit.DefaultAuditPageSize
		}
		before, _ := strconv.ParseInt(req.URL.Query().Get("before"), 10, 64)
		events, err := a.Page(req, limit, before)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		audit.WriteJSON(w, events)
	})

	// Verify re-walks the tamper-evidence hash chain and reports the first
	// broken link, if any — the backend half of detecting a DB-level
	// UPDATE/DELETE against audit_events. The dashboard integrity banner
	// that surfaces this is a separate follow-up.
	r.Get("/admin/audit/verify", func(w http.ResponseWriter, req *http.Request) {
		result, err := a.Verify(req.Context())
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		audit.WriteJSON(w, result)
	})

	// Export streams the audit log as a CSV or JSON download. Unlike the
	// paginated /admin/audit view, this gives the whole matching trail in one
	// request — for compliance archival or an external pipeline. Streams
	// row-by-row so a large table doesn't have to be buffered in memory.
	//
	// The optional actor/method/status filters mirror the dashboard's audit
	// page, so "filter, then export" yields those rows and no others.
	r.Get("/admin/audit/export", func(w http.ResponseWriter, req *http.Request) {
		format := strings.ToLower(req.URL.Query().Get("format"))
		if format == "" {
			format = "csv"
		}
		filter, err := parseStreamFilter(req)
		if err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, err)
			return
		}
		stamp := time.Now().UTC().Format("20060102-150405")

		switch format {
		case "csv":
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition",
				fmt.Sprintf(`attachment; filename="gameplane-audit-%s.csv"`, stamp))
			cw := csv.NewWriter(w)
			_ = cw.Write([]string{"id", "ts", "actor", "method", "path", "target", "status", "ip", "reason"})
			err := a.Stream(req.Context(), filter, func(e audit.Event) error {
				return cw.Write([]string{
					strconv.FormatInt(e.ID, 10), e.TS, e.Actor, e.Method, e.Path,
					e.Target, strconv.Itoa(e.Status), e.IP, e.Reason,
				})
			})
			cw.Flush()
			// Headers are already sent, so a mid-stream failure can't change the
			// status code; surface it in the log so the operator knows the export
			// is truncated rather than complete.
			if err != nil {
				slog.Warn("audit export failed mid-stream", "format", "csv", "err", err)
			}
		case "json":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition",
				fmt.Sprintf(`attachment; filename="gameplane-audit-%s.json"`, stamp))
			bw := bufio.NewWriter(w)
			_ = bw.WriteByte('[')
			first := true
			err := a.Stream(req.Context(), filter, func(e audit.Event) error {
				if !first {
					_ = bw.WriteByte(',')
				}
				first = false
				b, mErr := json.Marshal(e)
				if mErr != nil {
					return mErr
				}
				_, wErr := bw.Write(b)
				return wErr
			})
			_ = bw.WriteByte(']')
			_ = bw.Flush()
			if err != nil {
				slog.Warn("audit export failed mid-stream", "format", "json", "err", err)
			}
		default:
			httperr.WriteCode(w, req, http.StatusBadRequest,
				errors.New("format must be csv or json"))
		}
	})
}

// exportMethods is the set a caller may filter on. Restricting it keeps an
// arbitrary string out of the query and matches the dashboard's dropdown.
var exportMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// parseStreamFilter maps the export query string onto an audit.StreamFilter.
// Unknown or empty values mean "no filter" rather than "match nothing", so a
// bare /admin/audit/export still exports the whole table.
func parseStreamFilter(req *http.Request) (audit.StreamFilter, error) {
	q := req.URL.Query()
	f := audit.StreamFilter{
		Since: q.Get("since"),
		Until: q.Get("until"),
		Actor: strings.TrimSpace(q.Get("actor")),
	}

	if m := strings.ToUpper(strings.TrimSpace(q.Get("method"))); m != "" {
		if !exportMethods[m] {
			return f, fmt.Errorf("unsupported method filter %q", m)
		}
		f.Method = m
	}

	// A status *class* ("2xx") rather than a code: the dashboard groups by
	// class, and an exact code is already reachable via the JSON export.
	switch s := strings.ToLower(strings.TrimSpace(q.Get("status"))); s {
	case "":
	case "2xx":
		f.StatusMin, f.StatusMax = 200, 299
	case "4xx":
		f.StatusMin, f.StatusMax = 400, 499
	case "5xx":
		f.StatusMin, f.StatusMax = 500, 599
	default:
		return f, fmt.Errorf("status must be one of 2xx, 4xx, 5xx (got %q)", s)
	}
	return f, nil
}
