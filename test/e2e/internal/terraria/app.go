// Package main implements a hand-rolled join-depth probe for Terraria, used
// by the e2e suite to measure how far a real client can get against a
// running server: handshake, then a WorldData request to prove a full join.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/terraria/terrariaproto"
)

func main() {
	// Parse standard flags.
	addr := flag.String("addr", "", "game server host:port")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline")
	expectDepthStr := flag.String("expect-depth", "JOINED", "expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth")
	flag.Parse()

	// Validate required flags.
	if *addr == "" {
		emitVerdictAndExit(&joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: no probe attempted
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("bad flag: -addr is required"),
		}, joindepth.JOINED, *expectFail, 1)
	}

	// Parse expected depth.
	expectedDepth, err := joindepth.Parse(*expectDepthStr)
	if err != nil {
		emitVerdictAndExit(&joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: no probe attempted
			Detail:       fmt.Sprintf("Bad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED; got %q", *expectDepthStr),
			Err:          fmt.Errorf("bad flag: -expect-depth=%q must be one of QUERY, PARTIAL, JOINED", *expectDepthStr),
		}, joindepth.JOINED, *expectFail, 1)
	}

	log.SetFlags(log.Ltime)

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	verdict := probeTerraria(ctx, *addr)

	emitVerdictAndExit(verdict, expectedDepth, *expectFail, 0)
}

// probeTerraria attempts to connect and measure join depth, returning a ProbeVerdict.
// It does not exit; the caller determines exit code based on expected depth and -expect-fail.
func probeTerraria(ctx context.Context, addr string) *joindepth.ProbeVerdict {
	// Retry the handshake at 15 seconds per attempt.
	var conn *terrariaproto.Conn

	if err := retryWithDeadline(ctx, "handshake", 15*time.Second, func(actx context.Context) error {
		var err error
		conn, _, err = terrariaproto.Connect(actx, addr)

		if errors.Is(err, terrariaproto.ErrPasswordRequired) {
			// Server accepted handshake but requires password.
			// This is PARTIAL: protocol works, but credential gate blocks further progress.
			// Wrap with a marker so retryWithDeadline knows this is fatal (don't retry).
			return &fatalError{
				depth:  joindepth.PARTIAL,
				detail: "Password required prompt received",
				err:    err,
			}
		}

		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		// Check if it's a fatal error wrapping a PARTIAL depth.
		var fe *fatalError
		if errors.As(err, &fe) {
			return &joindepth.ProbeVerdict{
				ReachedDepth: fe.depth,
				Detail:       fe.detail,
				Err:          fe.err,
			}
		}

		// Handshake failed (no successful connection or connection dropped before ContinueConnecting).
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: no successful handshake
			Detail:       describeTransportError(err),
			Err:          err,
		}
	}

	// After successful Connect, request world data with its own timeout.
	defer func() { _ = conn.Close() }()
	worldDataCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := conn.RequestWorldData(worldDataCtx); err != nil {
		// RequestWorldData failed. Since we got past Connect, the handshake succeeded,
		// so we reached at least PARTIAL. The failure is either a transport error
		// (connection dropped, timeout) or protocol error (server disconnected).
		// Either way, we measured PARTIAL depth (handshake succeeded).
		detail := fmt.Sprintf("Server disconnected before sending WorldData: %v", err)
		if joindepth.IsTransportError(err) {
			// Make the detail more specific for transport errors.
			detail = describeTransportError(err)
		}
		return &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.PARTIAL,
			Detail:       detail,
			Err:          err,
		}
	}

	// Successfully received world data.
	return &joindepth.ProbeVerdict{
		ReachedDepth: joindepth.JOINED,
		Detail:       "WorldData packet (type 7) received; server accepted joining client",
		Err:          nil,
	}
}

// fatalError wraps a depth measurement with a marker to prevent retry.
type fatalError struct {
	depth  joindepth.JoinDepth
	detail string
	err    error
}

func (e *fatalError) Error() string {
	return e.err.Error()
}

// retryWithDeadline retries fn until success, ctx expires, or fn returns a fatal error.
func retryWithDeadline(ctx context.Context, what string, attemptTimeout time.Duration, fn func(context.Context) error) error {
	const retryInterval = 3 * time.Second
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			// Deadline exhausted. Return the last error if we have one, else the context error.
			if lastErr != nil {
				return fmt.Errorf("%s never succeeded before deadline: %w", what, lastErr)
			}
			return err
		}

		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := fn(actx)
		cancel()

		if err == nil {
			return nil
		}

		// Check for fatal errors (don't retry).
		var fe *fatalError
		if errors.As(err, &fe) {
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

// describeTransportError produces a human-readable description of a transport error.
func describeTransportError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	if strings.Contains(errStr, "i/o timeout") || strings.Contains(errStr, "Dial timeout") {
		return fmt.Sprintf("Dial timeout; connection never established: %v", err)
	}

	if strings.Contains(errStr, "connection refused") {
		return fmt.Sprintf("Connection refused: %v", err)
	}

	if strings.Contains(errStr, "connection reset") {
		return fmt.Sprintf("Connection reset by peer: %v", err)
	}

	if strings.Contains(errStr, "no such host") {
		return fmt.Sprintf("DNS failure: %v", err)
	}

	if strings.Contains(errStr, "network is unreachable") {
		return fmt.Sprintf("Network unreachable: %v", err)
	}

	return fmt.Sprintf("Transport error: %v", err)
}

// emitVerdictAndExit writes the VERDICT line to stdout and exits with the appropriate code.
func emitVerdictAndExit(verdict *joindepth.ProbeVerdict, expectedDepth joindepth.JoinDepth, expectFail bool, _ int) {
	// Emit the VERDICT line to stdout.
	verdictLine, err := verdict.Encode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe: failed to encode verdict: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(verdictLine)

	// Log details to stderr.
	if verdict.Err != nil {
		log.Printf("probe error: %v", verdict.Err)
	}
	if expectedDepth != verdict.ReachedDepth {
		log.Printf("probe: reached %s, expected %s", verdict.ReachedDepth.String(), expectedDepth.String())
	}

	// Determine exit code based on verdict and expectations.
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, expectFail)
	os.Exit(exitCode)
}
