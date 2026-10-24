package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type forwardPort struct{ network, port, target string }

// The operator supplies only advertised ports and this exact Service. No client
// packet can choose a destination, and this node never advertises subnet routes.
func tailscaleForwardPlan(cfg Config) ([]forwardPort, error) {
	if cfg.GameServerName == "" || cfg.GameServerNamespace == "" || cfg.BackingServiceDNS != cfg.GameServerName+"."+cfg.GameServerNamespace+".svc" {
		return nil, errors.New("tailscale target must be the GameServer's backing Service DNS")
	}
	entries := strings.Split(cfg.BackingServicePorts, ",")
	if len(entries) > 32 {
		return nil, errors.New("tailscale supports at most 32 advertised ports")
	}
	ports := make([]forwardPort, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		parts := strings.Split(entry, ":")
		if len(parts) != 3 || parts[0] == "" {
			return nil, fmt.Errorf("tailscale port %q must be name:port:tcp or name:port:udp; update the operator and tunnel images together", entry)
		}
		port, err := strconv.Atoi(parts[1])
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid Tailscale port %q", entry)
		}
		if parts[2] != "tcp" && parts[2] != "udp" {
			return nil, fmt.Errorf("unsupported Tailscale protocol %q; only TCP and UDP can be forwarded", parts[2])
		}
		key := parts[2] + ":" + strconv.Itoa(port)
		if seen[key] {
			return nil, fmt.Errorf("duplicate Tailscale listener %s", key)
		}
		seen[key] = true
		ports = append(ports, forwardPort{network: parts[2], port: strconv.Itoa(port), target: net.JoinHostPort(cfg.BackingServiceDNS, strconv.Itoa(port))})
	}
	return ports, nil
}

type forwardLimits struct {
	sessions, udpQueue int
	idle, dial         time.Duration
}

// The budget is shared by all listeners, protocols and address families.
// At most 128 sessions, four queued UDP packets each and one 64KiB response
// buffer per UDP session bound memory independent of client fan-out.
var defaultForwardLimits = forwardLimits{sessions: 128, udpQueue: 4, idle: 30 * time.Second, dial: 5 * time.Second}

type forwarder struct {
	ctx       context.Context
	cancel    context.CancelFunc
	limits    forwardLimits
	slots     chan struct{}
	wg        sync.WaitGroup
	listeners []io.Closer // registered before Close; no concurrent additions
	closeOnce sync.Once
	failed    chan error
}

func newForwarder(ctx context.Context, limits forwardLimits) *forwarder {
	ctx, cancel := context.WithCancel(ctx)
	return &forwarder{ctx: ctx, cancel: cancel, limits: limits, slots: make(chan struct{}, limits.sessions), failed: make(chan error, 1)}
}

func (f *forwarder) Close() {
	f.closeOnce.Do(func() {
		f.cancel()
		for _, listener := range f.listeners {
			_ = listener.Close()
		}
		f.wg.Wait()
	})
}

func (f *forwarder) fail(err error) {
	if f.ctx.Err() == nil {
		select {
		case f.failed <- err:
		default:
		}
	}
}

func (f *forwarder) acquire() bool {
	select {
	case f.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (f *forwarder) serveTCP(listener net.Listener, target string) {
	f.listeners = append(f.listeners, listener)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				f.fail(fmt.Errorf("TCP listener: %w", err))
				return
			}
			if !f.acquire() {
				_ = client.Close()
				continue
			}
			f.wg.Add(1)
			go func() { defer f.wg.Done(); defer func() { <-f.slots }(); f.proxyTCP(client, target) }()
		}
	}()
}

func (f *forwarder) proxyTCP(client net.Conn, target string) {
	defer func() { _ = client.Close() }()
	stopClient := context.AfterFunc(f.ctx, func() { _ = client.Close() })
	defer stopClient()
	upstream, err := (&net.Dialer{Timeout: f.limits.dial}).DialContext(f.ctx, "tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	stopUpstream := context.AfterFunc(f.ctx, func() { _ = upstream.Close() })
	defer stopUpstream()
	// Activity in either direction refreshes each socket's idle timeout.
	a := &idleConn{Conn: client, idle: 5 * time.Minute}
	b := &idleConn{Conn: upstream, idle: 5 * time.Minute}
	done := make(chan struct{})
	go func() { _, _ = io.CopyBuffer(b, a, make([]byte, 32*1024)); closeWrite(upstream); close(done) }()
	_, _ = io.CopyBuffer(a, b, make([]byte, 32*1024))
	closeWrite(client)
	_ = upstream.Close()
	_ = client.Close()
	<-done
}

func closeWrite(conn net.Conn) {
	if half, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
}

type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	_ = c.SetDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(p)
}
func (c *idleConn) Write(p []byte) (int, error) {
	_ = c.SetDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(p)
}

type udpForwardSession struct{ packets chan []byte }

func (f *forwarder) serveUDP(listener net.PacketConn, target string) {
	f.listeners = append(f.listeners, listener)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		var mu sync.Mutex
		sessions := map[string]*udpForwardSession{}
		buf := make([]byte, 65536)
		for {
			n, peer, err := listener.ReadFrom(buf)
			if err != nil {
				f.fail(fmt.Errorf("UDP listener: %w", err))
				return
			}
			if n > 65507 || f.ctx.Err() != nil {
				continue
			}
			key := peer.String()
			mu.Lock()
			session := sessions[key]
			if session == nil && f.acquire() {
				session = &udpForwardSession{packets: make(chan []byte, f.limits.udpQueue)}
				sessions[key] = session
				f.wg.Add(1)
				go func(session *udpForwardSession, peer net.Addr, key string) {
					defer f.wg.Done()
					defer func() { mu.Lock(); delete(sessions, key); mu.Unlock(); <-f.slots }()
					f.proxyUDP(listener, peer, session.packets, target)
				}(session, peer, key)
			}
			if session != nil {
				// A slow backend or full queue drops the datagram; neither makes
				// the receive loop wait, allocate workers, or grow memory.
				if len(session.packets) < cap(session.packets) {
					session.packets <- append([]byte(nil), buf[:n]...)
				}
			}
			mu.Unlock()
		}
	}()
}

func (f *forwarder) proxyUDP(listener net.PacketConn, peer net.Addr, packets <-chan []byte, target string) {
	upstream, err := (&net.Dialer{Timeout: f.limits.dial}).DialContext(f.ctx, "udp", target)
	if err != nil {
		return
	}
	stop := context.AfterFunc(f.ctx, func() { _ = upstream.Close() })
	defer stop()
	done := make(chan struct{})
	_ = upstream.SetReadDeadline(time.Now().Add(f.limits.idle))
	go func() {
		defer close(done)
		buf := make([]byte, 65536)
		for {
			n, err := upstream.Read(buf)
			if err != nil {
				return
			}
			if n <= 65507 {
				_ = listener.SetWriteDeadline(time.Now().Add(f.limits.dial))
				if _, err := listener.WriteTo(buf[:n], peer); err != nil {
					return
				}
			}
		}
	}()
	defer func() { _ = upstream.Close(); <-done }()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-done:
			return
		case packet := <-packets:
			_ = upstream.SetWriteDeadline(time.Now().Add(f.limits.dial))
			_ = upstream.SetReadDeadline(time.Now().Add(f.limits.idle))
			if _, err := upstream.Write(packet); err != nil {
				return
			}
		}
	}
}
