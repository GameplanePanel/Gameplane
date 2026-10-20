package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/netguard"
)

// errPermanent marks a delivery failure that retrying cannot fix — a 4xx
// response, a blocked address, or a sink Secret missing required keys.
var errPermanent = errors.New("permanent delivery failure")

// retryBackoffs drives the worker's retry loop: an immediate attempt plus
// two retries. Package-level so tests can shrink the waits.
var retryBackoffs = []time.Duration{0, 2 * time.Second, 8 * time.Second}

// deliverWithRetry ships one event to one sink from the worker, retrying
// transient failures with a short backoff and giving up on permanent ones.
func (n *Notifier) deliverWithRetry(ctx context.Context, s Sink, secret map[string][]byte, e Event) {
	var err error
	for _, wait := range retryBackoffs {
		if wait > 0 {
			select {
			case <-ctx.Done():
				deliveries.WithLabelValues(s.Kind, "failed").Inc()
				return
			case <-time.After(wait):
			}
		}
		if err = n.deliver(ctx, s, secret, e); err == nil {
			deliveries.WithLabelValues(s.Kind, "sent").Inc()
			return
		}
		if errors.Is(err, errPermanent) {
			break
		}
	}
	deliveries.WithLabelValues(s.Kind, "failed").Inc()
	slog.Warn("notification delivery failed", "sink", s.Name, "kind", s.Kind, "type", e.Type, "err", err)
}

// deliver makes a single delivery attempt to s. The error text never
// contains the sink URL — webhook URLs embed capability tokens, and this
// error flows to logs and the test endpoint's response.
func (n *Notifier) deliver(ctx context.Context, s Sink, secret map[string][]byte, e Event) error {
	var (
		body []byte
		err  error
	)
	switch s.Kind {
	case "discord":
		body, err = formatDiscord(e)
	case "slack":
		body, err = formatSlack(e)
	case "webhook":
		body, err = formatWebhook(e)
	case "ntfy":
		// ntfy takes a plain-text body with the metadata in headers, not a
		// JSON envelope — it bypasses the shared JSON POST below.
		body, headers := formatNtfy(e)
		if auth := string(secret["authorization"]); auth != "" {
			headers["Authorization"] = auth
		}
		return n.sendHTTPHeaders(ctx, string(secret["url"]), headers, body)
	case "smtp":
		return sendSMTP(ctx, secret, e)
	default:
		return fmt.Errorf("unknown sink kind %q: %w", s.Kind, errPermanent)
	}
	if err != nil {
		return fmt.Errorf("format %s payload: %w: %w", s.Kind, err, errPermanent)
	}
	return n.sendHTTP(ctx, string(secret["url"]), string(secret["authorization"]), body)
}

// sendHTTP POSTs body as JSON to url. 4xx means the endpoint understood
// us and said no (bad token, revoked webhook) — permanent; 5xx and
// network errors are transient and retryable.
func (n *Notifier) sendHTTP(ctx context.Context, rawURL, authHeader string, body []byte) error {
	headers := map[string]string{"Content-Type": "application/json"}
	if authHeader != "" {
		headers["Authorization"] = authHeader
	}
	return n.sendHTTPHeaders(ctx, rawURL, headers, body)
}

// sendHTTPHeaders is sendHTTP with an explicit header set, for sinks
// whose protocol rides on headers rather than a JSON body (ntfy).
func (n *Notifier) sendHTTPHeaders(ctx context.Context, rawURL string, headers map[string]string, body []byte) error {
	if rawURL == "" {
		return fmt.Errorf(`secret has no "url" key: %w`, errPermanent)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w: %w", sanitizeErr(err), errPermanent)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		if errors.Is(err, netguard.ErrBlockedAddr) {
			return fmt.Errorf("post: %w: %w", sanitizeErr(err), errPermanent)
		}
		return fmt.Errorf("post: %w", sanitizeErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 500:
		return fmt.Errorf("endpoint returned %d", resp.StatusCode)
	default:
		return fmt.Errorf("endpoint returned %d: %w", resp.StatusCode, errPermanent)
	}
}

// sanitizeErr strips the *url.Error wrapper whose Error() would print the
// full request URL — and with it any capability token in the path.
func sanitizeErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

// sendSMTP delivers e as a plain-text mail. Supported secret keys: host,
// port (default 587), from, to (comma-separated), optional username +
// password (AUTH PLAIN — refused by net/smtp on unencrypted connections),
// optional tls = starttls (default) | implicit | none.
func sendSMTP(ctx context.Context, secret map[string][]byte, e Event) error {
	host := string(secret["host"])
	if host == "" {
		return fmt.Errorf(`secret has no "host" key: %w`, errPermanent)
	}
	port := string(secret["port"])
	if port == "" {
		port = "587"
	}
	from := strings.TrimSpace(string(secret["from"]))
	if from == "" {
		return fmt.Errorf(`secret has no "from" key: %w`, errPermanent)
	}
	var rcpts []string
	for _, r := range strings.Split(string(secret["to"]), ",") {
		if r = strings.TrimSpace(r); r != "" {
			rcpts = append(rcpts, r)
		}
	}
	if len(rcpts) == 0 {
		return fmt.Errorf(`secret has no "to" recipients: %w`, errPermanent)
	}
	mode := string(secret["tls"])
	if mode == "" {
		mode = "starttls"
	}

	conn, err := netguard.Dialer(10*time.Second, netguard.IsAllowed).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		if errors.Is(err, netguard.ErrBlockedAddr) {
			return fmt.Errorf("dial smtp: %w: %w", err, errPermanent)
		}
		return fmt.Errorf("dial smtp: %w", err)
	}
	// One deadline bounds the whole SMTP exchange — net/smtp has no
	// context support past the dial.
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if mode == "implicit" {
		conn = tls.Client(conn, &tls.Config{ServerName: host})
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = c.Close() }()
	if mode == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	user, pass := string(secret["username"]), string(secret["password"])
	if user != "" && pass != "" {
		if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			return fmt.Errorf("smtp auth: %w: %w", err, errPermanent)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	subject, body := formatEmail(e)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		sanitizeHeader(from), sanitizeHeader(strings.Join(rcpts, ", ")), subject,
		time.Now().UTC().Format(time.RFC1123Z), body)
	if _, err := io.WriteString(w, msg); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}
