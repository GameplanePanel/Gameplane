package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	healthPollInterval = 2 * time.Second
	healthStaleAfter   = 10 * time.Second
	healthyResetAfter  = time.Minute
	frpHealthURL       = "http://127.0.0.1:7400/api/status"
)

// Only closed-set, non-sensitive state reaches the health endpoint. Never put
// provider response text, credentials, login links or target addresses here.
type relayObservation struct {
	Connection string `json:"connection"`
	Forwards   bool   `json:"forwardsRegistered"`
	Assigned   bool   `json:"endpointAssigned"`
}

func (o relayObservation) ready() bool {
	return o.Connection == "connected" && o.Forwards && o.Assigned
}

type relaySnapshot struct {
	relayObservation
	Ready bool `json:"ready"`
}

type relayHealth struct {
	mu                    sync.Mutex
	observation           relayObservation
	checked, healthySince time.Time
}

func (h *relayHealth) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observation = relayObservation{Connection: "unknown"}
	h.checked, h.healthySince = time.Time{}, time.Time{}
}

func (h *relayHealth) observe(now time.Time, observation relayObservation, backoff *exponentialBackoff) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observeLocked(now, observation, backoff)
}

func (h *relayHealth) observeLocked(now time.Time, observation relayObservation, backoff *exponentialBackoff) {
	if !observation.ready() {
		h.healthySince = time.Time{}
	} else {
		if h.healthySince.IsZero() || now.Sub(h.checked) >= healthStaleAfter {
			h.healthySince = now
		}
		if backoff != nil && now.Sub(h.healthySince) >= healthyResetAfter {
			backoff.reset()
		}
	}
	h.observation, h.checked = observation, now
}

func (h *relayHealth) snapshot(now time.Time) relaySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.checked.IsZero() || now.Sub(h.checked) >= healthStaleAfter {
		return relaySnapshot{relayObservation: relayObservation{Connection: "unknown"}}
	}
	return relaySnapshot{relayObservation: h.observation, Ready: h.observation.ready()}
}

func relayHealthHandler(h *relayHealth) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !h.snapshot(time.Now()).Ready {
			http.Error(w, "relay connection or forwards not confirmed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.snapshot(time.Now()))
	})
	return mux
}

type relayCheck func(context.Context) relayObservation

func startRelayHealth(ctx context.Context, h *relayHealth) (func(), error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ":8091")
	if err != nil {
		return nil, fmt.Errorf("relay health listener: %w", err)
	}
	server := &http.Server{Handler: relayHealthHandler(h), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	return func() { _ = server.Close(); <-done }, nil
}

func monitorRelay(ctx context.Context, h *relayHealth, check relayCheck, backoff *exponentialBackoff) {
	if check == nil {
		return
	}
	for ctx.Err() == nil {
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		observation := check(queryCtx)
		cancel()
		h.mu.Lock()
		if ctx.Err() == nil {
			h.observeLocked(time.Now(), observation, backoff)
		}
		h.mu.Unlock()
		if !waitRelayRetry(ctx, healthPollInterval) {
			return
		}
	}
}

func waitRelayRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Each attempt owns its monitor. Joining it before clearing state prevents a
// final in-flight response from re-marking an exited child ready.
func superviseRelay(ctx context.Context, cfg Config, h *relayHealth, check relayCheck, runChild func(context.Context) error, wait func(context.Context, time.Duration) bool) error {
	backoff := &exponentialBackoff{}
	for ctx.Err() == nil {
		h.clear()
		attemptCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); monitorRelay(attemptCtx, h, check, backoff) }()
		err := runChild(attemptCtx)
		cancel()
		h.clear()
		<-done
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		log.Print("relay process exited; readiness cleared")
		if isUnrecoverable(cfg, err) {
			return fmt.Errorf("unrecoverable relay error: %w", err)
		}
		// A clean unexpected exit is also a failure. Never busy-loop on it.
		delay := backoff.next()
		log.Printf("restarting relay in %v", delay)
		if !wait(ctx, delay) {
			return nil
		}
	}
	return nil
}

func relayHealthCheck(cfg Config, credential string) relayCheck {
	if cfg.TunnelType == "frp" {
		// No environment proxy or redirects: the admin credential is restricted
		// to this process's loopback frpc endpoint.
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		return func(ctx context.Context) relayObservation {
			return checkFrpHealth(ctx, client, frpHealthURL, cfg, credential)
		}
	}
	if cfg.TunnelType == "playit" {
		ports, err := parseBackingPorts(cfg.BackingServicePorts)
		return func(ctx context.Context) relayObservation {
			if err != nil {
				return relayObservation{Connection: "unknown"}
			}
			state, err := queryPlayitState(ctx, playitSocketPath)
			if err != nil {
				return relayObservation{Connection: "unknown"}
			}
			return playitHealthObservation(state, ports)
		}
	}
	return nil // Tailscale's embedded server supplies its own readiness.
}

func checkFrpHealth(ctx context.Context, client *http.Client, endpoint string, cfg Config, credential string) relayObservation {
	unknown := relayObservation{Connection: "unknown"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unknown
	}
	req.SetBasicAuth("gameplane", credential)
	response, err := client.Do(req)
	if err != nil {
		return unknown
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return unknown
	}
	var status map[string][]struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Error  string `json:"err"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return unknown
	}
	count := 0
	allRegistered := true
	connection := "unavailable"
	for _, entry := range strings.Split(cfg.BackingServicePort, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ":")
		if len(parts) != 4 {
			return unknown
		}
		name, protocol := strings.TrimSpace(parts[0]), strings.ToLower(strings.TrimSpace(parts[3]))
		found := false
		for _, proxy := range status[protocol] {
			if proxy.Name == name && proxy.Type == protocol {
				// These states belong to an authenticated control session.
				// A rejected registration is distinct from a missing connection.
				if proxy.Status == "running" || proxy.Status == "start error" || proxy.Status == "wait start" {
					connection = "connected"
				}
				if proxy.Status == "running" && proxy.Error == "" {
					found = true
				}
			}
		}
		if !found {
			allRegistered = false
		}
		count++
	}
	if count == 0 {
		return unknown
	}
	return relayObservation{Connection: connection, Forwards: allRegistered, Assigned: allRegistered}
}

func playitHealthObservation(state playitState, ports []backingPort) relayObservation {
	// v1.0.10's IPC reports cached API assignment, not relay heartbeat. Its
	// daemon retains Running on refresh errors. Keep connection unknown even
	// when addresses exist; polling cached state cannot renew a health lease.
	assigned := state.Phase == "running" && len(ports) > 0 && len(playitEndpoints(state.Tunnels, ports)) == len(ports)
	return relayObservation{Connection: "unknown", Assigned: assigned}
}

func validFrpIdentity(uid string) bool {
	if len(uid) == 0 || len(uid) > 128 {
		return false
	}
	for _, r := range uid {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
