// Package main implements a hand-rolled join-depth probe for Rust, used by
// the e2e suite to measure how far a real client can get against a running
// server (A2S query for depth, if the server answers it).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/probe"
	a2s "github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/a2sproto"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

func main() {
	log.SetFlags(log.Ltime)

	// Parse standard flags and -expect-fail.
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline for probe")
	expectDepthStr := flag.String("expect-depth", "JOINED", "expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth")
	flag.Parse()

	// Validate flags.
	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("missing -addr flag"),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	expectedDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       fmt.Sprintf("Bad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED; got %q", *expectDepthStr),
			Err:          err,
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Run the probe with deadline.
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	verdict := probeRust(ctx, *addr, expectedDepth, *expectFail)

	// Emit the VERDICT line to stdout.
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

// probeRust attempts to reach the deepest join depth on a Rust server.
// Rust's game protocol is RakNet-derived and not publicly documented;
// real joins require Steam identity which CI cannot mint.
// This probe measures QUERY depth via A2S query (if available).
//
// Returns a ProbeVerdict that captures the depth reached and any errors encountered.
// Errors are classified as either transport-level (connection refused, timeout, DNS)
// or protocol-level (reached a live listener but wrong depth).
//
// Path B strategy:
//   - Rust servers MAY answer A2S_INFO queries on the game port (28015).
//     This is not guaranteed and depends on server configuration, but many
//     Rust servers do respond to A2S queries for compatibility with query tools.
//   - If A2S succeeds, we establish QUERY depth and log server metadata.
//   - If A2S fails, the server is not responding to standard query protocols,
//     and QUERY depth cannot be established via Path B alone.
//
// We do NOT use WebSocket RCON (port 28016) for depth measurement because:
//   - The RCON password is only known to the agent (it's injected as an env var).
//   - Dialing RCON from the probe would require secretless auth, which is not
//     how CI works — the probe binary is read-only and receives no secrets.
//   - The agent (Path A) will test RCON independently as part of the heartbeat.
//   - Using RCON here would blur the distinction between Path A (Gameplane) and
//     Path B (independent server protocol).
func probeRust(ctx context.Context, addr string, expectedDepth joindepth.JoinDepth, expectFail bool) *joindepth.ProbeVerdict {
	// Attempt A2S query on the game port.
	// Rust servers may or may not respond to A2S; if they don't, this is a
	// transport-level failure (exit code 3).
	var info *a2s.Info
	var a2sErr error
	if err := probe.Retry(ctx, "a2s-info", 15*time.Second, func(actx context.Context) error {
		var err error
		info, err = a2s.QueryInfo(actx, addr)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		// A2S query failed. This could mean:
		// 1. The server is not yet ready (boot in progress).
		// 2. The server doesn't answer A2S queries (some Rust configs disable it).
		// 3. Network path is blocked (unlikely in-cluster, but possible).
		// 4. Nothing is listening at all (connection refused, DNS failure, timeout).
		a2sErr = err
		log.Printf("a2s-info failed (non-fatal; Rust may not answer A2S): %v", err)
	}

	// If A2S succeeded, we have QUERY depth.
	if info != nil {
		log.Printf("a2s: server=%q map=%q players=%d/%d version=%q", info.Name, info.Map, info.Players, info.MaxPlayers, info.Version)
		reachedDepth := joindepth.QUERY
		detail := fmt.Sprintf("A2S_INFO response received: server=%q map=%q", info.Name, info.Map)

		if expectFail {
			// Under -expect-fail, reaching QUERY when we expected it to fail is a test failure.
			// Do NOT set Err; the ExitCodeFromVerdict function uses the absence of Err
			// to detect when the probe unexpectedly succeeded (returns 1 or 2).
			if reachedDepth == expectedDepth {
				return &joindepth.ProbeVerdict{
					ReachedDepth: reachedDepth,
					Detail:       detail + "; expected to fail but succeeded",
					Err:          nil,
				}
			}
			// Otherwise, depth mismatch under -expect-fail is a success (probe correctly failed).
			return &joindepth.ProbeVerdict{
				ReachedDepth: reachedDepth,
				Detail:       detail + "; expected to fail, and did (depth mismatch)",
				Err:          nil,
			}
		}

		// Normal case (not -expect-fail).
		if reachedDepth == expectedDepth {
			return &joindepth.ProbeVerdict{
				ReachedDepth: reachedDepth,
				Detail:       detail,
				Err:          nil,
			}
		}

		// Reached a different depth than expected (e.g., reached QUERY but expected JOINED).
		// This is exit code 2: connected but wrong depth. Do NOT set Err;
		// the ExitCodeFromVerdict function uses the absence of Err to distinguish exit 2.
		return &joindepth.ProbeVerdict{
			ReachedDepth: reachedDepth,
			Detail:       detail + fmt.Sprintf("; expected %s", expectedDepth),
			Err:          nil,
		}
	}

	// A2S failed. Since Rust's game protocol is undocumented and requires Steam auth,
	// we cannot measure JOINED or PARTIAL depth via Path B.
	if a2sErr != nil {
		// Classify the error: is it a transport failure or something else?
		// Use UNKNOWN depth to signal that we never reached any query depth.
		if joindepth.IsTransportError(a2sErr) {
			if expectFail {
				// Under -expect-fail, transport failure is success (probe correctly failed).
				return &joindepth.ProbeVerdict{
					ReachedDepth: joindepth.JoinDepth(-1), // -1 renders as UNKNOWN
					Detail:       fmt.Sprintf("Transport failure: %v; expected to fail, and did", a2sErr),
					Err:          nil,
				}
			}

			// Normal case: transport failure is exit code 3, report UNKNOWN depth.
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.JoinDepth(-1), // -1 renders as UNKNOWN
				Detail:       fmt.Sprintf("Transport failure: %v", a2sErr),
				Err:          fmt.Errorf("a2s query failed: %w", a2sErr),
			}
		}

		// Fallback: other errors (internal or protocol-level).
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // -1 renders as UNKNOWN
			Detail:       fmt.Sprintf("A2S query failed: %v", a2sErr),
			Err:          fmt.Errorf("a2s query error: %w", a2sErr),
		}
	}

	// This should not be reached, but just in case...
	return &joindepth.ProbeVerdict{
		ReachedDepth: joindepth.JoinDepth(-1), // -1 renders as UNKNOWN
		Detail:       "Rust probe: no query method succeeded",
		Err:          fmt.Errorf("rust probe: no query method succeeded"),
	}
}
