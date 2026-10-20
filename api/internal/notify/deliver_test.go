package notify

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/netguard"
)

// testNotifier builds a Notifier with just the delivery plumbing — the
// store/kube fields stay nil because these tests never load sinks.
func testNotifier() *Notifier {
	return &Notifier{
		client: netguard.HTTPClient(5*time.Second, netguard.IsAllowed),
		ch:     make(chan Event, 4),
	}
}

// fastRetries shrinks the retry backoffs for the duration of a test.
func fastRetries(t *testing.T) {
	t.Helper()
	orig := retryBackoffs
	retryBackoffs = []time.Duration{0, 0, 0}
	t.Cleanup(func() { retryBackoffs = orig })
}

func TestSendHTTPSuccess(t *testing.T) {
	var gotAuth, gotCT atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotCT.Store(r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	n := testNotifier()
	if err := n.sendHTTP(context.Background(), srv.URL, "Bearer tok", []byte(`{}`)); err != nil {
		t.Fatalf("sendHTTP: %v", err)
	}
	if gotAuth.Load() != "Bearer tok" {
		t.Errorf("authorization header = %q", gotAuth.Load())
	}
	if gotCT.Load() != "application/json" {
		t.Errorf("content-type = %q", gotCT.Load())
	}
}

func TestSendHTTP4xxIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	err := testNotifier().sendHTTP(context.Background(), srv.URL, "", []byte(`{}`))
	if !errors.Is(err, errPermanent) {
		t.Fatalf("4xx: err = %v, want errPermanent", err)
	}
}

func TestSendHTTP5xxIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	err := testNotifier().sendHTTP(context.Background(), srv.URL, "", []byte(`{}`))
	if err == nil || errors.Is(err, errPermanent) {
		t.Fatalf("5xx: err = %v, want transient error", err)
	}
}

func TestSendHTTPMissingURL(t *testing.T) {
	err := testNotifier().sendHTTP(context.Background(), "", "", nil)
	if !errors.Is(err, errPermanent) || !strings.Contains(err.Error(), `"url"`) {
		t.Fatalf("missing url: err = %v", err)
	}
}

func TestSendHTTPBlockedAddr(t *testing.T) {
	// Link-local (the cloud metadata range) must be refused at dial time
	// and treated as permanent. No packet leaves the host: the dial
	// control hook rejects before connecting.
	err := testNotifier().sendHTTP(context.Background(), "http://169.254.169.254/latest", "", nil)
	if !errors.Is(err, errPermanent) {
		t.Fatalf("blocked addr: err = %v, want errPermanent", err)
	}
	// The sanitized error must not echo the URL path back.
	if strings.Contains(err.Error(), "/latest") {
		t.Fatalf("error leaks URL path: %v", err)
	}
}

func TestDeliverWithRetryRecovers(t *testing.T) {
	fastRetries(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	n := testNotifier()
	s := Sink{Name: "hook", Kind: "webhook", Enabled: true}
	secret := map[string][]byte{"url": []byte(srv.URL)}
	n.deliverWithRetry(context.Background(), s, secret, Event{Type: EventBackupFailed})
	if got := calls.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (two 500s then success)", got)
	}
}

func TestDeliverWithRetryStopsOnPermanent(t *testing.T) {
	fastRetries(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	n := testNotifier()
	s := Sink{Name: "hook", Kind: "discord", Enabled: true}
	n.deliverWithRetry(context.Background(), s, map[string][]byte{"url": []byte(srv.URL)}, Event{Type: EventBackupFailed})
	if got := calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (4xx must not retry)", got)
	}
}

func TestDeliverUnknownKind(t *testing.T) {
	err := testNotifier().deliver(context.Background(), Sink{Name: "x", Kind: "carrier-pigeon"}, nil, Event{})
	if !errors.Is(err, errPermanent) {
		t.Fatalf("unknown kind: err = %v, want errPermanent", err)
	}
}

// ntfy rides its metadata in headers over a plain-text body, with the
// optional token as a standard Authorization header.
func TestDeliverNtfy(t *testing.T) {
	type got struct {
		title, prio, tags, auth, ct, body string
	}
	var rec atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		rec.Store(got{
			title: r.Header.Get("Title"),
			prio:  r.Header.Get("Priority"),
			tags:  r.Header.Get("Tags"),
			auth:  r.Header.Get("Authorization"),
			ct:    r.Header.Get("Content-Type"),
			body:  string(buf[:n]),
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := Event{
		Type: EventBackupFailed, Kind: "Backup", Namespace: "games", Name: "nightly",
		Reason: "ResticError", Message: "repository locked", Instance: "prod",
	}
	secret := map[string][]byte{"url": []byte(srv.URL), "authorization": []byte("Bearer tk_x")}
	if err := testNotifier().deliver(context.Background(), Sink{Name: "n", Kind: "ntfy"}, secret, e); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	g, _ := rec.Load().(got)
	if g.title != "[prod] backup failed: games/nightly" {
		t.Errorf("Title = %q", g.title)
	}
	if g.prio != "high" || g.tags != "rotating_light" {
		t.Errorf("Priority/Tags = %q/%q, want high/rotating_light for a failure", g.prio, g.tags)
	}
	if g.auth != "Bearer tk_x" {
		t.Errorf("Authorization = %q", g.auth)
	}
	if !strings.HasPrefix(g.ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", g.ct)
	}
	if !strings.Contains(g.body, "repository locked") {
		t.Errorf("body = %q, want the event detail", g.body)
	}
}

// fakeSMTP runs a minimal single-connection SMTP server good enough for a
// tls=none, unauthenticated exchange. It sends the received DATA payload on
// the returned channel once the client QUITs.
func fakeSMTP(t *testing.T) (addr string, msgs <-chan string) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		br := bufio.NewReader(conn)
		var data strings.Builder
		inData := false
		fmt.Fprintf(conn, "220 fake ESMTP\r\n")
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					fmt.Fprintf(conn, "250 OK\r\n")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				fmt.Fprintf(conn, "250-fake\r\n250 OK\r\n")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				fmt.Fprintf(conn, "354 go ahead\r\n")
			case strings.HasPrefix(cmd, "QUIT"):
				fmt.Fprintf(conn, "221 bye\r\n")
				out <- data.String()
				return
			default: // MAIL FROM, RCPT TO, NOOP, ...
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return ln.Addr().String(), out
}

func TestSendSMTP(t *testing.T) {
	addr, msgs := fakeSMTP(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	secret := map[string][]byte{
		"host": []byte(host),
		"port": []byte(port),
		"from": []byte("gameplane@example.com"),
		"to":   []byte("ops@example.com, admin@example.com"),
		"tls":  []byte("none"),
	}
	e := Event{Type: EventBackupFailed, TS: "2026-07-03T10:00:00Z", Kind: "Backup", Namespace: "games", Name: "nightly", Message: "exit 1"}
	if err := sendSMTP(context.Background(), secret, e); err != nil {
		t.Fatalf("sendSMTP: %v", err)
	}
	select {
	case msg := <-msgs:
		for _, want := range []string{"Subject: [Gameplane] backup failed: games/nightly", "To: ops@example.com, admin@example.com", "exit 1"} {
			if !strings.Contains(msg, want) {
				t.Errorf("mail missing %q:\n%s", want, msg)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no mail received")
	}
}

func TestSendSMTPMissingKeys(t *testing.T) {
	cases := []struct {
		name   string
		secret map[string][]byte
	}{
		{"no host", map[string][]byte{"from": []byte("a@b"), "to": []byte("c@d")}},
		{"no from", map[string][]byte{"host": []byte("mail"), "to": []byte("c@d")}},
		{"no recipients", map[string][]byte{"host": []byte("mail"), "from": []byte("a@b"), "to": []byte(" , ")}},
	}
	for _, tc := range cases {
		err := sendSMTP(context.Background(), tc.secret, Event{Type: EventTest})
		if !errors.Is(err, errPermanent) {
			t.Errorf("%s: err = %v, want errPermanent", tc.name, err)
		}
	}
}
