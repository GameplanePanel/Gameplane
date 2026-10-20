package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	dashToken  = "dash-token-for-tests"
	dashOrigin = "http://example.com" // httptest.NewRequest uses Host example.com
)

var dashNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// newDash builds a receiver with a fixed clock and returns it with its
// dashboard handler.
func newDash(t *testing.T, cfg config) (*server, http.Handler) {
	t.Helper()
	if cfg.dashboardToken == "" {
		cfg.dashboardToken = dashToken
	}
	s := newServer(cfg)
	s.now = func() time.Time { return dashNow }
	return s, s.dashboardRoutes()
}

// seedDash stores reports on today (2026-10-06, the as-of day) and the day
// before it.
func seedDash(t *testing.T, s *server) {
	t.Helper()
	for _, r := range []struct {
		day, version string
		servers, tpl int
	}{
		{"2026-10-06", "1.0.0", 7, 21},
		{"2026-10-06", "1.0.0", 3, 4},
		{"2026-10-05", "2.3.4", 99, 98},
	} {
		if err := s.store.recordBasic(context.Background(), r.day, r.version, r.servers, r.tpl); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

type dashReq struct {
	method, path string
	header       map[string]string
	body         string
	cookie       *http.Cookie
	remote       string
}

func doDash(t *testing.T, h http.Handler, r dashReq) *httptest.ResponseRecorder {
	t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, strings.NewReader(r.body))
	if r.body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	if r.cookie != nil {
		req.AddCookie(r.cookie)
	}
	if r.remote != "" {
		req.RemoteAddr = r.remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func loginForm(token string) string { return url.Values{"token": {token}}.Encode() }

func sameOriginHeader() map[string]string { return map[string]string{"Origin": dashOrigin} }

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// signIn logs in with the right token and returns the session cookie.
func signIn(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm(dashToken)})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("login: %d Location %q, want 303 /", rec.Code, rec.Header().Get("Location"))
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	t.Fatal("login set no session cookie")
	return nil
}

func TestDashboardRefusalInvariant(t *testing.T) {
	_, empty := newDash(t, config{})
	populated, full := newDash(t, config{})
	seedDash(t, populated)
	wrongOrigin := map[string]string{"Origin": "http://evil.example"}
	cases := []struct {
		name string
		req  dashReq
		code int
	}{
		{"login page", dashReq{path: "/login"}, http.StatusOK},
		{"wrong token", dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm("nope")}, http.StatusUnauthorized},
		{"login without origin", dashReq{method: http.MethodPost, path: "/login", body: loginForm("nope")}, http.StatusForbidden},
		{"login cross origin", dashReq{method: http.MethodPost, path: "/login", header: wrongOrigin, body: loginForm(dashToken)}, http.StatusForbidden},
		{"overview", dashReq{path: "/"}, http.StatusSeeOther},
		{"overview range 7", dashReq{path: "/?range=7"}, http.StatusSeeOther},
		{"overview bad range", dashReq{path: "/?range=bogus"}, http.StatusSeeOther},
		{"views", dashReq{path: "/api/v1/views"}, http.StatusUnauthorized},
		{"views wrong bearer", dashReq{path: "/api/v1/views", header: bearer("nope")}, http.StatusUnauthorized},
		{"views forged cookie", dashReq{path: "/api/v1/views", cookie: &http.Cookie{Name: sessionCookie, Value: "AAAA"}}, http.StatusUnauthorized},
		{"metrics", dashReq{path: "/metrics"}, http.StatusUnauthorized},
		{"metrics wrong bearer", dashReq{path: "/metrics", header: bearer("nope")}, http.StatusUnauthorized},
		{"logout", dashReq{method: http.MethodPost, path: "/logout", header: sameOriginHeader()}, http.StatusSeeOther},
		{"unknown path", dashReq{path: "/admin"}, http.StatusNotFound},
		{"unknown static file", dashReq{path: "/static/missing.css"}, http.StatusNotFound},
		{"template is not static", dashReq{path: "/static/overview.html"}, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := doDash(t, empty, tc.req), doDash(t, full, tc.req)
			if a.Code != tc.code || b.Code != tc.code {
				t.Fatalf("status = %d (empty) / %d (populated), want %d", a.Code, b.Code, tc.code)
			}
			if a.Body.String() != b.Body.String() {
				t.Fatalf("body differs between an empty and a populated store:\nempty:     %q\npopulated: %q", a.Body, b.Body)
			}
			for _, h := range []string{"Content-Type", "Location", "Set-Cookie", "Content-Security-Policy", "Cache-Control"} {
				if a.Header().Get(h) != b.Header().Get(h) {
					t.Fatalf("header %s differs: %q vs %q", h, a.Header().Get(h), b.Header().Get(h))
				}
			}
			// Nothing from the data may appear in a refusal.
			for _, leak := range []string{"2026-10-0", "1.0.0", "2.3.4", "99"} {
				if strings.Contains(b.Body.String(), leak) {
					t.Fatalf("refusal body contains %q: %q", leak, b.Body)
				}
			}
		})
	}
}

func TestDashboardLoginFlow(t *testing.T) {
	_, h := newDash(t, config{})

	page := doDash(t, h, dashReq{path: "/login"})
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="token"`) ||
		!strings.Contains(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("login page: %d %q", page.Code, page.Body)
	}
	if strings.Contains(page.Body.String(), loginFailedMessage) {
		t.Fatal("a fresh login page must not show the error")
	}

	bad := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm("wrong")})
	if bad.Code != http.StatusUnauthorized || !strings.Contains(bad.Body.String(), loginFailedMessage) {
		t.Fatalf("wrong token: %d %q", bad.Code, bad.Body)
	}
	if len(bad.Result().Cookies()) != 0 {
		t.Fatal("a failed login must not set a cookie")
	}

	ok := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm(dashToken)})
	if ok.Code != http.StatusSeeOther || ok.Header().Get("Location") != "/" {
		t.Fatalf("login: %d Location %q", ok.Code, ok.Header().Get("Location"))
	}
	cookies := ok.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != "gp_telemetry_session" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode ||
		c.Path != "/" || c.MaxAge != 43200 || c.Value == "" {
		t.Fatalf("cookie attributes = %+v", c)
	}

	session := &http.Cookie{Name: c.Name, Value: c.Value}
	home := doDash(t, h, dashReq{path: "/", cookie: session})
	if home.Code != http.StatusOK || !strings.Contains(home.Header().Get("Content-Type"), "text/html") || home.Body.Len() == 0 {
		t.Fatalf("overview with session: %d", home.Code)
	}
	redirect := doDash(t, h, dashReq{path: "/"})
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "/login" {
		t.Fatalf("overview without session: %d Location %q", redirect.Code, redirect.Header().Get("Location"))
	}

	out := doDash(t, h, dashReq{method: http.MethodPost, path: "/logout", header: sameOriginHeader(), cookie: session})
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/login" {
		t.Fatalf("logout: %d Location %q", out.Code, out.Header().Get("Location"))
	}
	cleared := out.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != sessionCookie || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie, got %+v", cleared)
	}
}

func TestDashboardOriginChecks(t *testing.T) {
	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"no origin or referer", nil, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"null origin", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"unparsable origin", map[string]string{"Origin": "::bad"}, http.StatusForbidden},
		{"foreign referer", map[string]string{"Referer": "http://evil.example/login"}, http.StatusForbidden},
		{"same-origin origin", map[string]string{"Origin": dashOrigin}, http.StatusSeeOther},
		{"same-origin referer", map[string]string{"Referer": dashOrigin + "/login"}, http.StatusSeeOther},
		{"origin wins over referer", map[string]string{"Origin": "http://evil.example", "Referer": dashOrigin + "/login"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run("login "+tc.name, func(t *testing.T) {
			_, h := newDash(t, config{}) // a fresh login limiter per case
			rec := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: tc.header, body: loginForm(dashToken)})
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
		t.Run("logout "+tc.name, func(t *testing.T) {
			_, h := newDash(t, config{})
			rec := doDash(t, h, dashReq{method: http.MethodPost, path: "/logout", header: tc.header})
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestDashboardRotatingTheTokenInvalidatesSessionsAndKeepsData(t *testing.T) {
	s, h := newDash(t, config{dashboardToken: "old-token"})
	seedDash(t, s)
	login := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm("old-token")})
	if login.Code != http.StatusSeeOther {
		t.Fatalf("login with the old token: %d", login.Code)
	}
	old := &http.Cookie{Name: sessionCookie, Value: login.Result().Cookies()[0].Value}
	if rec := doDash(t, h, dashReq{path: "/api/v1/views", cookie: old}); rec.Code != http.StatusOK {
		t.Fatalf("old cookie before rotation: %d", rec.Code)
	}

	// Same store, new token.
	rotated := newServerWithStore(config{dashboardToken: "new-token"}, s.store)
	rotated.now = s.now
	h2 := rotated.dashboardRoutes()
	if rec := doDash(t, h2, dashReq{path: "/", cookie: old}); rec.Code != http.StatusSeeOther {
		t.Fatalf("old cookie after rotation, overview: %d, want 303", rec.Code)
	}
	if rec := doDash(t, h2, dashReq{path: "/api/v1/views", cookie: old}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie after rotation, views: %d, want 401", rec.Code)
	}
	if rec := doDash(t, h2, dashReq{path: "/api/v1/views", header: bearer("old-token")}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old bearer after rotation: %d, want 401", rec.Code)
	}
	rec := doDash(t, h2, dashReq{path: "/api/v1/views", header: bearer("new-token")})
	if rec.Code != http.StatusOK {
		t.Fatalf("new bearer: %d", rec.Code)
	}
	var v Views
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Empty || v.Basic == nil || v.Basic.LatestDay.ServersTotal != 10 {
		t.Fatalf("data lost after rotation: %+v", v)
	}
}
func TestDashboardRejectsExpiredTamperedAndMalformedCookies(t *testing.T) {
	s, h := newDash(t, config{})
	now := dashNow
	s.now = func() time.Time { return now }
	good := signIn(t, h)
	if rec := doDash(t, h, dashReq{path: "/", cookie: good}); rec.Code != http.StatusOK {
		t.Fatalf("fresh cookie: %d", rec.Code)
	}
	// Change a character inside the MAC, away from its last character, which
	// carries base64 padding bits that do not affect the decoded bytes.
	flip := func(v string) string {
		i := len(v) - 5
		c := "A"
		if v[i:i+1] == "A" {
			c = "B"
		}
		return v[:i] + c + v[i+1:]
	}
	for name, value := range map[string]string{
		"tampered mac":    flip(good.Value),
		"tampered expiry": "9" + good.Value,
		"no separator":    strings.ReplaceAll(good.Value, ".", ""),
		"not base64":      "!!!",
		"too short":       "AAAA",
		"empty":           "",
		"truncated":       good.Value[:len(good.Value)-4],
		"extra trailing":  good.Value + "AAAA",
	} {
		rec := doDash(t, h, dashReq{path: "/", cookie: &http.Cookie{Name: sessionCookie, Value: value}})
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s: status = %d, want 303", name, rec.Code)
		}
	}
	now = now.Add(12*time.Hour - time.Second)
	if rec := doDash(t, h, dashReq{path: "/", cookie: good}); rec.Code != http.StatusOK {
		t.Fatalf("cookie just before expiry: %d", rec.Code)
	}
	now = now.Add(time.Second)
	if rec := doDash(t, h, dashReq{path: "/", cookie: good}); rec.Code != http.StatusSeeOther {
		t.Fatalf("expired cookie: %d, want 303", rec.Code)
	}
}

func TestDashboardSecurityHeadersOnEveryResponse(t *testing.T) {
	_, h := newDash(t, config{})
	session := signIn(t, h)
	want := map[string]string{
		"Content-Security-Policy": "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	}
	for name, r := range map[string]dashReq{
		"login page":       {path: "/login"},
		"failed login":     {method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm("x")},
		"forbidden login":  {method: http.MethodPost, path: "/login"},
		"redirect":         {path: "/"},
		"overview":         {path: "/", cookie: session},
		"views":            {path: "/api/v1/views", cookie: session},
		"unauthorized":     {path: "/api/v1/views"},
		"metrics":          {path: "/metrics", header: bearer(dashToken)},
		"metrics refused":  {path: "/metrics"},
		"method":           {method: http.MethodPost, path: "/metrics"},
		"static":           {path: "/static/style.css"},
		"static missing":   {path: "/static/nope.css"},
		"static traversal": {path: "/static/..%2Flogin.html"},
		"not found":        {path: "/nope"},
		"logout":           {method: http.MethodPost, path: "/logout", header: sameOriginHeader()},
	} {
		rec := doDash(t, h, r)
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s: header %s = %q, want %q", name, k, got, v)
			}
		}
	}
}

func TestDashboardLoginLimitIsFivePerMinutePerSource(t *testing.T) {
	s, h := newDash(t, config{})
	attempt := func(remote string) *httptest.ResponseRecorder {
		return doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm("wrong"), remote: remote})
	}
	for i := range 5 {
		if rec := attempt("192.0.2.1:1000"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	limited := attempt("192.0.2.1:2000")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("sixth attempt: %d Retry-After %q, want 429 with Retry-After", limited.Code, limited.Header().Get("Retry-After"))
	}
	// The limit also stops a correct token.
	right := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm(dashToken), remote: "192.0.2.1:3000"})
	if right.Code != http.StatusTooManyRequests {
		t.Fatalf("correct token while limited: %d, want 429", right.Code)
	}
	if rec := attempt("192.0.2.9:1000"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("another source: %d, want 401", rec.Code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "login"); got != 2 {
		t.Fatalf("rate_limited_total{route=login} = %v, want 2", got)
	}
}

func TestDashboardLoginLimitUsesForwardedSourceFromTrustedProxy(t *testing.T) {
	_, h := newDash(t, config{trustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	attempt := func(xff string) int {
		return doDash(t, h, dashReq{
			method: http.MethodPost, path: "/login", remote: "10.0.0.1:1000",
			header: map[string]string{"Origin": dashOrigin, "X-Forwarded-For": xff}, body: loginForm("wrong"),
		}).Code
	}
	for range 5 {
		if code := attempt("203.0.113.5"); code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", code)
		}
	}
	if code := attempt("203.0.113.5"); code != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt from the same client: %d, want 429", code)
	}
	if code := attempt("203.0.113.6"); code != http.StatusUnauthorized {
		t.Fatalf("another client behind the proxy: %d, want 401", code)
	}
}

func TestDashboardViewsEndpoint(t *testing.T) {
	s, h := newDash(t, config{})
	seedDash(t, s)
	session := signIn(t, h)

	for name, r := range map[string]dashReq{
		"bearer": {path: "/api/v1/views", header: bearer(dashToken)},
		"cookie": {path: "/api/v1/views", cookie: session},
	} {
		rec := doDash(t, h, r)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s: %d %q", name, rec.Code, rec.Header().Get("Content-Type"))
		}
		var v Views
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.Range != 30 || v.AsOf != "2026-10-06" || v.Empty || v.Basic == nil {
			t.Fatalf("%s: %+v", name, v)
		}
		if want := (LatestDayRow{ServersTotal: 10, TemplatesTotal: 25}); v.Basic.LatestDay != want {
			t.Fatalf("%s: latestDay = %+v, want %+v", name, v.Basic.LatestDay, want)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if string(raw["extended"]) != "null" {
			t.Fatalf("%s: extended = %s, want null", name, raw["extended"])
		}
		var basic map[string]json.RawMessage
		if err := json.Unmarshal(raw["basic"], &basic); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"reportsPerDay", "versions", "fleet", "latestDay"} {
			if _, ok := basic[key]; !ok {
				t.Errorf("%s: basic has no %q key", name, key)
			}
		}
	}

	ranges := map[string]int{"": 30, "?range=7": 7, "?range=90": 90, "?range=365": 365, "?range=abc": 30, "?range=8": 30}
	for query, want := range ranges {
		rec := doDash(t, h, dashReq{path: "/api/v1/views" + query, header: bearer(dashToken)})
		var v Views
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Range != want {
			t.Errorf("query %q: range = %d (err %v), want %d", query, v.Range, err, want)
		}
	}

	refused := doDash(t, h, dashReq{path: "/api/v1/views"})
	if refused.Code != http.StatusUnauthorized || refused.Body.String() != `{"error":"unauthorized"}` ||
		refused.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unauthenticated views: %d %q", refused.Code, refused.Body)
	}
}

func TestDashboardViewsEmptyStore(t *testing.T) {
	_, h := newDash(t, config{})
	rec := doDash(t, h, dashReq{path: "/api/v1/views?range=7", header: bearer(dashToken)})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["empty"]) != "true" || string(raw["basic"]) != "null" || string(raw["extended"]) != "null" {
		t.Fatalf("empty store view = %s", rec.Body)
	}
	session := signIn(t, h)
	if page := doDash(t, h, dashReq{path: "/", cookie: session}); page.Code != http.StatusOK {
		t.Fatalf("overview on an empty store: %d", page.Code)
	}
}

func TestDashboardMetricsAcceptsBearerOnly(t *testing.T) {
	s, h := newDash(t, config{})
	session := signIn(t, h)
	s.reports.WithLabelValues("1.0.0").Inc()
	for name, r := range map[string]dashReq{
		"no credentials": {path: "/metrics"},
		"wrong bearer":   {path: "/metrics", header: bearer("nope")},
		"basic scheme":   {path: "/metrics", header: map[string]string{"Authorization": "Basic " + dashToken}},
		"bare token":     {path: "/metrics", header: map[string]string{"Authorization": dashToken}},
		"cookie only":    {path: "/metrics", cookie: session},
	} {
		rec := doDash(t, h, r)
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != `{"error":"unauthorized"}` {
			t.Errorf("%s: %d %q, want 401 unauthorized", name, rec.Code, rec.Body)
		}
	}
	rec := doDash(t, h, dashReq{path: "/metrics", header: bearer(dashToken)})
	if rec.Code != http.StatusOK {
		t.Fatalf("with the bearer token: %d", rec.Code)
	}
	for _, series := range []string{
		`gameplane_telemetry_reports_total{version="1.0.0"} 1`,
		`gameplane_telemetry_rate_limited_total{route="ingest"} 0`,
		`gameplane_telemetry_rate_limited_total{route="login"} 0`,
	} {
		if !strings.Contains(rec.Body.String(), series) {
			t.Errorf("metrics lack %s", series)
		}
	}
	if post := doDash(t, h, dashReq{method: http.MethodPost, path: "/metrics", header: bearer(dashToken)}); post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics: %d, want 405", post.Code)
	}
}

func TestDashboardWithoutTokenFailsClosed(t *testing.T) {
	s := newServer(config{}) // no dashboard token
	s.now = func() time.Time { return dashNow }
	h := s.dashboardRoutes()
	for name, r := range map[string]dashReq{
		"empty bearer":         {path: "/api/v1/views", header: map[string]string{"Authorization": "Bearer "}},
		"empty metrics bearer": {path: "/metrics", header: map[string]string{"Authorization": "Bearer "}},
	} {
		if rec := doDash(t, h, r); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, rec.Code)
		}
	}
	login := doDash(t, h, dashReq{method: http.MethodPost, path: "/login", header: sameOriginHeader(), body: loginForm("")})
	if login.Code != http.StatusUnauthorized || len(login.Result().Cookies()) != 0 {
		t.Fatalf("login with an empty token: %d, want 401 and no cookie", login.Code)
	}
	// A cookie signed with the key of the empty token is refused.
	forger := &dashboard{s: s, key: sessionKey("")}
	forged := &http.Cookie{Name: sessionCookie, Value: forger.newSessionValue(dashNow)}
	if rec := doDash(t, h, dashReq{path: "/", cookie: forged}); rec.Code != http.StatusSeeOther {
		t.Fatalf("forged cookie: %d, want 303", rec.Code)
	}
	if rec := doDash(t, h, dashReq{path: "/api/v1/views", cookie: forged}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie, views: %d, want 401", rec.Code)
	}
}

func TestDashboardStatic(t *testing.T) {
	_, h := newDash(t, config{})
	css := doDash(t, h, dashReq{path: "/static/style.css"})
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") || css.Body.Len() == 0 {
		t.Fatalf("style.css: %d %q", css.Code, css.Header().Get("Content-Type"))
	}
	for _, path := range []string{"/static/", "/static/login.html", "/static/layout.html", "/static/nope.css", "/static/..%2Flogin.html"} {
		if rec := doDash(t, h, dashReq{path: path}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}

func TestDashboardStoreFailureIs500(t *testing.T) {
	s, h := newDash(t, config{})
	session := signIn(t, h)
	if _, err := s.store.db.ExecContext(context.Background(), "DROP TABLE daily_basic"); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]dashReq{
		"overview": {path: "/", cookie: session},
		"views":    {path: "/api/v1/views", header: bearer(dashToken)},
	} {
		if rec := doDash(t, h, r); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s with a broken store: %d, want 500", name, rec.Code)
		}
	}
}
