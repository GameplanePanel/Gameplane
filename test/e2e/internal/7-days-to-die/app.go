// Package main implements a hand-rolled join-depth probe for 7 Days to Die,
// used by the e2e suite to measure how far a real client can get against a
// running server (A2S query, then a plain TCP fallback).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	a2s "github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/a2sproto"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

func main() {
	// Parse standard flags
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS or direct IP)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline for probe execution")
	expectDepthStr := flag.String("expect-depth", "JOINED", "expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach the expected depth; exit 0 if it correctly failed")
	flag.Parse()

	// Validate required flags
	if *addr == "" {
		reportVerdict(&joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("missing required flag -addr"),
		}, 1)
	}

	// Parse expected depth
	expectedDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		reportVerdict(&joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       fmt.Sprintf("Bad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED; got %q", *expectDepthStr),
			Err:          fmt.Errorf("invalid -expect-depth: %w", err),
		}, 1)
	}

	// Set up context with deadline
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	// Run the probe
	verdict := probeSevenDaysToDay(ctx, *addr)

	// Determine exit code based on expectFail flag and verdict
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	reportVerdict(verdict, exitCode)
}

// reportVerdict emits the machine-readable VERDICT line and exits.
func reportVerdict(verdict *joindepth.ProbeVerdict, exitCode int) {
	verdictLine, err := verdict.Encode()
	if err != nil {
		// Invariant violation: JOINED without Detail
		fmt.Fprintf(os.Stderr, "VERDICT encoding error: %v\n", err)
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\t%s\t%s\n", verdict.ReachedDepth.String(), "Invalid verdict: JOINED requires Detail")
		os.Exit(1)
	}

	// Emit the machine-readable verdict line
	fmt.Println(verdictLine)
	os.Exit(exitCode)
}

// probeSevenDaysToDay attempts to measure join depth on a 7 Days to Die server.
//
// 7 Days to Die (Unity engine) declares multiple ports:
//   - TCP 26900: primary game port
//   - UDP 26900: game port (redundant, same number as TCP)
//   - UDP 26901: secondary protocol port
//   - UDP 26902: tertiary protocol port
//   - TCP 8081: telnet console (no password wiring possible in this template)
//
// node-gamedig's "sdtd" game definition documents A2S (Valve/Source Engine
// Query) support at `port: 26900, port_query_offset: 1` — the query port is
// the game port PLUS ONE (26901/UDP), not the game port itself. Its "sdtd"
// protocol implementation is literally `class sdtd extends Valve`: the same
// A2S_INFO/A2S_PLAYER wire format as every other Source-family game, with an
// optional telnet enrichment step layered on top that this probe does not use
// (the telnet password is not wired in this template).
// Reference: https://github.com/gamedig/node-gamedig
// (lib/games.js "sdtd" entry; protocols/sdtd.js).
//
// This probe:
//  1. Attempts A2S_INFO on the query port (26901/UDP), using up to half of
//     whatever deadline remains.
//  2. If A2S never succeeds, falls back to a plain TCP dial on the declared
//     primary game port (26900/TCP), using the rest of the deadline — a real
//     TCP accept is a falsifiable signal a live server actually grants, even
//     without understanding 7DtD's join protocol.
//  3. Without joining the game (credentials unknown), QUERY is the maximum
//     measurable depth either way.
func probeSevenDaysToDay(ctx context.Context, addr string) *joindepth.ProbeVerdict {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       fmt.Sprintf("Failed to parse address: %v", err),
			Err:          fmt.Errorf("parse addr: %w", err),
		}
	}
	queryAddr := net.JoinHostPort(host, "26901")

	// Give A2S up to half of whatever deadline remains, so the TCP fallback
	// below is guaranteed a genuine share of the budget rather than starting
	// with an already-expired context. If ctx somehow carries no deadline,
	// fall back to a fixed budget so this phase still can't run forever.
	a2sBudget := 90 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 {
			a2sBudget = remaining / 2
		}
	}
	a2sCtx, cancel := context.WithTimeout(ctx, a2sBudget)

	var info *a2s.Info
	a2sErr := retryA2S(a2sCtx, queryAddr, &info)
	cancel()

	if a2sErr == nil && info != nil {
		log.Printf("a2s: server=%q map=%q players=%d/%d", info.Name, info.Map, info.Players, info.MaxPlayers)
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       fmt.Sprintf("A2S_INFO response: %q, map %q, %d/%d players", info.Name, info.Map, info.Players, info.MaxPlayers),
			Err:          nil,
		}
	}
	log.Printf("a2s-query: never succeeded on %s (%v); falling back to tcp connectivity on %s", queryAddr, a2sErr, addr)

	// A2S never succeeded. Fall back to plain TCP connectivity on the
	// declared primary game port, using whatever budget remains on the
	// ORIGINAL ctx (not the expired a2sCtx sub-budget) — a real TCP accept is
	// a falsifiable signal a live server actually grants.
	tcpErr := retryTCP(ctx, addr)
	if tcpErr == nil {
		// TCP connectivity succeeded, but A2S did not: the server is
		// listening but did not answer A2S_INFO within this run. Without
		// official protocol documentation for anything deeper, or
		// credentials for a real join, QUERY depth is all this establishes.
		log.Printf("tcp-connectivity: server is listening, but A2S query on %s was not answered", queryAddr)
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       fmt.Sprintf("TCP connection established to %s (A2S query failed)", addr),
			Err:          nil,
		}
	}

	// Both A2S and TCP failed; this is a transport error.
	return &joindepth.ProbeVerdict{
		ReachedDepth: joindepth.JoinDepth(-1),
		Detail:       fmt.Sprintf("Transport failure: A2S query on %s and TCP dial to %s both failed", queryAddr, addr),
		Err:          fmt.Errorf("7dtd server not reachable: tcp dial to %s failed: %w (a2s query on %s: %w)", addr, tcpErr, queryAddr, a2sErr),
	}
}

// retryA2S performs a retry loop for A2S queries until success or deadline.
func retryA2S(ctx context.Context, queryAddr string, info **a2s.Info) error {
	const retryInterval = 3 * time.Second
	const attemptTimeout = 15 * time.Second
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("a2s-query: never succeeded before deadline: %w", lastErr)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		qinfo, err := a2s.QueryInfo(attemptCtx, queryAddr)
		cancel()

		if err == nil && qinfo != nil {
			*info = qinfo // Capture the result
			return nil    // Success
		}

		lastErr = err
		log.Printf("a2s-query not ready: %v", err)

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return fmt.Errorf("a2s-query: deadline reached: %w", lastErr)
		case <-time.After(retryInterval):
		}
	}
}

// retryTCP performs a retry loop for TCP connectivity until success or deadline.
func retryTCP(ctx context.Context, addr string) error {
	const retryInterval = 3 * time.Second
	const attemptTimeout = 15 * time.Second
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("tcp-connectivity: never succeeded before deadline: %w", lastErr)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		d := net.Dialer{}
		conn, derr := d.DialContext(attemptCtx, "tcp", addr)
		cancel()

		if derr == nil {
			_ = conn.Close()
			log.Printf("tcp-connectivity: connection established to %s", addr)
			return nil // Success
		}

		lastErr = derr
		log.Printf("tcp-connectivity not ready: %v", derr)

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return fmt.Errorf("tcp-connectivity: deadline reached: %w", lastErr)
		case <-time.After(retryInterval):
		}
	}
}
