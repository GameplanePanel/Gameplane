// Package main implements a hand-rolled join-depth probe for ARK: Survival
// Ascended, used by the e2e suite to measure how far a real client can get
// against a running server (RCON port TCP connectivity only).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

func main() {
	// Parse standard flags
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute,
		"overall deadline; the probe retries until the server is reachable or this elapses")
	expectDepth := flag.String("expect-depth", "QUERY",
		"expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false,
		"if set, probe must NOT reach -expect-depth; exits 0 on correct failure, non-zero if it somehow succeeded")
	flag.Parse()

	// Validate inputs
	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("bad flag: -addr is required"),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	expectedDepth, err := joindepth.Parse(*expectDepth)
	if err != nil {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1),
			Detail:       fmt.Sprintf("Bad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED; got %q", *expectDepth),
			Err:          fmt.Errorf("bad flag: %w", err),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	log.SetFlags(log.Ltime)

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	verdict := probeARK(ctx, *addr)

	// Emit VERDICT line
	verdictLine, err := verdict.Encode()
	if err != nil {
		fmt.Printf("VERDICT\t%s\t%s\t%s\n",
			joindepth.FailInternalError,
			"UNKNOWN",
			fmt.Sprintf("verdict encoding error: %v", err))
		os.Exit(1)
	}
	fmt.Println(verdictLine)

	// Determine exit code
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	os.Exit(exitCode)
}

// probeARK attempts to measure join depth on an ARK: Survival Ascended server.
//
// ARK: Survival Ascended (UE5) declares no query port. The join protocol
// requires EOS/Steam identity that CI cannot mint, making a full join
// impossible. The honest depth is RCON port connectivity only.
//
// This probe:
//  1. Attempts TCP connect to port 27020 (RCON/Source protocol port) to verify a listener exists.
//  2. A successful TCP handshake proves the server is listening on that port.
//  3. Returns QUERY depth if the connection succeeded.
//  4. Returns an error with transport-level details if the server is not reachable.
//
// Why TCP on 27020 instead of UDP on 7777:
//   - UDP dial does not handshake; it just creates a local socket. It succeeds even
//     when nothing is listening.
//   - TCP dial requires a real handshake with the remote listener, which is
//     falsifiable: a dead server or closed port will reject the connection.
//   - ARK declares rcon.protocol: source on TCP 27020, and this port exists on all
//     ARK servers (though RCON is not enabled by default without admin configuration).
//
// Note: This proves the TCP port is open, which is consistent with QUERY depth.
// It does not prove the game is serving players (that would require join or query depth).
func probeARK(ctx context.Context, addr string) *joindepth.ProbeVerdict {
	// Parse addr to replace UDP with TCP on 27020.
	// The addr passed in is in format "host:7777" (game port).
	// We need to connect to "host:27020" (RCON port) instead.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// If addr doesn't have a port, assume it's just the host.
		host = addr
	}
	rconAddr := net.JoinHostPort(host, "27020")

	// Retry connecting to the RCON port
	var lastErr error
	retryInterval := 3 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			// Deadline expired: transport failure with UNKNOWN depth
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.JoinDepth(-1), // Renders as UNKNOWN
				Detail:       fmt.Sprintf("Dial timeout after deadline against %s; connection never established", rconAddr),
				Err:          fmt.Errorf("connection never established: %w", lastErr),
			}
		}

		actx, cancel := context.WithTimeout(ctx, 15*time.Second)
		d := net.Dialer{}
		conn, err := d.DialContext(actx, "tcp", rconAddr)
		cancel()

		if err == nil {
			_ = conn.Close()
			log.Printf("connectivity-probe: successfully connected to RCON port (TCP 27020)")
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.QUERY,
				Detail:       "TCP connection to RCON port (27020) accepted; server is listening",
				Err:          nil,
			}
		}

		lastErr = err
		log.Printf("tcp-connect-rcon not ready yet: %v", err)

		select {
		case <-ctx.Done():
			// Deadline expired during retry loop
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.JoinDepth(-1), // Renders as UNKNOWN
				Detail:       fmt.Sprintf("Dial timeout after deadline against %s; connection never established", rconAddr),
				Err:          fmt.Errorf("connection never established: %w", lastErr),
			}
		case <-time.After(retryInterval):
		}
	}
}
