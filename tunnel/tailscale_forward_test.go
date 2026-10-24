package main

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTailscaleForwardPlanRestrictsDestinations(t *testing.T) {
	cfg := Config{GameServerName: "game", GameServerNamespace: "games", BackingServiceDNS: "game.games.svc", BackingServicePorts: "java:25565:tcp,bedrock:19132:udp"}
	ports, err := tailscaleForwardPlan(cfg)
	if err != nil || len(ports) != 2 || ports[0].target != "game.games.svc:25565" || ports[1].network != "udp" {
		t.Fatalf("plan: %+v, %v", ports, err)
	}
	for _, bad := range []string{"other.games.svc", "127.0.0.1", "game.games.svc:80", "http://game.games.svc"} {
		cfg.BackingServiceDNS = bad
		if _, err := tailscaleForwardPlan(cfg); err == nil {
			t.Fatalf("accepted unrelated target %q", bad)
		}
	}
	cfg.BackingServiceDNS = "game.games.svc"
	for _, bad := range []string{"game:25565", "game:0:tcp", "game:65536:udp", "game:22:sctp", "game:22:tcp,other:22:tcp", strings.Repeat("p:1:tcp,", 33)} {
		cfg.BackingServicePorts = bad
		if _, err := tailscaleForwardPlan(cfg); err == nil {
			t.Fatalf("accepted invalid ports %q", bad)
		}
	}
}

func TestTailscaleTCPForwardAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		c, e := backend.Accept()
		if e == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newForwarder(ctx, forwardLimits{sessions: 2, udpQueue: 2, idle: time.Second, dial: time.Second})
	f.serveTCP(listener, backend.Addr().String())
	defer f.Close()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("gameplay"))
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "gameplay" {
		t.Fatalf("TCP reply %q: %v", buf, err)
	}
	f.Close()
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("connection survived forwarding shutdown")
	}
}

func TestTailscaleUDPForwardIsolationLimitAndExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		buf := make([]byte, 65536)
		for {
			n, peer, e := backend.ReadFrom(buf)
			if e != nil {
				return
			}
			_, _ = backend.WriteTo(buf[:n], peer)
		}
	}()
	listener, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newForwarder(ctx, forwardLimits{sessions: 2, udpQueue: 2, idle: time.Second, dial: time.Second})
	f.serveUDP(listener, backend.LocalAddr().String())
	defer f.Close()
	var clients []net.Conn
	for i := 0; i < 3; i++ {
		c, e := (&net.Dialer{}).DialContext(ctx, "udp", listener.LocalAddr().String())
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		clients = append(clients, c)
	}
	for i := 0; i < 2; i++ {
		_ = clients[i].SetDeadline(time.Now().Add(time.Second))
		payload := "peer-" + strconv.Itoa(i)
		_, _ = clients[i].Write([]byte(payload))
		buf := make([]byte, 32)
		n, e := clients[i].Read(buf)
		if e != nil || string(buf[:n]) != payload {
			t.Fatalf("peer %d reply %q: %v", i, buf[:n], e)
		}
	}
	_ = clients[2].SetDeadline(time.Now().Add(50 * time.Millisecond))
	_, _ = clients[2].Write([]byte("over limit"))
	if _, err := clients[2].Read(make([]byte, 32)); err == nil {
		t.Fatal("UDP session limit not enforced")
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(f.slots) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(f.slots) != 0 {
		t.Fatal("idle UDP sessions were not reclaimed")
	}
	_ = clients[2].SetDeadline(time.Now().Add(time.Second))
	_, _ = clients[2].Write([]byte("new session"))
	buf := make([]byte, 32)
	n, err := clients[2].Read(buf)
	if err != nil || string(buf[:n]) != "new session" {
		t.Fatalf("session after expiry: %q %v", buf[:n], err)
	}
	f.Close()
	if len(f.slots) != 0 {
		t.Fatal("shutdown leaked sessions")
	}
}
