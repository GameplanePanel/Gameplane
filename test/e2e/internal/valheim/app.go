// Package main implements a hand-rolled join-depth probe for Valheim, used
// by the e2e suite to measure how far a real client can get against a
// running server: since Valheim's UDP game protocol is not publicly
// documented, this probe measures QUERY depth via the lloesche/
// valheim-server image's documented HTTP status.json endpoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// statusResponse mirrors the HTTP status.json response from the
// lloesche/valheim-server image, per
// https://github.com/lloesche/valheim-server-docker#status-http-server
type statusResponse struct {
	Server struct {
		Players      int    `json:"players"`
		MaxPlayers   int    `json:"max_players"`
		WorldSaveAge int    `json:"world_save_age"`
		UptimeSecs   int    `json:"uptime_secs"`
		Version      string `json:"version"`
		NetworkInfo  struct {
			ConnectedPlayers int `json:"connected_players"`
		} `json:"network_info"`
	} `json:"server"`
}

func main() {
	// Standard flags per the probe CLI contract.
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute,
		"overall deadline; the probe retries until the server is playable or this elapses")
	expectDepth := flag.String("expect-depth", "QUERY",
		"expected join depth: QUERY | PARTIAL | JOINED")
	expectFail := flag.Bool("expect-fail", false,
		"if set, probe must NOT reach -expect-depth; exits 0 on correct failure")

	flag.Parse()

	// Set up logging.
	log.SetFlags(log.Ltime)

	// Validate required flags.
	if *addr == "" {
		verdict := joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       "Bad flag: -addr is required",
			Err:          errors.New("-addr is required"),
		}
		emitVerdictAndExit(&verdict, joindepth.QUERY, *expectFail, 1)
	}

	// Parse expected depth.
	parsedExpect, err := joindepth.Parse(*expectDepth)
	if err != nil {
		verdict := joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       fmt.Sprintf("Bad flag: %v", err),
			Err:          err,
		}
		emitVerdictAndExit(&verdict, joindepth.QUERY, *expectFail, 1)
	}

	// Create the probe context with deadline.
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	// Run the probe.
	reached, evidence, transportErr := probeValheim(ctx, *addr)

	// Build the verdict.
	verdict := joindepth.ProbeVerdict{
		ReachedDepth: reached,
		Detail:       evidence,
		Err:          transportErr,
	}

	// Determine exit code based on the contract.
	exitCode := joindepth.ExitCodeFromVerdict(&verdict, parsedExpect, *expectFail)
	emitVerdictAndExit(&verdict, parsedExpect, *expectFail, exitCode)
}

// emitVerdictAndExit emits the machine-readable VERDICT line and exits.
func emitVerdictAndExit(v *joindepth.ProbeVerdict, _ joindepth.JoinDepth, _ bool, exitCode int) {
	// Encode the verdict line.
	line, err := v.Encode()
	if err != nil {
		// If encoding fails, emit a fallback and exit with internal error.
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tVERDICT encoding failed: %v\n", err)
		os.Exit(1)
	}

	// Emit the verdict line to stdout.
	fmt.Println(line)

	// Exit with the computed code.
	os.Exit(exitCode)
}

// probeValheim fetches the documented status.json HTTP endpoint from a
// Valheim server and returns QUERY depth on success.
//
// Valheim's game protocol (UDP 2456+) is not publicly documented, so a
// full join (JOINED or PARTIAL depth) is not possible in CI. However,
// the lloesche/valheim-server image exposes a documented HTTP status
// endpoint at /status.json (port 80 by default). A successful fetch and
// parse proves the server is alive and responding to the health protocol
// that Kubernetes uses for readiness probes.
//
// Returns (depth, evidence, error). Transport-level errors are wrapped with %w.
func probeValheim(ctx context.Context, addr string) (joindepth.JoinDepth, string, error) {
	// Retry fetching status.json with per-attempt timeout.
	const probeAttempt = 15 * time.Second
	const retryInterval = 3 * time.Second

	var status *statusResponse
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			// Deadline reached; no response from server.
			return joindepth.JoinDepth(-1), "", fmt.Errorf("status.json never succeeded before the deadline: %w", lastErr)
		}

		actx, cancel := context.WithTimeout(ctx, probeAttempt)
		status, lastErr = fetchStatus(actx, addr)
		cancel()

		if lastErr == nil {
			break
		}

		log.Printf("status.json not ready yet: %v", lastErr)

		select {
		case <-ctx.Done():
			// Deadline reached while sleeping.
			return joindepth.JoinDepth(-1), "", fmt.Errorf("status.json never succeeded before the deadline: %w", lastErr)
		case <-time.After(retryInterval):
			// Continue to next attempt.
		}
	}

	// Successfully fetched status.json; report QUERY depth with server version as evidence.
	if status != nil && status.Server.Version != "" {
		evidence := fmt.Sprintf("status.json response: version %s, %d/%d players online",
			status.Server.Version, status.Server.Players, status.Server.MaxPlayers)
		log.Printf("valheim status: players=%d/%d uptime=%ds version=%s",
			status.Server.Players, status.Server.MaxPlayers,
			status.Server.UptimeSecs, status.Server.Version)
		return joindepth.QUERY, evidence, nil
	}

	// Fallback evidence if version is empty.
	evidence := fmt.Sprintf("status.json response: %d/%d players online",
		status.Server.Players, status.Server.MaxPlayers)
	return joindepth.QUERY, evidence, nil
}

// fetchStatus fetches and parses status.json from the Valheim HTTP status
// endpoint (port 80 by default). The addr parameter is host:port of the
// game server's status port.
//
// Errors are wrapped with %w to preserve the cause for transport error detection.
func fetchStatus(ctx context.Context, addr string) (*statusResponse, error) {
	// Construct the HTTP GET request to /status.json.
	// addr is "host:port" where port is STATUS_HTTP_PORT (default 80).
	url := fmt.Sprintf("http://%s/status.json", addr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Send the request with the context's deadline.
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch status.json: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check HTTP status code.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status.json returned %d", resp.StatusCode)
	}

	// Read and parse the JSON response.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var status statusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("parse status.json: %w", err)
	}

	return &status, nil
}
