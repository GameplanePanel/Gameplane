// Package main implements a hand-rolled join-depth probe for Don't Starve
// Together, used by the e2e suite to measure how far a real client can get
// against a running server (A2S query on the Steam query port for depth,
// plus a purely diagnostic raw UDP poke at the game port).
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
	// Register standard flags.
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline; the probe retries until the server is playable or this elapses")
	expectDepthStr := flag.String("expect-depth", "QUERY", "expected join depth: QUERY | PARTIAL | JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth; exits 0 on correct failure")
	flag.Parse()

	if *addr == "" {
		fmt.Fprintf(os.Stderr, "probe: -addr is required\n")
		fmt.Println("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tBad flag: -addr is required")
		os.Exit(1)
	}

	expectDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe: invalid -expect-depth: %v\n", err)
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tBad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED\n")
		os.Exit(1)
	}

	log.SetFlags(log.Ltime)

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	verdict := probeDontStarveTogether(ctx, *addr, expectDepth, *expectFail)
	verdictLine, err := verdict.Encode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe: failed to encode verdict: %v\n", err)
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tFailed to encode verdict: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(verdictLine)

	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectDepth, *expectFail)
	os.Exit(exitCode)
}

// unknownDepth is a sentinel JoinDepth value that encodes as "UNKNOWN" in VERDICT lines.
// It is used when the probe never reached any measurable depth (e.g., transport failure).
const unknownDepth = joindepth.JoinDepth(-1)

// probeDontStarveTogether attempts to reach the deepest join depth on a Don't
// Starve Together server.
//
// DST is a Steamworks GameServer title (Steam AppID 322330): like the other
// four games in this bucket, its Steamworks GameServer integration answers
// Valve's A2S (Source Engine Query) protocol on a dedicated Steam query port,
// independent of the game's own ENet-based join protocol on the game port.
// node-gamedig's "dst" game definition confirms this: `port: 10999,
// port_query: 27016, protocol: 'valve'` — plain A2S, no per-game protocol
// override. The community-documented cluster.ini [STEAM] section corroborates
// the port number: a DST master shard's `master_server_port` defaults to
// 27016.
// References:
//   - https://github.com/gamedig/node-gamedig (lib/games.js, "dst" entry)
//   - https://dontstarve.wiki.gg/wiki/Guides/Don%E2%80%99t_Starve_Together_Dedicated_Servers
//     (cluster.ini [STEAM] master_server_port)
//
// An earlier version of this probe sent a single invented byte (0x00) to the
// game port (10999) and required any reply. That is not falsifiable: DST's
// game port speaks ENet, a reliable-UDP layer that does not answer arbitrary
// unrecognized datagrams — the same failure mode proven empirically against
// Factorio's query port (it silently drops unrecognized packets, so a probe
// requiring a response to a made-up packet fails permanently against a
// healthy server). A2S_INFO on the Steam query port is the actual documented
// request a real server answers, so it is used here instead.
//
// CAVEAT (unverified): unlike the other four games in this bucket, port 27016
// is not mentioned anywhere in the jamesits/dst-server-docker image's own
// documentation (which calls out only 10999/11000 for players to open), and
// this probe has never been run against a real server — DST is in the heavy
// set (opt-in only, never runs in CI; see spec.md "Measured connectivity").
// If a real run shows the port never responds — e.g. because this image's
// entrypoint never initializes the Steamworks GameServer object without a
// Steam cluster token, which CI does not set — this assertion needs to be
// revisited. See spec.md for what to do if that happens.
//
// Depth measurement: Returns QUERY depth and a ProbeVerdict if the Steam query
// port answers A2S_INFO. Returns an error in the ProbeVerdict if the query fails.
func probeDontStarveTogether(ctx context.Context, addr string, _ joindepth.JoinDepth, _ bool) *joindepth.ProbeVerdict {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return &joindepth.ProbeVerdict{
			ReachedDepth: unknownDepth,
			Detail:       fmt.Sprintf("Failed to parse address: %v", err),
			Err:          fmt.Errorf("parse addr: %w", err),
		}
	}

	// Steam query port is 27016 (per node-gamedig's port_query and DST's
	// documented cluster.ini [STEAM] master_server_port default).
	queryAddr := net.JoinHostPort(host, "27016")

	var info *a2s.Info
	var lastErr error
	err = retryWithDeadline(ctx, "a2s-info", 15*time.Second, func(actx context.Context) error {
		var qerr error
		info, qerr = a2s.QueryInfo(actx, queryAddr)
		lastErr = qerr
		return qerr
	})

	if err == nil && info != nil {
		log.Printf("a2s query succeeded: server=%q map=%q players=%d/%d",
			info.Name, info.Map, info.Players, info.MaxPlayers)
		detail := fmt.Sprintf("A2S_INFO response received: server %q, map %q, %d/%d players online",
			info.Name, info.Map, info.Players, info.MaxPlayers)
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       detail,
			Err:          nil,
		}
	}

	// A2S never succeeded before the deadline. Send one bounded, best-effort
	// diagnostic probe at the game port purely for evidence — it cannot
	// change the outcome below. Silence is itself a measurement: future
	// readers need to know a raw probe was attempted and what (if anything)
	// came back.
	diagCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	logRawDiagnostic(diagCtx, net.JoinHostPort(host, "10999"))

	// Classify the error as transport or internal.
	if joindepth.IsTransportError(err) {
		return &joindepth.ProbeVerdict{
			ReachedDepth: unknownDepth,
			Detail:       fmt.Sprintf("Dial timeout or connection refused on %s", queryAddr),
			Err:          fmt.Errorf("a2s query on steam query port %s timed out or connection refused: %w", queryAddr, lastErr),
		}
	}

	return &joindepth.ProbeVerdict{
		ReachedDepth: unknownDepth,
		Detail:       fmt.Sprintf("A2S query failed: %v", lastErr),
		Err:          fmt.Errorf("a2s query on steam query port %s never succeeded: %w", queryAddr, lastErr),
	}
}

// retryWithDeadline calls fn until it succeeds, ctx expires, or fn reports a fatal error.
func retryWithDeadline(ctx context.Context, what string, attempt time.Duration, fn func(context.Context) error) error {
	const retryInterval = 3 * time.Second
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("%s never succeeded before the deadline: %w", what, lastErr)
		}

		actx, cancel := context.WithTimeout(ctx, attempt)
		err := fn(actx)
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

// logRawDiagnostic sends a minimal, non-protocol UDP probe and logs whatever
// comes back, or explicitly logs that nothing did. It is purely diagnostic:
// its outcome is never used to determine probe success or failure, and it
// runs with its own short, independent timeout rather than the (by this
// point, likely expired) probe deadline.
func logRawDiagnostic(ctx context.Context, addr string) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		log.Printf("raw-diagnostic: dial %s failed: %v", addr, err)
		return
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(2 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte{0x00}); err != nil {
		log.Printf("raw-diagnostic: write to %s failed: %v", addr, err)
		return
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		log.Printf("raw-diagnostic: no response from %s within the diagnostic window (%v)", addr, err)
		return
	}

	hexResp := hex.EncodeToString(buf[:n])
	if len(hexResp) > 512 {
		hexResp = hexResp[:512] + "... (truncated)"
	}
	log.Printf("raw-diagnostic: %s replied to a non-protocol probe, len=%d hex=%s", addr, n, hexResp)
}
