// Package main implements a hand-rolled join-depth probe for Factorio, used
// by the e2e suite to measure how far a real client can get against a
// running server (TCP connectivity on the RCON port for depth, plus a
// purely diagnostic best-effort UDP probe at the game port).
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

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
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
			ReachedDepth: joindepth.JoinDepth(-1), // renders as UNKNOWN
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
			ReachedDepth: joindepth.JoinDepth(-1), // renders as UNKNOWN
			Detail:       fmt.Sprintf("Bad flag: -expect-depth must be one of QUERY, PARTIAL, JOINED (got %q)", *expectDepthStr),
			Err:          err,
		}
		line, _ := verdict.Encode()
		fmt.Println(line)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	depth, evidence, err := probeFactorio(ctx, *addr)

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

// probeFactorio attempts to reach the deepest join depth on a Factorio server.
// Factorio's multiplayer protocol is not publicly documented (only conceptual
// descriptions in FFF #149/#147 exist). The probe checks TCP port 27015 (RCON)
// to verify the server is listening and responsive.
//
// Depth measurement: A TCP accept on the RCON port establishes QUERY depth.
// The RCON port proves the container is serving; the UDP game port (34197)
// has no query surface on a real Factorio server (unrecognized packets are
// silently dropped). No deeper probe is possible without the documented join
// protocol format and credentials. The test does not attempt JOINED or even
// PARTIAL because Factorio's join is mediated by the in-game multiplayer UI,
// which CI cannot operate.
//
// Returns: (depth, evidence, error)
// - depth: the join depth reached (QUERY, PARTIAL, or JOINED)
// - evidence: a human-readable description of the concrete server-originated artifact proving the depth
// - error: a transport-level or protocol-level error, or nil if successful
func probeFactorio(ctx context.Context, addr string) (joindepth.JoinDepth, string, error) {
	// Parse the input address to extract the host/port and construct the RCON address.
	// The input addr is expected to be "host:34197" (the game port); replace with port 27015 (RCON).
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return joindepth.JoinDepth(-1), "", fmt.Errorf("factorio probe: malformed address %q; expected host:port: %w", addr, err)
	}
	rconAddr := net.JoinHostPort(host, "27015")

	// Attempt to establish a TCP connection to the RCON port (27015).
	// A successful TCP accept proves the server is listening and responsive.
	if err := retryWithContext(ctx, "factorio-rcon-probe", 15*time.Second, func(actx context.Context) error {
		d := net.Dialer{}
		conn, err := d.DialContext(actx, "tcp", rconAddr)
		if err != nil {
			return fmt.Errorf("dial tcp %s: %w", rconAddr, err)
		}
		_ = conn.Close()
		return nil
	}); err != nil {
		// TCP connect failed; RCON port is not accepting connections.
		// Distinguish between transport failures and other errors.
		if joindepth.IsTransportError(err) {
			return joindepth.JoinDepth(-1), "", fmt.Errorf("dial timeout after %v against %s; connection never established: %w", 15*time.Second, rconAddr, err)
		}
		return joindepth.JoinDepth(-1), "", fmt.Errorf("factorio rcon probe failed: %w", err)
	}

	log.Printf("factorio-rcon-probe: tcp connection accepted on port 27015")

	// Optionally attempt a diagnostic UDP probe on the game port to log any response.
	// This does not affect the depth result; it is purely informational.
	go func() {
		diagCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if response, err := sendConnectionRequestUDP(diagCtx, addr); err == nil {
			responseHex := hex.EncodeToString(response)
			if len(responseHex) > 512 {
				responseHex = responseHex[:512] + "... (truncated)"
			}
			log.Printf("factorio-udp-diagnostic: udp response_bytes=%d, response_hex=%s", len(response), responseHex)
		} else {
			log.Printf("factorio-udp-diagnostic: no udp response (expected; Factorio silently drops unrecognized packets): %v", err)
		}
	}()

	// Depth is QUERY: the RCON port is accepting connections, proving the
	// server is listening. We cannot probe deeper without the documented protocol
	// and credentials. The factorio module's template sets require_user_verification: false
	// and public: false to disable external auth, but join still requires the
	// in-game multiplayer UI, which CI cannot operate.
	return joindepth.QUERY, "TCP connection accepted on RCON port 27015", nil
}

// retryWithContext calls fn until it succeeds, ctx expires, or fn reports a fatal error.
// Every failed attempt is logged.
func retryWithContext(ctx context.Context, what string, attemptTimeout time.Duration, fn func(context.Context) error) error {
	const retryInterval = 3 * time.Second
	var lastErr error

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

// sendConnectionRequestUDP sends a diagnostic best-effort Factorio connection
// request packet on UDP 34197 and returns the server's response, or an error if
// the dial or send fails. The packet layout below is unverified best-effort,
// inferred from conceptual descriptions of Factorio's handshake (FFF #149/#147)
// but not from official documentation.
//
// This is a purely diagnostic function used to log the server's behavior on the
// game port; it does not affect the probe depth result. Real Factorio servers
// silently drop unrecognized UDP packets, so no response is expected or required.
//
// A real Factorio server will either:
// - Respond with a structured message (bytes we log for evidence).
// - Time out (UDP is connectionless; no guarantee of response; expected behavior).
// - Respond with garbage (port open but not game protocol).
//
// This function returns an error only if the UDP dial or send fails; a timeout
// or no response is treated as error and logged.
func sendConnectionRequestUDP(ctx context.Context, addr string) ([]byte, error) {
	// Dial UDP to the game port.
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	// Set a short read deadline for the response. UDP is connectionless, so we
	// cannot distinguish "server is sending" from "server rejected the packet
	// silently". A 2-second deadline is long enough for LAN / in-cluster
	// latency but short enough to not block the retry loop.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}

	// Send a best-effort connection request. The packet layout is unverified:
	// Factorio's multiplayer handshake is not publicly documented. This payload
	// is a minimal attempt based on conceptual descriptions; it may or may not
	// match the real wire format. The server's response (or lack thereof) is the
	// only ground truth.
	//
	// Layout (unverified, inferred from FFF #149/#147 concepts):
	// - byte[1]: 0x01 (connection request type, guessed)
	// - byte[2-9]: random 8-byte session ID (arbitrary)
	// - rest: optional metadata (version string, player name, etc.)
	requestPayload := []byte{
		0x01,                                           // unverified: connection request type
		0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03, // 8-byte random session ID
		// No further data; server either responds or times out.
	}

	if _, err := conn.Write(requestPayload); err != nil {
		return nil, fmt.Errorf("send connection request: %w", err)
	}

	// Read the server's response.
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		// Timeout or read error. Both are acceptable for this probe:
		// a timeout means the server received the packet but did not respond
		// (behavior to log as "no response", not an error).
		// a genuine read error (e.g., connection refused, which is impossible
		// for UDP but could happen if the kernel replies) is also logged.
		// Return empty response + error so the retry loop catches and retries.
		return nil, fmt.Errorf("read response: %w", err)
	}

	return buf[:n], nil
}
