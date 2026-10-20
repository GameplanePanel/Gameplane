// Package main implements a hand-rolled join-depth probe for Palworld, used
// by the e2e suite to measure how far a real client can get against a
// running server (A2S query on the query port for depth).
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

	// Register standard flags.
	addr := flag.String("addr", "", "game server host:port")
	deadline := flag.Duration("deadline", 4*time.Minute, "total elapsed time allowed before giving up")
	expectDepthStr := flag.String("expect-depth", "JOINED", "expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth; exits 0 on correct failure")
	flag.Parse()

	if *addr == "" {
		emitVerdictAndExit(joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("bad flag: -addr is required"),
		}, joindepth.QUERY, false)
	}

	expectDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		emitVerdictAndExit(joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       fmt.Sprintf("Bad flag: %v", err),
			Err:          fmt.Errorf("bad flag: %w", err),
		}, joindepth.QUERY, false)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	// Run the probe.
	depth, detailErr := probePalworld(ctx, *addr)

	// Build the verdict.
	verdict := joindepth.ProbeVerdict{
		ReachedDepth: depth,
		Detail:       detailErr.Detail,
		Err:          detailErr.Err,
	}

	emitVerdictAndExit(verdict, expectDepth, *expectFail)
}

// errorDetail holds both the detail string and any error for verdict building.
type errorDetail struct {
	Detail string
	Err    error
}

// probePalworld attempts to reach the deepest join depth on a Palworld server.
// It queries A2S info on port 27015 to establish QUERY depth.
// Depth measurement: A2S success on 27015 proves QUERY. The REST API on 8212
// and the game protocol on 8211 are not measured for join depth.
func probePalworld(ctx context.Context, addr string) (joindepth.JoinDepth, errorDetail) {
	// First: Query A2S on the query port to verify the server is alive.
	// Palworld declares QUERY_PORT=27015 in the template; A2S is the standard
	// Steam query protocol used for server discovery.
	// A2S failure is fatal — server is not responding to queries at all.
	var info *a2s.Info
	if err := probe.Retry(ctx, "a2s-info", 15*time.Second, func(actx context.Context) error {
		var err error
		info, err = a2s.QueryInfo(actx, addr)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		// Classify the error: is it a transport failure or internal error?
		if joindepth.IsTransportError(err) {
			// Transport failure: no depth measured. Use JoinDepth(-1) to stringify as UNKNOWN.
			return joindepth.JoinDepth(-1), errorDetail{
				Detail: fmt.Sprintf("A2S query failed; no response from %s", addr),
				Err:    fmt.Errorf("a2s query failed (transport): %w", err),
			}
		}
		// Internal error: also report UNKNOWN depth since A2S didn't respond.
		return joindepth.JoinDepth(-1), errorDetail{
			Detail: "A2S query error",
			Err:    fmt.Errorf("a2s query failed: %w", err),
		}
	}

	// Log server metadata from A2S response.
	detail := fmt.Sprintf("A2S_INFO response received: server=%q map=%q players=%d/%d",
		info.Name, info.Map, info.Players, info.MaxPlayers)
	log.Print(detail)

	// Depth is QUERY because A2S succeeded.
	// The REST API on 8212 and game protocol on 8211 are not measured for depth:
	// - REST API requires HTTP Basic auth (admin password) we cannot mint.
	// - Game protocol (Unreal Engine) is undocumented and not publicly joinable.
	// A2S on 27015 is the only honest depth measurement.
	return joindepth.QUERY, errorDetail{
		Detail: detail,
		Err:    nil,
	}
}

// emitVerdictAndExit emits the VERDICT line to stdout and exits with the appropriate code.
func emitVerdictAndExit(verdict joindepth.ProbeVerdict, expectedDepth joindepth.JoinDepth, expectFail bool) {
	// Emit the VERDICT line to stdout (no log prefix).
	line, err := verdict.Encode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe: failed to encode verdict: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(line)

	// Determine the exit code based on the verdict and expectations.
	exitCode := joindepth.ExitCodeFromVerdict(&verdict, expectedDepth, expectFail)
	os.Exit(exitCode)
}
