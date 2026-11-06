//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// i18nInstanceDefaultMu serializes the two tests that depend on the instance
// default language: TestAPI_I18n_NotifyLanguage (T062), which needs the
// default to be "en" while its sinks send, and TestAPI_I18n_InstanceDefault
// (T092), which changes it. Both hold the lock for their whole body and
// restore the default before releasing it.
var i18nInstanceDefaultMu sync.Mutex

// i18nAuthLocale reads the unauthenticated GET /auth/locale through a
// port-forward to the API and returns its defaultLanguage. The endpoint
// returns exactly one key, so any other shape fails the test.
func i18nAuthLocale(t *testing.T, port int) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/auth/locale", port), nil)
	if err != nil {
		t.Fatalf("build /auth/locale request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /auth/locale: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /auth/locale: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/locale: status %d, body=%s", resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode /auth/locale: %v; body=%s", err, body)
	}
	if len(got) != 1 {
		t.Fatalf("/auth/locale: want exactly one key, got %d; body=%s", len(got), body)
	}
	lang, ok := got["defaultLanguage"].(string)
	if !ok {
		t.Fatalf("/auth/locale: defaultLanguage missing or not a string; body=%s", body)
	}
	return lang
}

// i18nConfigSection returns one section of GET /admin/config/ and whether it
// is present.
func i18nConfigSection(t *testing.T, admin *APIClient, section string) (json.RawMessage, bool) {
	t.Helper()
	resp, body, err := admin.Get("/admin/config/")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("GET /admin/config/: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/config/: status=%d body=%s", resp.StatusCode, string(body))
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode /admin/config/: %v\n%s", err, string(body))
	}
	v, ok := all[section]
	return v, ok
}

// i18nNotifyExpect is the payload contract for one test-send (spec 019, T062).
type i18nNotifyExpect struct {
	sink      string
	query     string
	language  string
	eventWord string
	detail    string
}

// i18nCheckNotifyPayload asserts one receiver log body against the test-send
// contract: the Event fields are unchanged (type, ts, test, optional instance),
// and the additive language, title and text carry the rendered strings.
func i18nCheckNotifyPayload(t *testing.T, want i18nNotifyExpect, raw string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("%s: decode payload: %v; body=%s", want.sink, err, raw)
	}

	// Only the Event fields and the three additive fields may appear.
	allowed := map[string]bool{"type": true, "ts": true, "test": true, "instance": true,
		"language": true, "title": true, "text": true}
	for key := range payload {
		if !allowed[key] {
			t.Errorf("%s: unexpected payload key %q; body=%s", want.sink, key, raw)
		}
	}
	for _, key := range []string{"type", "ts", "test", "language", "title", "text"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("%s: payload missing %q; body=%s", want.sink, key, raw)
		}
	}
	if payload["type"] != "test" {
		t.Errorf("%s: type=%v, want test", want.sink, payload["type"])
	}
	if payload["test"] != true {
		t.Errorf("%s: test=%v, want true", want.sink, payload["test"])
	}
	ts, _ := payload["ts"].(string)
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("%s: ts=%q is not RFC3339: %v", want.sink, ts, err)
	}
	if payload["language"] != want.language {
		t.Errorf("%s: language=%v, want %q", want.sink, payload["language"], want.language)
	}

	// The [instance] part is not asserted: it depends on general.instanceName.
	title, _ := payload["title"].(string)
	text, _ := payload["text"].(string)
	if !strings.HasPrefix(title, "[") || !strings.HasSuffix(title, " "+want.eventWord) {
		t.Errorf("%s: title=%q, want \"[instance] %s\"", want.sink, title, want.eventWord)
	}
	if wantText := title + " — " + want.detail; text != wantText {
		t.Errorf("%s: text=%q, want %q", want.sink, text, wantText)
	}
}

// TestAPI_I18n_NotifyLanguage proves that a notification sink's language
// selects the rendered title and text of a test-send (spec 019, T062), and
// that the pre-existing payload fields are unchanged. A sink with language
// "fr" renders French; a sink with no language follows the instance default,
// which is "en".
//
// The test-send delivers to an in-cluster webhook receiver (fixtures/
// notify-sink.yaml) that logs each request's query and body. Budget: one
// e2e-admin login, in the operator bucket for the login budget.
func TestAPI_I18n_NotifyLanguage(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	// Close is registered first so it runs last: every cleanup below uses admin.
	t.Cleanup(admin.Close)

	// Defers, not t.Cleanup, for everything that calls the API: the client's
	// port-forward is bound to t.Context(), which is canceled before cleanups
	// run, so a request made from a cleanup never reaches the API.
	i18nInstanceDefaultMu.Lock()
	defer i18nInstanceDefaultMu.Unlock()

	// The rollout proves the receiver is listening. The API-to-Service CGI
	// readiness send below also gates DNS, endpoint routing, and body handling.
	fixture, err := filepath.Abs(filepath.Join("fixtures", "notify-sink.yaml"))
	if err != nil {
		t.Fatalf("resolve notify-sink fixture: %v", err)
	}
	envInstance.ApplyYAML(t, "notify-sink.yaml")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = envInstance.Kubectl(ctx, "delete", "-f", fixture, "--ignore-not-found")
	})
	// Cleanup runs in reverse registration order, so retain receiver evidence
	// before deleting it. t.Context() is already canceled when cleanups run.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		commands := [][]string{
			{"get", "pods", "-o", "wide"},
			{"get", "service", "e2e-notify-sink", "-o", "yaml"},
			{"get", "endpointslices", "-l", "kubernetes.io/service-name=e2e-notify-sink", "-o", "yaml"},
			{"describe", "pods", "-l", "app.kubernetes.io/name=e2e-notify-sink"},
			{"logs", "deploy/e2e-notify-sink", "--all-containers=true", "--tail=100"},
			{"exec", "deploy/e2e-notify-sink", "--", "sh", "-c", `for file in /tmp/requests.log /tmp/hooks.log; do printf '\n%s\n' "$file"; cat "$file"; done`},
		}
		for _, args := range commands {
			commandCtx, stop := context.WithTimeout(ctx, 7*time.Second)
			out, err := envInstance.Kubectl(commandCtx, append([]string{"-n", "gameplane-system"}, args...)...)
			stop()
			t.Logf("notify sink diagnostics %v: %v\n%s", args, err, out)
		}
	})
	if out, err := envInstance.Kubectl(t.Context(), "rollout", "status", "-n", "gameplane-system",
		"deploy/e2e-notify-sink", "--timeout=2m"); err != nil {
		t.Fatalf("notify sink receiver not ready: %v\n%s", err, out)
	}

	// Restore the notifications section (or an empty one if it was absent).
	prior, hadPrior := i18nConfigSection(t, admin, "notifications")
	defer func() {
		body := prior
		if !hadPrior {
			body = json.RawMessage(`{"sinks":[]}`)
		}
		r, _, _ := admin.Do(http.MethodPut, "/admin/config/notifications", body)
		if r != nil {
			_ = r.Body.Close()
		}
	}()

	type sinkTarget struct{ name, query string }
	sinks := []sinkTarget{
		{"e2e-notify-fr", "sink=fr"},
		{"e2e-notify-default", "sink=default"},
	}
	readinessSink := sinkTarget{"e2e-notify-readiness", "probe=readiness"}
	configuredSinks := append([]sinkTarget{readinessSink}, sinks...)
	for _, s := range configuredSinks {
		name := s.name
		defer func() {
			r, _, _ := admin.Delete("/admin/notifications/sinks/" + name + "/secret")
			if r != nil {
				_ = r.Body.Close()
			}
		}()
	}

	// Secrets hold each sink's URL; configRef must name the returned Secret.
	for _, s := range configuredSinks {
		url := fmt.Sprintf("http://e2e-notify-sink.gameplane-system.svc.cluster.local:8080/cgi-bin/hook?%s", s.query)
		resp, body, err := admin.Do(http.MethodPut, "/admin/notifications/sinks/"+s.name+"/secret",
			map[string]string{"kind": "webhook", "url": url, "authorization": ""})
		if resp != nil {
			r := resp
			defer func() { _ = r.Body.Close() }()
		}
		if err != nil {
			t.Fatalf("PUT secret %s: %v", s.name, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT secret %s: status=%d body=%s", s.name, resp.StatusCode, string(body))
		}
		var got struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode secret response %s: %v\n%s", s.name, err, string(body))
		}
		if want := "gameplane-notify-" + s.name; got.Name != want {
			t.Fatalf("secret name=%q, want %q", got.Name, want)
		}
	}

	// The fr sink names French explicitly; the default sink omits language.
	// All stay disabled so the notifier never delivers real events here. The
	// readiness sink uses French too, exercising the same UTF-8 body path.
	resp, body, err := admin.Do(http.MethodPut, "/admin/config/notifications", map[string]any{
		"sinks": []map[string]any{
			{"name": readinessSink.name, "kind": "webhook", "enabled": false,
				"configRef": "gameplane-notify-" + readinessSink.name, "language": "fr"},
			{"name": "e2e-notify-fr", "kind": "webhook", "enabled": false,
				"configRef": "gameplane-notify-e2e-notify-fr", "language": "fr"},
			{"name": "e2e-notify-default", "kind": "webhook", "enabled": false,
				"configRef": "gameplane-notify-e2e-notify-default"},
		},
	})
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("PUT notifications config: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT notifications config: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Race R1 guard: the default-sink expectation below is the instance
	// default, so read it just before sending and require "en".
	port, stop := envInstance.PortForward(t, "gameplane-system", "svc/gameplane-api", 80)
	defer stop()
	defaultTag := i18nAuthLocale(t, port)
	if defaultTag != "en" {
		t.Fatalf("instance default language = %q, want en before the test-send", defaultTag)
	}

	// Establish the exact API -> Service -> CGI POST path before measuring the
	// language contract. A probe may be delivered despite a lost response, so
	// its distinct query cannot satisfy either language assertion below.
	readinessCtx, cancelReadiness := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelReadiness()
	readinessAttempts := 0
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		readinessAttempts++
		resp, b, err := admin.DoContext(readinessCtx, http.MethodPost,
			"/admin/notifications/sinks/"+readinessSink.name+"/test", nil)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			var networkErr net.Error
			if !errors.As(err, &networkErr) {
				t.Fatalf("notify sink readiness request: %v", err)
			}
			t.Logf("notify sink readiness attempt %d: %v", readinessAttempts, err)
			return false, fmt.Sprintf("notify sink readiness request: %v", err)
		}
		if resp.StatusCode == http.StatusBadGateway &&
			(strings.Contains(string(b), "connection refused") || strings.Contains(string(b), "context deadline exceeded")) {
			t.Logf("notify sink readiness attempt %d: %s", readinessAttempts, b)
			return false, fmt.Sprintf("notify sink readiness: %s", b)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("notify sink readiness: status=%d body=%s", resp.StatusCode, b)
		}
		var delivered struct {
			Delivered bool `json:"delivered"`
		}
		if err := json.Unmarshal(b, &delivered); err != nil || !delivered.Delivered {
			t.Fatalf("notify sink readiness: delivered not true; body=%s", b)
		}
		return true, ""
	})
	cancelReadiness()

	for _, s := range sinks {
		// A ready pod can still refuse connections on its Service IP for a
		// moment while kube-proxy programs the new endpoint, so only that
		// refusal is retried. A refused send reaches no receiver, so it adds
		// no log line.
		var body []byte
		envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
			resp, b, err := admin.Post("/admin/notifications/sinks/"+s.name+"/test", nil)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err != nil {
				t.Fatalf("POST test %s: %v", s.name, err)
			}
			if resp.StatusCode == http.StatusBadGateway && strings.Contains(string(b), "connection refused") {
				return false, fmt.Sprintf("POST test %s: receiver not reachable yet: %s", s.name, string(b))
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST test %s: status=%d body=%s", s.name, resp.StatusCode, string(b))
			}
			body = b
			return true, ""
		})
		var delivered struct {
			Delivered bool `json:"delivered"`
		}
		if err := json.Unmarshal(body, &delivered); err != nil || !delivered.Delivered {
			t.Fatalf("POST test %s: delivered not true; body=%s", s.name, string(body))
		}
	}

	// The test-send is synchronous, so each receiver line already exists.
	// Eventually only absorbs the log write racing the response on a loaded runner.
	var logText string
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		out, err := envInstance.KubectlExec(t, "gameplane-system", "deploy/e2e-notify-sink", "cat", "/tmp/hooks.log")
		if err != nil {
			return false, fmt.Sprintf("cat /tmp/hooks.log: %v: %s", err, out)
		}
		logText = out
		return strings.Contains(out, "sink=fr\t") && strings.Contains(out, "sink=default\t"),
			fmt.Sprintf("log lacks both sinks: %q", out)
	})

	// The last matching line wins in case an earlier run left lines behind.
	bodyFor := map[string]string{}
	for _, line := range strings.Split(logText, "\n") {
		query, body, ok := strings.Cut(line, "\t")
		if ok {
			bodyFor[query] = body
		}
	}

	i18nCheckNotifyPayload(t, i18nNotifyExpect{
		sink: "e2e-notify-fr", query: "sink=fr", language: "fr",
		eventWord: "notification de test",
		detail:    "Ceci est une notification de test de Gameplane. Votre destination est correctement configurée.",
	}, bodyFor["sink=fr"])

	i18nCheckNotifyPayload(t, i18nNotifyExpect{
		sink: "e2e-notify-default", query: "sink=default", language: defaultTag,
		eventWord: "test notification",
		detail:    "This is a test notification from Gameplane. Your sink is wired up correctly.",
	}, bodyFor["sink=default"])
}
