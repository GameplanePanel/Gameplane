package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRelayHealthExpiresAndResetsOnlyAfterConfirmedInterval(t *testing.T) {
	t.Parallel()
	h := &relayHealth{}
	b := &exponentialBackoff{}
	for i := 0; i < 12; i++ {
		b.next()
	}
	start := time.Unix(100, 0)
	good := relayObservation{Connection: "connected", Forwards: true, Assigned: true}
	h.observe(start, good, b)
	if !h.snapshot(start.Add(healthStaleAfter - time.Nanosecond)).Ready {
		t.Fatal("fresh confirmed health not ready")
	}
	if h.snapshot(start.Add(healthStaleAfter)).Ready {
		t.Fatal("stale health remained ready")
	}
	// A gap in observations breaks continuity, even when the child lives on.
	h.observe(start.Add(healthyResetAfter), good, b)
	if got := b.next(); got != 300*time.Second {
		t.Fatalf("stale interval reset backoff: %v", got)
	}
	for elapsed := time.Duration(0); elapsed <= healthyResetAfter; elapsed += healthPollInterval {
		h.observe(start.Add(healthyResetAfter+elapsed), good, b)
	}
	if got := b.next(); got != time.Second {
		t.Fatalf("confirmed healthy interval did not reset: %v", got)
	}
	h.clear()
	if h.snapshot(start.Add(2 * healthyResetAfter)).Ready {
		t.Fatal("child exit retained ready")
	}
}

func TestRelayHealthHTTPSeparatesLivenessAndReadiness(t *testing.T) {
	t.Parallel()
	h := &relayHealth{}
	handler := relayHealthHandler(h)
	for _, path := range []string{"/healthz", "/readyz", "/status"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		want := http.StatusOK
		if path == "/readyz" {
			want = http.StatusServiceUnavailable
		}
		if w.Code != want {
			t.Fatalf("%s status=%d want=%d", path, w.Code, want)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("health response exposed credentials")
		}
	}
}

func TestFrpHealthRequiresEveryRegisteredProxy(t *testing.T) {
	t.Parallel()
	cfg := Config{BackingServicePort: "game:25565:30000:tcp,query:19132:30001:udp"}
	cases := []struct {
		name, body string
		code       int
		ready      bool
	}{
		{"registered", `{"tcp":[{"name":"game","type":"tcp","status":"running","err":""}],"udp":[{"name":"query","type":"udp","status":"running","err":""}]}`, 200, true},
		{"duplicate refused", `{"tcp":[{"name":"game","type":"tcp","status":"start error","err":"already exists"}],"udp":[{"name":"query","type":"udp","status":"running"}]}`, 200, false},
		{"missing udp", `{"tcp":[{"name":"game","type":"tcp","status":"running"}]}`, 200, false},
		{"lost control", `{}`, 200, false},
		{"wrong type", `{"tcp":[{"name":"game","type":"udp","status":"running"}],"udp":[{"name":"query","type":"udp","status":"running"}]}`, 200, false},
		{"auth refused", `secret rejection body must not escape`, 401, false},
		{"bad body", `{`, 200, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				u, p, ok := r.BasicAuth()
				if !ok || u != "gameplane" || p != "fixture-token" {
					t.Error("missing admin authentication")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			got := checkFrpHealth(context.Background(), s.Client(), s.URL, cfg, "fixture-token")
			if got.ready() != tc.ready {
				t.Fatalf("observation=%+v ready want %v", got, tc.ready)
			}
		})
	}
}

func TestFrpIdentityStableAcrossRestartAndUniqueAcrossRecreation(t *testing.T) {
	// Config output has a fixed path, so this test cannot run in parallel.
	defer func() { _ = os.Remove(frpConfigPath) }()
	for _, uid := range []string{"uid-one", "uid-two"} {
		cfg := Config{GameServerUID: uid, GameServerName: "same", GameServerNamespace: "games", FrpServerAddr: "relay", FrpServerPort: 7000, BackingServiceDNS: "same.games.svc", BackingServicePort: "game:25565:30000:tcp"}
		var prior string
		for i := 0; i < 2; i++ {
			path, err := renderFrpConfig(cfg, "token")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `user = "`+uid+`"`) || !strings.Contains(string(data), `name = "game"`) {
				t.Fatalf("identity or public mapping changed: %s", data)
			}
			if i > 0 && prior != string(data) {
				t.Fatal("restart changed identity/config")
			}
			prior = string(data)
		}
	}
	for _, uid := range []string{"", "with.dot", "bad\nuser", `bad"user`} {
		if validFrpIdentity(uid) {
			t.Fatalf("accepted unsafe identity %q", uid)
		}
	}
}

func TestSupervisionBoundsCleanExitRetriesAndCancels(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var delays []time.Duration
	err := superviseRelay(ctx, Config{}, &relayHealth{}, nil, func(context.Context) error { calls++; return nil }, func(_ context.Context, delay time.Duration) bool {
		delays = append(delays, delay)
		if calls == 4 {
			cancel()
			return false
		}
		return true
	})
	if err != nil || calls != 4 || len(delays) != 4 {
		t.Fatalf("err=%v calls=%d delays=%v", err, calls, delays)
	}
	for i, d := range delays {
		if d != time.Duration(1<<i)*time.Second {
			t.Fatalf("unexpected delay %v", delays)
		}
	}
	if waitRelayRetry(ctx, time.Hour) {
		t.Fatal("cancelled wait continued")
	}
	err = superviseRelay(context.Background(), Config{}, &relayHealth{}, nil, func(context.Context) error { return errors.New("exit status 127") }, waitRelayRetry)
	if err == nil {
		t.Fatal("unrecoverable child error was retried")
	}
}

func TestPlayitCachedRunningCannotConfirmConnection(t *testing.T) {
	t.Parallel()
	state := playitState{Phase: "running", Tunnels: []playitTunnel{{DisplayAddress: "fixture.playit.gg:30000", Destination: "127.0.0.1:25565"}}}
	got := playitHealthObservation(state, []backingPort{{name: "game", port: 25565}})
	if got.ready() || got.Connection != "unknown" || !got.Assigned {
		t.Fatalf("cached assignment must not imply connection: %+v", got)
	}
	got = playitHealthObservation(playitState{Phase: "running"}, []backingPort{{name: "game", port: 25565}})
	if got.Assigned || got.ready() {
		t.Fatal("missing address ready")
	}
}

func TestLoadFrpConfigRejectsMissingOrUnsafeUID(t *testing.T) {
	t.Parallel()
	for _, uid := range []string{"", "bad.uid", "bad\nuid"} {
		vars := map[string]string{"GAMESERVER_NAME": "same", "GAMESERVER_NAMESPACE": "games", "GAMESERVER_UID": uid, "TUNNEL_TYPE": "frp", "BACKING_SERVICE_DNS": "same.games.svc", "FRP_SERVER_ADDR": "relay", "BACKING_SERVICE_PORT": "game:25565:30000:tcp"}
		if _, err := loadConfig(func(key string) string { return vars[key] }); err == nil {
			t.Fatalf("accepted identity %q", uid)
		}
	}
}

func TestFrpHealthCancelsBlockedProviderQuery(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan relayObservation, 1)
	go func() {
		done <- checkFrpHealth(ctx, server.Client(), server.URL, Config{BackingServicePort: "game:25565:30000:tcp"}, "token")
	}()
	<-started
	cancel()
	select {
	case state := <-done:
		if state.ready() {
			t.Fatal("cancelled query ready")
		}
	case <-time.After(time.Second):
		t.Fatal("provider query ignored cancellation")
	}
}

func TestExitedChildCannotBeMadeReadyByLateStatus(t *testing.T) {
	t.Parallel()
	h := &relayHealth{}
	queryStarted := make(chan struct{})
	check := func(ctx context.Context) relayObservation {
		close(queryStarted)
		<-ctx.Done()
		return relayObservation{Connection: "connected", Forwards: true, Assigned: true}
	}
	err := superviseRelay(context.Background(), Config{}, h, check, func(context.Context) error { <-queryStarted; return errors.New("child exited") }, func(context.Context, time.Duration) bool { return false })
	if err != nil || h.snapshot(time.Now()).Ready {
		t.Fatalf("late status restored exited child: err=%v state=%+v", err, h.snapshot(time.Now()))
	}
}
