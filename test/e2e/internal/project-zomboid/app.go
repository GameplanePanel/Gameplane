// Package main implements a hand-rolled join-depth probe for Project
// Zomboid, used by the e2e suite to measure how far a real client can get
// against a running server (A2S query on the game port for depth, plus a
// purely diagnostic raw UDP poke).
package main

import (
	"context"
	"encoding/binary"
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
	addr := flag.String("addr", "", "game server host:port")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline")
	expectDepth := flag.String("expect-depth", "JOINED", "expected join depth: QUERY, PARTIAL, or JOINED")
	expectFail := flag.Bool("expect-fail", false, "probe must NOT reach -expect-depth")
	flag.Parse()

	// Validate required flags.
	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // encodes as UNKNOWN
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("bad flag: -addr is required"),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Parse expected depth.
	expectedDepth, err := joindepth.Parse(*expectDepth)
	if err != nil {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // encodes as UNKNOWN
			Detail:       fmt.Sprintf("Bad flag: %v", err),
			Err:          err,
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Configure logging.
	log.SetFlags(log.Ltime)

	// Create context with deadline.
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	// Run the probe and construct a ProbeVerdict.
	verdict := probeProjectZomboidWithVerdict(ctx, *addr)

	// Emit the VERDICT line.
	verdictLine, _ := verdict.Encode()
	fmt.Println(verdictLine)

	// Exit with the appropriate code.
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	os.Exit(exitCode)
}

// probeProjectZomboidWithVerdict attempts to reach the deepest join depth on a Project
// Zomboid server and returns a ProbeVerdict.
//
// Project Zomboid is a Steamworks GameServer title: like the other four games
// in this bucket, its Steamworks GameServer integration answers Valve's A2S
// (Source Engine Query) protocol directly on the game port. node-gamedig's
// "projectzomboid" game definition confirms this: `port: 16261, protocol:
// 'valve'` — no separate query port and no per-game protocol override; A2S
// rides the same UDP port as the game.
// Reference: https://github.com/gamedig/node-gamedig (lib/games.js,
// "projectzomboid" entry).
//
// An earlier version of this probe sent a hand-rolled 4-byte zero packet to
// the game port and required any reply. That is not falsifiable: a server
// that only answers well-formed requests would silently drop an unrecognized
// packet — the same failure mode proven empirically against Factorio's query
// port (it drops unrecognized UDP datagrams with no reply at all, so a probe
// requiring a response to a made-up packet fails permanently against a
// perfectly healthy server). A2S_INFO is the actual documented request this
// port answers, so it is used here instead.
//
// Depth measurement: Returns QUERY if the game port answers A2S_INFO.
// Distinguishes between transport failures (exit 3) and protocol errors (exit 2).
func probeProjectZomboidWithVerdict(ctx context.Context, addr string) *joindepth.ProbeVerdict {
	var info *a2s.Info
	var lastErr error

	// Retry the A2S query until the deadline passes.
	for ctx.Err() == nil {
		// Attempt A2S query with a per-attempt timeout.
		actx, cancel := context.WithTimeout(ctx, 15*time.Second)
		info, lastErr = a2s.QueryInfo(actx, addr)
		cancel()

		if lastErr == nil {
			// Success! We reached QUERY depth.
			log.Printf("a2s query succeeded: server=%q map=%q players=%d/%d",
				info.Name, info.Map, info.Players, info.MaxPlayers)

			detail := fmt.Sprintf("A2S_INFO response received: server=%q players=%d/%d",
				info.Name, info.Players, info.MaxPlayers)
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.QUERY,
				Detail:       detail,
				Err:          nil,
			}
		}

		// Error occurred. Log and classify it.
		log.Printf("a2s query attempt failed: %v", lastErr)

		// Check if this is a transport error or protocol error.
		if joindepth.IsTransportError(lastErr) {
			// Retry for transport errors (server not ready, etc).
			select {
			case <-ctx.Done():
				// Deadline reached before a successful query.
				diagCtx, diagCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				logRawDiagnostic(diagCtx, addr)
				diagCancel()
				return &joindepth.ProbeVerdict{
					ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN
					Detail:       fmt.Sprintf("Dial timeout after 4m against %s; connection never established", addr),
					Err:          fmt.Errorf("a2s query on game port %s never succeeded: %w", addr, lastErr),
				}
			case <-time.After(3 * time.Second):
				// Retry after 3 seconds.
			}
		} else {
			// Protocol error or other fatal error - don't retry.
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN
				Detail:       fmt.Sprintf("A2S protocol error: %v", lastErr),
				Err:          fmt.Errorf("a2s query failed with protocol error: %w", lastErr),
			}
		}
	}

	// Deadline expired without success.
	diagCtx, diagCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	logRawDiagnostic(diagCtx, addr)
	diagCancel()
	return &joindepth.ProbeVerdict{
		ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN
		Detail:       fmt.Sprintf("Dial timeout after 4m against %s; connection never established", addr),
		Err:          fmt.Errorf("a2s query on game port %s never succeeded: %w", addr, lastErr),
	}
}

// logRawDiagnostic sends a minimal, non-protocol UDP probe (the same 4-byte
// zero packet this probe used to require a reply to) and logs whatever comes
// back, or explicitly logs that nothing did. It is purely diagnostic: its
// outcome is never used to determine probe success or failure, and it runs
// with its own short, independent timeout rather than the (by this point,
// likely expired) probe deadline.
func logRawDiagnostic(ctx context.Context, addr string) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "udp4", addr)
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

	req := make([]byte, 4)
	binary.LittleEndian.PutUint32(req, 0x00000000)
	if _, err := conn.Write(req); err != nil {
		log.Printf("raw-diagnostic: write to %s failed: %v", addr, err)
		return
	}

	buf := make([]byte, 1024)
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
