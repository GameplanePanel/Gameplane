package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// webFS holds the dashboard templates and static files (no JavaScript).
// The templates and static files are described in specs 022 T068 and T076.
//
//go:embed web
var webFS embed.FS

const (
	sessionCookie = "gp_telemetry_session"
	// sessionMaxAge is the cookie lifetime in seconds (12 hours).
	sessionMaxAge = 43200
	sessionTTL    = sessionMaxAge * time.Second
	// sessionKeyLabel is the HMAC message that derives the cookie key K from
	// DASHBOARD_TOKEN (research R8).
	sessionKeyLabel = "gameplane-telemetry-session"
	// loginPerMinute is the login attempt limit per source (research R6).
	loginPerMinute = 5
	// maxLoginBody bounds the login form body.
	maxLoginBody = 4 << 10

	// dashboardCSP is sent on every dashboard response.
	dashboardCSP = "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

	// loginFailedMessage is the only login failure message; it never says
	// whether the token was close, missing or unconfigured.
	loginFailedMessage = "Invalid credentials"
	unauthorizedBody   = `{"error":"unauthorized"}`
)

// staticFile holds a preloaded static file with its content type.
type staticFile struct {
	ctype string
	data  []byte
}

// dashboard serves the private dashboard listener.
type dashboard struct {
	s           *server
	token       string
	key         []byte
	pages       map[string]*template.Template
	staticFiles map[string]staticFile
	logins      *bucketLimiter
}

// dashboardRoutes returns the handler of the dashboard listener. It is only
// mounted when DASHBOARD_TOKEN is set; with an empty token every
// authenticated route refuses, so it can never be open.
func (s *server) dashboardRoutes() http.Handler {
	static, err := fs.Sub(webFS, "web/static")
	if err != nil {
		panic(fmt.Sprintf("telemetry-receiver: embedded static files: %v", err))
	}
	staticFiles := make(map[string]staticFile)
	entries, err := fs.ReadDir(static, ".")
	if err != nil {
		panic(fmt.Sprintf("telemetry-receiver: read static files: %v", err))
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			data, err := fs.ReadFile(static, name)
			if err != nil {
				panic(fmt.Sprintf("telemetry-receiver: read static file %s: %v", name, err))
			}
			ctype := mime.TypeByExtension(path.Ext(name))
			if ctype == "" {
				ctype = "application/octet-stream"
			}
			staticFiles[name] = staticFile{ctype: ctype, data: data}
		}
	}
	d := &dashboard{
		s:           s,
		token:       s.cfg.dashboardToken,
		key:         sessionKey(s.cfg.dashboardToken),
		pages:       make(map[string]*template.Template),
		staticFiles: staticFiles,
		logins:      newBucketLimiter(loginPerMinute, loginPerMinute),
	}
	pageFiles := map[string][]string{
		"login":    {"web/layout.html", "web/login.html"},
		"overview": {"web/layout.html", "web/charts.html", "web/overview.html", "web/overview_extended.html"},
	}
	for page, files := range pageFiles {
		d.pages[page] = template.Must(template.New(page).Funcs(pageFuncs).ParseFS(webFS, files...))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", d.loginPage)
	mux.HandleFunc("POST /login", d.loginSubmit)
	mux.HandleFunc("POST /logout", d.logout)
	mux.HandleFunc("GET /{$}", d.overview)
	mux.HandleFunc("GET /api/v1/views", d.apiViews)
	mux.HandleFunc("GET /metrics", d.metrics)
	mux.HandleFunc("GET /static/{file}", d.serveStatic)
	return secureHeaders(mux)
}

// secureHeaders sets the headers every dashboard response carries, including
// the mux's own 404 and 405 answers.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", dashboardCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// sessionKey derives K = HMAC-SHA256(token, "gameplane-telemetry-session").
func sessionKey(token string) []byte {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(sessionKeyLabel))
	return m.Sum(nil)
}

// tokenEqual compares in constant time. Hashing first makes the comparison
// independent of the lengths.
func tokenEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// sessionMAC returns HMAC-SHA256(K, expiry).
func (d *dashboard) sessionMAC(expiry string) []byte {
	m := hmac.New(sha256.New, d.key)
	m.Write([]byte(expiry))
	return m.Sum(nil)
}

// newSessionValue returns the cookie value expiry || HMAC(K, expiry), written
// as "<expiry in Unix seconds>.<base64url of the MAC>".
func (d *dashboard) newSessionValue(now time.Time) string {
	expiry := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return expiry + "." + base64.RawURLEncoding.EncodeToString(d.sessionMAC(expiry))
}

// sessionValid reports whether the request carries an unexpired cookie
// signed with the key derived from the current token.
func (d *dashboard) sessionValid(r *http.Request) bool {
	if d.token == "" {
		return false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	expiry, encoded, found := strings.Cut(c.Value, ".")
	if !found {
		return false
	}
	mac, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal(mac, d.sessionMAC(expiry)) {
		return false
	}
	until, err := strconv.ParseInt(expiry, 10, 64)
	return err == nil && until > d.s.now().Unix()
}

// bearerValid reports whether the request has Authorization: Bearer <token>.
func (d *dashboard) bearerValid(r *http.Request) bool {
	if d.token == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && tokenEqual(got, d.token)
}

// sameOrigin requires an Origin (or, failing that, Referer) whose host is the
// request's own Host. A missing or "null" origin is refused.
func sameOrigin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		raw = r.Header.Get("Referer")
	}
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (d *dashboard) render(w http.ResponseWriter, status int, page string, data any) {
	var buf bytes.Buffer
	if err := d.pages[page].ExecuteTemplate(&buf, "layout", data); err != nil {
		slog.Error("render dashboard page", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(unauthorizedBody))
}

type loginData struct{ Error string }

func (d *dashboard) loginPage(w http.ResponseWriter, _ *http.Request) {
	d.render(w, http.StatusOK, "login", loginData{})
}

func (d *dashboard) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if !d.logins.allow(sourceIP(r, d.s.cfg.trustedProxyCIDRs), d.s.now()) {
		d.s.rateLimited.WithLabelValues(routeLogin).Inc()
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := r.ParseForm(); err != nil || d.token == "" || !tokenEqual(r.PostForm.Get("token"), d.token) {
		d.render(w, http.StatusUnauthorized, "login", loginData{Error: loginFailedMessage})
		return
	}
	now := d.s.now()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    d.newSessionValue(now),
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d *dashboard) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// rangeParam reads ?range=; invalid or missing values become defaultRange.
func rangeParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("range"))
	if err != nil {
		return defaultRange
	}
	return normalizeRange(n)
}

func (d *dashboard) overview(w http.ResponseWriter, r *http.Request) {
	if !d.sessionValid(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	v, err := BuildViews(r.Context(), d.s.store, rangeParam(r), d.s.now())
	if err != nil {
		slog.Error("build views", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	total, since := d.collectionMeta(r.Context())
	d.render(w, http.StatusOK, "overview", newOverviewPage(v, r.URL.Query().Get("view") == "table", total, since))
}

// collectionMeta reads the running report total and the collection start day
// for the overview tiles. A missing or unreadable value shows as 0 or empty
// rather than failing the page.
func (d *dashboard) collectionMeta(ctx context.Context) (int64, string) {
	var total int64
	if raw, ok, err := d.s.store.metaGet(ctx, metaReportsTotal); err == nil && ok {
		total, _ = strconv.ParseInt(raw, 10, 64)
	}
	var since string
	if raw, ok, err := d.s.store.metaGet(ctx, metaCollectionStart); err == nil && ok {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = dayString(t)
		}
	}
	return total, since
}

func (d *dashboard) apiViews(w http.ResponseWriter, r *http.Request) {
	if !d.sessionValid(r) && !d.bearerValid(r) {
		writeUnauthorized(w)
		return
	}
	v, err := BuildViews(r.Context(), d.s.store, rangeParam(r), d.s.now())
	if err != nil {
		slog.Error("build views", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("encode views", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// metrics serves the Prometheus registry to a Bearer token only; the session
// cookie is not accepted here (FR-030, spec Q8).
func (d *dashboard) metrics(w http.ResponseWriter, r *http.Request) {
	if !d.bearerValid(r) {
		writeUnauthorized(w)
		return
	}
	promhttp.HandlerFor(d.s.reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

// serveStatic serves one preloaded embedded static file with io.Copy rather
// than http.ServeContent or http.ServeFileFS, whose error responses drop the
// Cache-Control header that every dashboard response must carry.
func (d *dashboard) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	f, ok := d.staticFiles[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", f.ctype)
	_, _ = io.Copy(w, bytes.NewReader(f.data))
}
