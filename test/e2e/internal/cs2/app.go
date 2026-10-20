// Package main implements a hand-rolled join-depth probe for CS2, used by
// the e2e suite to measure how far a real client can get against a running
// server (A2S query for depth, plus a purely diagnostic Source protocol
// handshake attempt).
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
	// Parse standard flags: -addr, -deadline, -expect-depth, -expect-fail
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline")
	expectDepthStr := flag.String("expect-depth", "JOINED", "expected join depth: QUERY | PARTIAL | JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth")
	flag.Parse()

	log.SetFlags(log.Ltime)

	// Validate -addr flag
	if *addr == "" {
		verdictLine := fmt.Sprintf("VERDICT\t%s\t%s\t%s",
			joindepth.FailInternalError,
			"UNKNOWN",
			"probe: -addr is required")
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Parse -expect-depth flag
	expectedDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		verdictLine := fmt.Sprintf("VERDICT\t%s\t%s\t%s",
			joindepth.FailInternalError,
			"UNKNOWN",
			fmt.Sprintf("invalid -expect-depth: %v", err))
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Create a context bounded by the deadline
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	// Run the probe
	measuredDepth, probeErr := probeCS2(ctx, *addr)

	// Build the verdict
	verdict := &joindepth.ProbeVerdict{
		ReachedDepth: measuredDepth,
		Detail:       getDetailForDepth(measuredDepth, probeErr),
		Err:          probeErr,
	}

	// Handle -expect-fail logic
	if *expectFail {
		if measuredDepth == expectedDepth && probeErr == nil {
			// Probe reached the expected depth when it should not have — test failure
			verdictLine := fmt.Sprintf("VERDICT\t%s\t%s\t%s",
				joindepth.FailNegativeControlReached,
				measuredDepth.String(),
				fmt.Sprintf("reached %s when expected to fail", measuredDepth.String()))
			fmt.Println(verdictLine)
			os.Exit(1)
		}
		// Probe correctly failed (transport error, wrong depth, or internal error)
		verdictLine, encErr := verdict.Encode()
		if encErr != nil {
			fmt.Printf("VERDICT\t%s\t%s\t%s\n",
				joindepth.FailInternalError,
				"UNKNOWN",
				fmt.Sprintf("verdict encoding error: %v", encErr))
			os.Exit(1)
		}
		fmt.Println(verdictLine)
		os.Exit(0)
	}

	// Normal (positive control) case
	if measuredDepth == expectedDepth && probeErr == nil {
		verdictLine, encErr := verdict.Encode()
		if encErr != nil {
			fmt.Printf("VERDICT\t%s\t%s\t%s\n",
				joindepth.FailInternalError,
				"UNKNOWN",
				fmt.Sprintf("verdict encoding error: %v", encErr))
			os.Exit(1)
		}
		fmt.Println(verdictLine)
		os.Exit(0)
	}

	// Failure: reached wrong depth or encountered error
	verdictLine, encErr := verdict.Encode()
	if encErr != nil {
		fmt.Printf("VERDICT\t%s\t%s\t%s\n",
			joindepth.FailInternalError,
			"UNKNOWN",
			fmt.Sprintf("verdict encoding error: %v", encErr))
		os.Exit(1)
	}
	fmt.Println(verdictLine)

	// Determine exit code
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	os.Exit(exitCode)
}

// getDetailForDepth returns evidence string for the measured depth
func getDetailForDepth(depth joindepth.JoinDepth, err error) string {
	if err != nil {
		if joindepth.IsTransportError(err) {
			return fmt.Sprintf("Transport error: %v", err)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	switch depth {
	case joindepth.QUERY:
		return "A2S query response received"
	case joindepth.PARTIAL:
		return "Server accepted handshake intent but not completed"
	case joindepth.JOINED:
		return "Server accepted connection and join completed"
	default:
		return "Unknown depth"
	}
}

// probeCS2 implements the CS2 join depth check: queries the server via A2S
// to verify connectivity and protocol support. The A2S query success determines
// the depth (QUERY). The Source protocol handshake attempt (Challenge + Connect)
// is purely diagnostic — it is logged richly but never changes the returned depth
// or fails the probe.
func probeCS2(ctx context.Context, addr string) (joindepth.JoinDepth, error) {
	// Query the server via A2S to verify it is listening and responding to queries.
	// CS2 is Source 2, which still supports the Source A2S query protocol.
	var info *a2s.Info
	if err := retryWithDeadline(ctx, "A2S query", 15*time.Second, func(actx context.Context) error {
		var err error
		info, err = a2s.QueryInfo(actx, addr)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return joindepth.JoinDepth(-1), fmt.Errorf("A2S QueryInfo failed: %w", err)
	}

	log.Printf("A2S query succeeded: server=%q players=%d/%d map=%q", info.Name, info.Players, info.MaxPlayers, info.Map)

	// A2S succeeded; the depth is QUERY. Now run the Source protocol handshake
	// diagnostically (Challenge + Connect). This is purely for evidence: does the
	// server speak Source protocol? Where does it fail? Log the outcome richly,
	// but never fail the probe or change the returned depth based on this attempt.
	connectProbe(ctx, addr)

	// Depth determined by A2S success alone.
	return joindepth.QUERY, nil
}

// connectProbe attempts a Source protocol Challenge + Connect handshake
// and logs the outcome richly for debugging. It is purely diagnostic and
// never affects the probe outcome.
func connectProbe(ctx context.Context, addr string) {
	var challenge uint32
	var challengeOk bool
	err := retryWithDeadline(ctx, "Source challenge", 15*time.Second, func(actx context.Context) error {
		var err error
		challenge, err = source.Challenge(actx, addr)
		if err != nil {
			return err
		}
		challengeOk = true
		return nil
	})

	if err != nil {
		log.Printf("connect-probe: challenge failed (server does not speak Source): %v", err)
		return
	}

	if !challengeOk {
		log.Printf("connect-probe: challenge did not complete")
		return
	}

	log.Printf("connect-probe: challenge obtained")

	// Attempt to connect with the challenge. The bot name is fixed.
	var result *source.ConnectResult
	err = retryWithDeadline(ctx, "Source connect", 15*time.Second, func(actx context.Context) error {
		r, err := source.Connect(actx, addr, challenge, "gameplane-e2e-bot", source.ProtocolSource2)
		if err != nil {
			return err
		}
		result = r
		return nil
	})

	if err != nil {
		log.Printf("connect-probe: connect attempt failed: %v", err)
		return
	}

	if result == nil {
		log.Printf("connect-probe: no response")
		return
	}

	// Log the connect response bytes for debugging.
	if len(result.Raw) > 0 {
		hexStr := hex.EncodeToString(result.Raw)
		// Bound to avoid huge logs; truncate if necessary.
		if len(hexStr) > 256 {
			hexStr = hexStr[:256] + "..."
		}
		log.Printf("connect-probe: response received (length=%d, hex=%s)", len(result.Raw), hexStr)
	}

	if !result.Accepted {
		// Connection was rejected.
		log.Printf("connect-probe: connect rejected: %s", result.RejectMsg)
		return
	}

	log.Printf("connect-probe: connect accepted")
}

// retryWithDeadline calls fn until it succeeds, ctx expires, or an internal error occurs.
// This implements the retry loop without importing the shared probe package.
func retryWithDeadline(ctx context.Context, what string, attemptTimeout time.Duration, fn func(context.Context) error) error {
	const retryInterval = 3 * time.Second
	var lastErr error

	for {
		// Check if context has expired
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("%s never succeeded before the deadline: %w", what, lastErr)
		}

		// Create a per-attempt timeout
		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := fn(actx)
		cancel()

		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("%s not ready yet: %v", what, err)

		// Wait before retrying, respecting context cancellation
		select {
		case <-ctx.Done():
		case <-time.After(retryInterval):
		}
	}
}
