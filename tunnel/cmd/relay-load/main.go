// Command relay-load measures the same open-loop UDP profile on each target.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/GameplanePanel/gameplane/tunnel/internal/relayload"
)

func main() {
	var profile relayload.Profile
	flag.IntVar(&profile.Clients, "clients", 32, "UDP sockets per target")
	flag.IntVar(&profile.Hz, "hz", 128, "packets per second per socket")
	flag.IntVar(&profile.Bytes, "bytes", 256, "bytes per packet")
	flag.IntVar(&profile.Seconds, "seconds", 10, "offered load duration per target")
	echo := flag.String("echo", "", "listen for UDP echo on this host:port instead of measuring")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := run(ctx, *echo, profile, flag.Args(), os.Stdout)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, echo string, p relayload.Profile, endpoints []string, output io.Writer) error {
	if echo != "" {
		conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", echo)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		defer func() { _ = conn.Close() }()
		return serveEcho(ctx, conn)
	}
	if len(endpoints) == 0 {
		return errors.New("specify a direct host:port followed by the relay targets, or use -echo host:port")
	}
	enc := json.NewEncoder(output)
	for _, endpoint := range endpoints {
		result, err := relayload.Measure(ctx, endpoint, p)
		if err != nil {
			return err
		}
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("encode result: %w", err)
		}
	}
	return nil
}

func serveEcho(ctx context.Context, conn net.PacketConn) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	buf := make([]byte, 1401)
	for ctx.Err() == nil {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read echo: %w", err)
		}
		if n >= 16 && n <= 1400 {
			if _, err := conn.WriteTo(buf[:n], peer); err != nil {
				return fmt.Errorf("write echo: %w", err)
			}
		}
	}
	return ctx.Err()
}
