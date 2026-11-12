package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/GameplanePanel/gameplane/tunnel/internal/relayload"
)

func TestMeasureEcho(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() { done <- serveEcho(ctx, conn) }()
	var output bytes.Buffer
	p := relayload.Profile{Clients: 2, Hz: 16, Bytes: 64, Seconds: 1}
	if err := run(ctx, "", p, []string{conn.LocalAddr().String()}, &output); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("echo shutdown: %v", err)
	}
	var result relayload.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Scheduled != 32 || result.Received < 2 || result.Received > result.Sent || result.RTTP99ms == nil || result.JitterP95ms == nil {
		t.Fatalf("unexpected direct profile: %+v", result)
	}
	if err := run(ctx, "", p, nil, &output); err == nil {
		t.Fatal("accepted no target")
	}
	if err := run(ctx, "", relayload.Profile{}, []string{"127.0.0.1:1"}, &output); err == nil {
		t.Fatal("accepted invalid profile")
	}
}
