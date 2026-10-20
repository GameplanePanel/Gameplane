// Package main implements a hand-rolled join-depth probe for Enshrouded,
// used by the e2e suite to measure how far a real client can get against a
// running server (A2S query for depth, plus a purely diagnostic raw UDP
// poke at the query port).
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
	// Parse standard flags: -addr, -deadline, -expect-depth, -expect-fail
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline")
	expectDepth := flag.String("expect-depth", "JOINED", "expected join depth: JOINED | PARTIAL | QUERY")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth; exits 0 on correct failure")
	flag.Parse()

	// Validate required flags
	if *addr == "" {
		log.Println("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tBad flag: -addr is required")
		os.Exit(1)
	}

	expectedDepth, err := joindepth.Parse(*expectDepth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		log.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tBad flag: -expect-depth=%s must be one of QUERY, PARTIAL, JOINED", *expectDepth)
		os.Exit(1)
	}

	// Run the probe with the deadline
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	result := runProbe(ctx, *addr)

	// Handle -expect-fail logic
	if *expectFail {
		handleNegativeControl(result, expectedDepth)
	} else {
		handlePositiveControl(result, expectedDepth)
	}
}

// runProbe executes the probe and returns a ProbeResult
func runProbe(ctx context.Context, addr string) *ProbeResult {
	depth, detail, err := probeEnshrouded(ctx, addr)
	return &ProbeResult{
		ReachedDepth: depth,
		Detail:       detail,
		Err:          err,
	}
}

// handlePositiveControl handles the normal (non-negative-control) case
func handlePositiveControl(result *ProbeResult, expectedDepth joindepth.JoinDepth) {
	verdict := &joindepth.ProbeVerdict{
		ReachedDepth: result.ReachedDepth,
		Detail:       result.Detail,
		Err:          result.Err,
	}

	verdictLine, _ := verdict.Encode()
	log.Println(verdictLine)

	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, false)
	os.Exit(exitCode)
}

// handleNegativeControl handles the -expect-fail case
func handleNegativeControl(result *ProbeResult, expectedDepth joindepth.JoinDepth) {
	// Under -expect-fail, we want the probe to NOT reach the expected depth.
	// If it did reach it (and no error), that's a test failure.
	if result.ReachedDepth == expectedDepth && result.Err == nil {
		// Probe reached the depth when it should not have — test failure
		verdictLine := fmt.Sprintf("VERDICT\tFAIL_NEGATIVE_CONTROL_REACHED\t%s\t%s",
			result.ReachedDepth.String(), result.Detail)
		log.Println(verdictLine)
		os.Exit(1)
	}

	// Probe correctly failed (either transport error, wrong depth, or internal error)
	verdict := &joindepth.ProbeVerdict{
		ReachedDepth: result.ReachedDepth,
		Detail:       result.Detail,
		Err:          result.Err,
	}
	verdictLine, _ := verdict.Encode()
	log.Println(verdictLine)

	// Exit 0 because the negative control passed (probe correctly failed)
	os.Exit(0)
}

// ProbeResult represents the outcome of a probe attempt
type ProbeResult struct {
	ReachedDepth joindepth.JoinDepth
	Detail       string
	Err          error
}

// probeEnshrouded attempts to reach the deepest join depth on an Enshrouded server.
// Enshrouded has no console, no RCON, and no remote control of any kind (the
// shipped template declares rcon.protocol: none and consoleMode: none), so the
// query port is the only measurement surface available at all.
//
// The query port (UDP 15637) speaks Valve's A2S (Source Engine Query) protocol.
// Enshrouded's developers do not publish this directly, but it is confirmed by
// node-gamedig — a widely used, actively maintained open-source game-server
// query library: its "enshrouded" game definition declares
// `port: 15636, port_query: 15637, protocol: 'valve'`, i.e. plain A2S on the
// query port, using the exact same generic Source-query implementation it uses
// for hundreds of Source-family and Source-adjacent (Steamworks GameServer)
// titles — no per-game protocol override exists for Enshrouded, unlike e.g.
// 7 Days to Die's telnet-enriched variant.
// Reference: https://github.com/gamedig/node-gamedig (lib/games.js,
// "enshrouded" entry; GAMES_LIST.md lists it under "Valve Protocol").
//
// An earlier version of this probe sent a single invented byte (0x01) to the
// query port and required any reply. That is not falsifiable: a real server
// that only answers well-formed A2S requests would silently drop an
// unrecognized packet — the same failure mode proven empirically against
// Factorio's query port (it drops unrecognized UDP datagrams with no reply at
// all, so a probe requiring a response to a made-up packet fails permanently
// against a perfectly healthy server). A2S_INFO is the actual documented
// request this port answers, so it is used here instead.
//
// Depth measurement: Returns QUERY if the query port answers A2S_INFO.
// Returns UNKNOWN depth with an error if it never does before the deadline,
// since Enshrouded has no other control or query surface to fall back to.
// Evidence string cites the A2S_INFO response (server name, map, player count).
func probeEnshrouded(ctx context.Context, addr string) (joindepth.JoinDepth, string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return joindepth.JoinDepth(-1), "", fmt.Errorf("parse addr: %w", err)
	}

	// Query port is 15637 (per the module template and node-gamedig's port_query).
	queryAddr := net.JoinHostPort(host, "15637")

	var info *a2s.Info
	err = retryWithDeadline(ctx, "a2s-info", 15*time.Second, func(actx context.Context) error {
		var qerr error
		info, qerr = a2s.QueryInfo(actx, queryAddr)
		return qerr
	})
	if err == nil {
		detail := fmt.Sprintf("A2S_INFO response: server=%q, map=%q, players=%d/%d", info.Name, info.Map, info.Players, info.MaxPlayers)
		log.Printf("a2s query succeeded: %s", detail)
		return joindepth.QUERY, detail, nil
	}

	// A2S never succeeded before the deadline. Send one bounded, best-effort
	// diagnostic probe purely for evidence — it cannot change the outcome
	// below (which is already a failure). Silence is itself a measurement:
	// future readers need to know a raw probe was attempted and what (if
	// anything) came back, in case A2S support turns out to be wrong.
	diagCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	logRawDiagnostic(diagCtx, queryAddr)

	return joindepth.JoinDepth(-1), "", fmt.Errorf("a2s query on query port %s never succeeded before the deadline: %w", queryAddr, err)
}

// retryWithDeadline calls fn repeatedly until it succeeds, ctx expires, or fn reports ErrFatal.
// Every failed attempt is logged so a failed probe's pod logs explain why the
// server never became queryable.
func retryWithDeadline(ctx context.Context, what string, attemptTimeout time.Duration, fn func(context.Context) error) error {
	var lastErr error
	retryInterval := 3 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("%s never succeeded before the deadline: %w", what, lastErr)
		}

		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
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
