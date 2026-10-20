// Package main implements a hand-rolled join-depth probe for Garry's Mod,
// used by the e2e suite to measure how far a real client can get against a
// running server (A2S query for depth, plus a purely diagnostic Source
// protocol challenge/connect attempt).
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	a2s "github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/a2sproto"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
	source "github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/sourceproto"
)

func main() {
	log.SetFlags(log.Ltime)

	// Parse flags.
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline; the probe retries until the server is playable or this elapses")
	expectDepthStr := flag.String("expect-depth", "JOINED", "expected join depth: QUERY | PARTIAL | JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth; exits 0 on correct failure, exits 1 or 2 if it somehow succeeded")
	flag.Parse()

	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JOINED, // unused for error case
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("missing required flag: -addr"),
		}
		line, _ := verdict.Encode()
		fmt.Println(line)
		os.Exit(1)
	}

	expectedDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JOINED,
			Detail:       fmt.Sprintf("Bad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED (got %q)", *expectDepthStr),
			Err:          err,
		}
		line, _ := verdict.Encode()
		fmt.Println(line)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	depth, evidence, err := probeGarrysMod(ctx, *addr)

	verdict := &joindepth.ProbeVerdict{
		ReachedDepth: depth,
		Detail:       evidence,
		Err:          err,
	}

	// Emit the VERDICT line.
	line, encodeErr := verdict.Encode()
	if encodeErr != nil {
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tFailed to encode verdict: %v\n", encodeErr)
		os.Exit(1)
	}
	fmt.Println(line)

	// Determine exit code based on -expect-fail flag.
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)

	os.Exit(exitCode)
}

// probeGarrysMod attempts to reach the deepest join depth on a Garry's Mod server.
// It queries A2S info (establishes QUERY depth).
// It attempts source protocol connection as diagnostic only (never changes the returned depth).
// Depth measurement: A2S success proves QUERY. Source protocol is optional instrumentation.
//
// Returns: (depth, evidence, error)
// - depth: the join depth reached (QUERY, PARTIAL, or JOINED)
// - evidence: a human-readable description of the concrete server-originated artifact proving the depth
// - error: a transport-level or protocol-level error, or nil if successful
func probeGarrysMod(ctx context.Context, addr string) (joindepth.JoinDepth, string, error) {
	// First: Query A2S to verify the server is alive and responding to queries.
	// This establishes QUERY depth and gives us server metadata.
	// A2S failure is fatal — server is not responding to queries at all.
	var info *a2s.Info
	if err := retryWithContext(ctx, "a2s-info", 15*time.Second, func(actx context.Context) error {
		var err error
		info, err = a2s.QueryInfo(actx, addr)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		// A2S failed; server not even responding to queries. This is a transport failure.
		if joindepth.IsTransportError(err) {
			return joindepth.JoinDepth(-1), "", fmt.Errorf("dial timeout or connection refused against %s; connection never established: %w", addr, err)
		}
		return joindepth.JoinDepth(-1), "", fmt.Errorf("a2s query failed: %w", err)
	}

	// Log server metadata from A2S response.
	log.Printf("a2s: server=%q map=%q players=%d/%d", info.Name, info.Map, info.Players, info.MaxPlayers)

	// Format evidence string for the A2S response.
	evidence := fmt.Sprintf("A2S_INFO response received: name=%q map=%q players=%d/%d", info.Name, info.Map, info.Players, info.MaxPlayers)

	// Depth is now QUERY because A2S succeeded.
	// The source protocol attempt below is purely diagnostic and will not change the returned depth.

	// Second: Attempt source protocol challenge and connect.
	// This is instrumentation only; the server may or may not speak source protocol,
	// and the connect may succeed or fail. We log the outcome for evidence (raw bytes, etc.)
	// but neither success nor failure changes our QUERY depth measurement.
	// Future CI runs can escalate to PARTIAL or JOINED once the connect format is verified.
	// attemptSourceConnect's own internal error is deliberately not surfaced
	// here: the source-protocol handshake is diagnostic-only, so its outcome
	// is folded into a plain success/fail boolean (connectOK) rather than an
	// error value that this function would then have to discard. A2S already
	// proved QUERY depth regardless of how this attempt goes.
	connectResult, connectOK := attemptSourceConnect(ctx, addr)
	if !connectOK {
		// Source protocol attempt failed (network, protocol error, etc.).
		// This is NOT fatal; A2S already proved QUERY depth.
		// No additional logging needed here; errors were logged in the retry callback.
		return joindepth.QUERY, evidence, nil
	}

	// Connect succeeded; decode and log the response for evidence.
	// Extract response type (byte 4, after the 0xFFFFFFFF header) for diagnosis.
	var respType string
	if len(connectResult.Raw) >= 5 {
		respType = fmt.Sprintf("0x%02x", connectResult.Raw[4])
	} else {
		respType = "truncated"
	}

	// Log outcome: either accepted or rejection reason.
	if connectResult.Accepted {
		log.Printf("connect-probe: response type %s — ACCEPTED (new client)", respType)
	} else if connectResult.RejectMsg != "" {
		// Log rejection reason; if it contains #GameUI_ token, extract and highlight it.
		log.Printf("connect-probe: response type %s — REJECTED: %s", respType, connectResult.RejectMsg)
	} else {
		log.Printf("connect-probe: response type %s — rejected (no reason provided)", respType)
	}

	// Also log raw hex for diagnosis, bounded to ~256 bytes to prevent log flooding.
	rawHex := hex.EncodeToString(connectResult.Raw)
	if len(rawHex) > 512 {
		rawHex = rawHex[:512] + "... (truncated)"
	}
	log.Printf("connect-probe: raw response (%d bytes): %s", len(connectResult.Raw), rawHex)

	// Regardless of connect outcome, return QUERY because that is what A2S proved.
	// The connect attempt was diagnostic; evidence is in the A2S response we already logged.
	return joindepth.QUERY, evidence, nil
}

// attemptSourceConnect runs the Source protocol challenge+connect handshake
// diagnostically and reports whether it completed. The caller treats a
// failed attempt as a non-fatal, expected outcome (A2S already proved QUERY
// depth), so this helper folds retryWithContext's error into a boolean
// rather than handing back an error the caller would only discard.
func attemptSourceConnect(ctx context.Context, addr string) (*source.ConnectResult, bool) {
	var connectResult *source.ConnectResult
	err := retryWithContext(ctx, "source-connect", 15*time.Second, func(actx context.Context) error {
		// Get a challenge token.
		challenge, err := source.Challenge(actx, addr)
		if err != nil {
			// Challenge failed; don't proceed to connect.
			// Log this as diagnostic but don't fail the probe.
			log.Printf("connect-probe: challenge failed: %v", err)
			return errFatal // Signal to stop retrying without failing the probe itself.
		}

		// Attempt to connect using the challenge.
		// Use a generic bot name; sv_lan 1 in the template should allow joins without Steam auth.
		result, err := source.Connect(actx, addr, challenge, "gameplane-e2e-bot", source.ProtocolSource1)
		if err != nil {
			// Connect failed; log as diagnostic.
			log.Printf("connect-probe: connect error: %v", err)
			return errFatal // Signal to stop retrying without failing the probe itself.
		}
		connectResult = result
		return nil
	})
	return connectResult, err == nil
}

// errFatal marks a failure that retrying cannot fix (a misconfigured server
// rather than one that is merely still booting), so the probe gives up at once.
var errFatal = fmt.Errorf("fatal")

// retryWithContext calls fn until it succeeds, ctx expires, or fn reports a fatal error.
// Every failed attempt is logged.
// If the overall deadline expires without success, the error message includes "Dial timeout"
// to ensure it's properly classified as a transport failure (exit code 3).
func retryWithContext(ctx context.Context, what string, attemptTimeout time.Duration, fn func(context.Context) error) error {
	const retryInterval = 3 * time.Second
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			// The word "timeout" in the message keeps this recognized as a
			// transport failure by joindepth.IsTransportError's keyword match.
			return fmt.Errorf("dial timeout: %s never succeeded before the deadline: %w", what, lastErr)
		}

		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := fn(actx)
		cancel()
		if err == nil {
			return nil
		}
		if fmt.Sprint(err) == fmt.Sprint(errFatal) {
			return err
		}
		lastErr = err
		log.Printf("%s not ready yet: %v", what, err)

		select {
		case <-ctx.Done():
		case <-time.After(retryInterval):
		}
	}
}
