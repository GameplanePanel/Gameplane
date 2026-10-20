// Package main implements a hand-rolled join-depth probe for DayZ, used by
// the e2e suite to measure how far a real client can get against a running
// server (A2S query for depth, plus a purely diagnostic game-port UDP poke).
package main

import (
	"context"
	"encoding/hex"
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
	// Parse standard flags.
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "total elapsed time allowed before giving up")
	expectDepth := flag.String("expect-depth", "JOINED", "expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth; exits 0 on correct failure")
	flag.Parse()

	// Validate flags.
	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("missing required flag: -addr"),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	expectedDepth, err := joindepth.Parse(*expectDepth)
	if err != nil {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       fmt.Sprintf("Bad flag: -expect-depth=%s", *expectDepth),
			Err:          err,
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Run the probe with the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	reachedDepth, probeErr := probeDayZ(ctx, *addr)
	if probeErr != nil {
		log.Printf("probe error: %v", probeErr)
	}

	// Build the verdict.
	// Note: if probeErr is a transport error, reachedDepth will be an invalid value (-1)
	// which .String() will render as "UNKNOWN" in the verdict line.
	verdict := &joindepth.ProbeVerdict{
		ReachedDepth: reachedDepth,
		Err:          probeErr,
	}

	// Set detail based on depth and error.
	if reachedDepth == joindepth.QUERY {
		verdict.Detail = "Server responded to A2S query on UDP 27015"
	} else if probeErr != nil {
		// Transport failure: describe the connection-level event.
		verdict.Detail = probeErr.Error()
	}

	// Emit the verdict line.
	verdictLine, err := verdict.Encode()
	if err != nil {
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tFailed to encode verdict: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(verdictLine)

	// Determine exit code.
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	os.Exit(exitCode)
}

// probeDayZ attempts to reach the deepest join depth on a DayZ server.
// It queries A2S info on UDP 27015 (establishes QUERY depth).
// It attempts a diagnostic UDP probe on the game port 2302 to observe
// what the server responds with, but makes no depth claim from it
// (the game protocol is undocumented and BattlEye RCON is unreachable).
//
// Depth measurement: A2S success proves QUERY.
// Game port diagnostic is instrumentation only and does not change depth.
//
// Reference:
// - A2S Query: https://developer.valvesoftware.com/wiki/Server_queries
// - DayZ Enfusion/BattlEye protocol: not publicly documented.
// - BattlEye RCON: bound to 127.0.0.1 (unreachable from probe pod).
func probeDayZ(ctx context.Context, addr string) (joindepth.JoinDepth, error) {
	// Extract the host from the address (removing :port if present).
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// If SplitHostPort fails, try using the addr as-is.
		host = addr
	}

	// Build the A2S query address on port 27015.
	// The caller provides addr as <service>.<namespace>.svc.cluster.local:27015
	// but we dial A2S on the same host with explicit port.
	a2sAddr := net.JoinHostPort(host, "27015")

	// First: Query A2S to verify the server is alive and responding to queries.
	// This establishes QUERY depth and gives us server metadata.
	// A2S failure means server is not responding to queries at all.
	var info *a2s.Info
	if err := retryUntilDeadline(ctx, "a2s-info", 15*time.Second, func(actx context.Context) error {
		var err error
		info, err = a2s.QueryInfo(actx, a2sAddr)
		if err != nil {
			return fmt.Errorf("a2s query: %w", err)
		}
		return nil
	}); err != nil {
		// A2S failed; server not responding to queries. This is a transport failure (no depth reached).
		// Return an invalid JoinDepth value (-1), which will render as "UNKNOWN" in the verdict line.
		return joindepth.JoinDepth(-1), fmt.Errorf("a2s-info never succeeded before the deadline: %w", err)
	}

	// Log server metadata from A2S response.
	log.Printf("a2s: server=%q map=%q players=%d/%d", info.Name, info.Map, info.Players, info.MaxPlayers)

	// Depth is now QUERY because A2S succeeded.
	// The game port diagnostic below is purely observational and will not change the returned depth.

	// Second: Attempt a diagnostic UDP probe on the game port (2302).
	// The game protocol (Enfusion) is not publicly documented, so we cannot
	// speak it. We send a minimal probe to observe what the server responds with,
	// for evidence purposes only. If the server doesn't respond or responds with
	// something we cannot parse, we log it and move on — it does not affect depth.
	if err := retryUntilDeadline(ctx, "game-port-diagnostic", 10*time.Second, func(actx context.Context) error {
		gameAddr := net.JoinHostPort(host, "2302")
		return probeGamePort(actx, gameAddr)
	}); err != nil {
		// Game port probe failed; log it but don't fail the overall probe.
		// A2S already proved QUERY depth.
		log.Printf("game-port-diagnostic: %v (non-fatal; A2S already established QUERY)", err)
	}

	// Return QUERY because that is what A2S proved.
	// The game port attempt was diagnostic; evidence is in the logs above.
	return joindepth.QUERY, nil
}

// retryUntilDeadline calls fn until it succeeds, the context deadline expires, or fn returns an error.
// It retries internally within the deadline; a timeout at the deadline-level is fatal (not retried).
func retryUntilDeadline(ctx context.Context, what string, attemptDuration time.Duration, fn func(context.Context) error) error {
	const retryInterval = 3 * time.Second
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("%s never succeeded before the deadline: %w", what, lastErr)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptDuration)
		err := fn(attemptCtx)
		cancel()

		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("%s not ready yet: %v", what, err)

		select {
		case <-ctx.Done():
		case <-time.After(retryInterval):
		}
	}
}

// probeGamePort sends a minimal UDP probe to the game port and logs the response.
// This is purely diagnostic; the Enfusion protocol is undocumented.
func probeGamePort(ctx context.Context, addr string) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Set a read deadline so we don't block forever.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}

	// Send a minimal probe: a single zero byte to see if the server is listening.
	// This is not a real protocol message, just a diagnostic poke.
	if _, err := conn.Write([]byte{0x00}); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// Try to read a response (any response is interesting).
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		// Timeout or connection refused is expected for undocumented protocols.
		// Log it as non-fatal.
		return fmt.Errorf("read from game port: %w", err)
	}

	// Log what we received, bound to prevent log flooding.
	hexResp := hex.EncodeToString(buf[:n])
	if len(hexResp) > 256 {
		hexResp = hexResp[:256] + "... (truncated)"
	}
	log.Printf("game-port-diagnostic: server responded with %d bytes, hex=%s", n, hexResp)
	return nil
}
