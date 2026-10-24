package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

const tailscaleStateDir = "/var/lib/gameplane-tailscale"

type tailnetNode interface {
	Up(context.Context) (*ipnstate.Status, error)
	Status(context.Context) (*ipnstate.Status, error)
	Listen(string, string) (net.Listener, error)
	ListenPacket(string, string) (net.PacketConn, error)
}

type embeddedTailnet struct{ *tsnet.Server }

func (s embeddedTailnet) Up(ctx context.Context) (*ipnstate.Status, error) {
	status, err := s.Server.Up(ctx)
	if err != nil {
		return nil, err
	}
	return checkTailnetHostname(status, s.Hostname)
}

func (s embeddedTailnet) Status(ctx context.Context) (*ipnstate.Status, error) {
	client, err := s.LocalClient()
	if err != nil {
		return nil, err
	}
	status, err := client.StatusWithoutPeers(ctx)
	if err != nil {
		return nil, err
	}
	return checkTailnetHostname(status, s.Hostname)
}

func checkTailnetHostname(status *ipnstate.Status, hostname string) (*ipnstate.Status, error) {
	if status == nil || status.Self == nil {
		return nil, errors.New("tailnet identity unavailable")
	}
	name, _, _ := strings.Cut(status.Self.DNSName, ".")
	if name != hostname {
		return nil, errors.New("tailnet hostname differs from advertised endpoint; enable MagicDNS and remove conflicting device names")
	}
	return status, nil
}

func tailscaleServer(cfg Config, authKey string) (*tsnet.Server, []string, error) {
	tags, err := parseTailscaleTags(cfg.TailscaleTags)
	if err != nil {
		return nil, nil, fmt.Errorf("tailscale tags: %w", err)
	}
	if cfg.TailscaleHostname == "" || authKey == "" {
		return nil, nil, errors.New("tailscale hostname and authKey are required")
	}
	return &tsnet.Server{Hostname: cfg.TailscaleHostname, AuthKey: authKey, AdvertiseTags: tags, Dir: tailscaleStateDir,
		// Neither verbose daemon logs nor interactive login URLs belong in pod
		// logs. Readiness and sanitized retry messages expose failure instead.
		Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {},
	}, tags, nil
}

func runTailscale(ctx context.Context, cfg Config, authKey string) error {
	ports, err := tailscaleForwardPlan(cfg)
	if err != nil {
		return err
	}
	var ready atomic.Bool
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ":8091")
	if err != nil {
		return fmt.Errorf("tailscale health listener: %w", err)
	}
	health := &http.Server{Handler: tailscaleHealth(&ready), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	healthDone := make(chan error, 1)
	go func() { healthDone <- health.Serve(listener) }()
	defer func() { _ = health.Close() }()
	backoff := &exponentialBackoff{}
	for ctx.Err() == nil {
		server, tags, err := tailscaleServer(cfg, authKey)
		if err != nil {
			return err
		}
		err = serveTailnet(ctx, embeddedTailnet{server}, tags, ports, &ready, 2*time.Second)
		ready.Store(false)
		_ = server.Close()
		if ctx.Err() != nil {
			return nil
		}
		// Up errors may embed registration responses. Keep credentials and
		// login URLs out of logs; the operator exposes a pending condition.
		if err != nil {
			log.Print("Tailscale forwarding unavailable; check credentials, requested tagOwners, device approval, unique hostname, MagicDNS and tailnet connectivity")
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-healthDone:
			return fmt.Errorf("tailscale health server: %w", err)
		case <-time.After(backoff.next()):
		}
	}
	return nil
}

func tailscaleHealth(ready *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "tailnet or forwarding listeners unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func tailnetReady(status *ipnstate.Status, tags []string) bool {
	if status == nil || status.BackendState != "Running" || status.Self == nil || !status.Self.Online || len(status.TailscaleIPs) == 0 {
		return false
	}
	for _, tag := range tags {
		if status.Self.Tags == nil || !slices.Contains(status.Self.Tags.AsSlice(), tag) {
			return false
		}
	}
	return true
}

func serveTailnet(ctx context.Context, node tailnetNode, tags []string, ports []forwardPort, ready *atomic.Bool, poll time.Duration) error {
	ready.Store(false)
	defer ready.Store(false)
	upCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	status, err := node.Up(upCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("join tailnet: %w", err)
	}
	if !tailnetReady(status, tags) {
		return errors.New("tailnet is not online with the requested tags")
	}
	ips := append([]netip.Addr(nil), status.TailscaleIPs...)
	f := newForwarder(ctx, defaultForwardLimits)
	defer f.Close()
	for _, port := range ports {
		if port.network == "tcp" {
			listener, err := node.Listen("tcp", ":"+port.port)
			if err != nil {
				return fmt.Errorf("TCP listener %s: %w", port.port, err)
			}
			f.serveTCP(listener, port.target)
		} else {
			// tsnet requires an explicit IP for UDP. Bind both families so
			// MagicDNS choosing IPv6 cannot bypass the advertised datapath.
			for _, ip := range ips {
				listener, err := node.ListenPacket("udp", net.JoinHostPort(ip.String(), port.port))
				if err != nil {
					return fmt.Errorf("UDP listener %s: %w", port.port, err)
				}
				f.serveUDP(listener, port.target)
			}
		}
	}
	ready.Store(true)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-f.failed:
			return err
		case <-ticker.C:
			statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			status, err := node.Status(statusCtx)
			cancel()
			ready.Store(err == nil && tailnetReady(status, tags))
			if err == nil && status != nil && !slices.Equal(status.TailscaleIPs, ips) {
				return errors.New("tailnet addresses changed; recreating forwarding listeners")
			}
		}
	}
}
