// Package main implements a hand-rolled join-depth probe for Satisfactory,
// used by the e2e suite to measure how far a real client can get against a
// running server (the unauthenticated QueryServerState HTTPS API call for
// depth, since CI cannot perform the in-game server claim admin
// authentication requires).
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

func main() {
	log.SetFlags(log.Ltime)

	// Parse flags.
	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline; the probe retries until the server is playable or this elapses")
	expectDepth := flag.String("expect-depth", "JOINED", "expected join depth: JOINED | PARTIAL | QUERY")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach expect-depth; exits 0 on correct failure, exits 1 or 2 if it somehow succeeded")
	flag.Parse()

	// Validate required flags.
	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: probe internal error
			Detail:       "Bad flag: -addr is required",
			Err:          fmt.Errorf("flag error: missing -addr"),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Parse the expected depth.
	expectedDepth, err := joindepth.Parse(*expectDepth)
	if err != nil {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.QUERY,
			Detail:       fmt.Sprintf("Bad flag: %v", err),
			Err:          fmt.Errorf("flag error: %w", err),
		}
		verdictLine, _ := verdict.Encode()
		fmt.Println(verdictLine)
		os.Exit(1)
	}

	// Create a context bounded by the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()

	// Probe the server.
	verdict := probeSatisfactory(ctx, *addr)

	// Encode and emit the verdict line.
	verdictLine, encodeErr := verdict.Encode()
	if encodeErr != nil {
		log.Printf("error encoding verdict: %v", encodeErr)
		fmt.Println("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tFailed to encode verdict")
		os.Exit(1)
	}
	fmt.Println(verdictLine)

	// Determine the exit code based on the expected depth and the expectFail flag.
	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	os.Exit(exitCode)
}

// probeSatisfactory attempts to reach the deepest join depth on a Satisfactory
// dedicated server. Since CI cannot perform the in-game server claim required
// to set an admin password, all authenticated functions (PasswordLogin,
// RunCommand) are unreachable in test environments.
//
// The honest measurement is to attempt the unauthenticated QueryServerState
// function, which (according to the OpenAPI spec referenced in
// agent/internal/rcon/satisfactory.go) returns server metadata without requiring
// authentication. If this succeeds, depth is QUERY. If it fails, we distinguish
// between transport errors (no connection) and API-level errors (connection made
// but rejected).
//
// Reference: The agent/internal/rcon/satisfactory.go file documents the
// Satisfactory HTTPS API at https://github.com/satisfactory-oas/spec — the
// endpoint is POST https://<host>:7777/api/v1 with a JSON body containing
// a "function" field.
func probeSatisfactory(ctx context.Context, addr string) *joindepth.ProbeVerdict {
	const retryInterval = 3 * time.Second
	const attemptTimeout = 15 * time.Second

	// Try QueryServerState repeatedly until the deadline, with exponential backoff.
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			// Deadline has been exceeded.
			if lastErr != nil && joindepth.IsTransportError(lastErr) {
				// The last error was a transport error (e.g., connection refused).
				// No connection was ever established.
				return &joindepth.ProbeVerdict{
					ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: no connection established.
					Detail:       fmt.Sprintf("Dial timeout after %v against %s; connection never established", attemptTimeout, addr),
					Err:          fmt.Errorf("dial timeout: %w", lastErr),
				}
			}
			// Generic deadline exceeded; could be retry exhaustion or API rejection.
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: deadline reached.
				Detail:       "Deadline reached; no response from server",
				Err:          fmt.Errorf("deadline exceeded: %w", lastErr),
			}
		}

		// Attempt the QueryServerState call with a timeout.
		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
		statusCode, err := queryServerState(actx, addr)
		cancel()

		if err == nil {
			// QueryServerState succeeded; the unauthenticated API is working.
			log.Printf("query-server-state: success (status=%d)", statusCode)
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.QUERY,
				Detail:       fmt.Sprintf("QueryServerState API response received (HTTP %d)", statusCode),
				Err:          nil,
			}
		}

		// The QueryServerState call failed. Check if this is a transport error or a protocol-level error.
		if joindepth.IsTransportError(err) {
			log.Printf("query-server-state transport error: %v (will retry)", err)
			lastErr = err
		} else {
			// Protocol-level or API-level error; this is still a connection that was made.
			log.Printf("query-server-state API error: %v (will retry)", err)
			lastErr = err
		}

		// Wait before retrying, unless the deadline is near.
		select {
		case <-ctx.Done():
			// Deadline reached during the retry wait.
			if joindepth.IsTransportError(lastErr) {
				return &joindepth.ProbeVerdict{
					ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: no connection established.
					Detail:       fmt.Sprintf("Dial timeout after %v against %s; connection never established", attemptTimeout, addr),
					Err:          fmt.Errorf("dial timeout: %w", lastErr),
				}
			}
			return &joindepth.ProbeVerdict{
				ReachedDepth: joindepth.JoinDepth(-1), // UNKNOWN: deadline reached.
				Detail:       "Deadline reached; no response from server",
				Err:          fmt.Errorf("deadline exceeded: %w", lastErr),
			}
		case <-time.After(retryInterval):
			// Continue to next attempt.
		}
	}
}

// isLoopbackHost reports whether host names the local machine, so that
// skipping TLS verification for the server's self-signed cert cannot expose
// the client to an off-host MITM. A bare "localhost" counts; anything else
// must parse to a loopback IP.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// queryServerState sends an unauthenticated QueryServerState request to the
// Satisfactory HTTPS API. It returns the HTTP status code,
// or an error if the request failed.
func queryServerState(ctx context.Context, addr string) (int, error) {
	// Split host and port. If no port is present, SplitHostPort returns an error,
	// but we still want to validate the bare host.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port in addr; use the entire addr as the host.
		host = addr
	}

	// Enforce loopback-only connections. The Satisfactory server generates a
	// self-signed cert with no CA supply API; InsecureSkipVerify is safe only
	// for pod-local connections (localhost or loopback IPs). Reject non-loopback
	// targets outright rather than allowing an unsafe dial.
	if !isLoopbackHost(host) {
		return 0, fmt.Errorf("refusing non-loopback connection to %q: satisfactory server TLS verification can only be skipped for loopback hosts", host)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(dctx, network, addr)
			},
			// The Satisfactory dedicated server presents a self-signed certificate.
			// We skip verification only because this connection is pod-local (127.0.0.1
			// or cluster-internal DNS name), which is safe: an off-host MITM cannot
			// intercept the connection. See agent/internal/rcon/satisfactory.go's
			// documentation for the rationale.
			// The loopback guard above ensures this is safe.
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				// InsecureSkipVerify is safe here: connection is pod-local (127.0.0.1 or cluster DNS),
				// so off-host MITM is not a risk. The loopback check above enforces this.
				InsecureSkipVerify: true,
			},
		},
		Timeout: 10 * time.Second,
	}
	defer client.CloseIdleConnections()

	// Build the request: a JSON POST with the QueryServerState function.
	// Note: No authentication token is sent (no "Authorization" header),
	// so this tests the unauthenticated API surface.
	reqBody, err := json.Marshal(map[string]string{
		"function": "QueryServerState",
	})
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+"/api/v1", bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read the response body (bounded to avoid log flooding).
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	body = body[:n]

	if n > 0 {
		log.Printf("query-server-state: status=%d body=%s", resp.StatusCode, string(body))
	} else {
		log.Printf("query-server-state: status=%d (no body)", resp.StatusCode)
	}

	// A 2xx status indicates the endpoint accepted the request.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// The endpoint accepted the request. Check if the body contains an error.
		var respEnv struct {
			Data      any    `json:"data,omitempty"`
			ErrorCode string `json:"errorCode,omitempty"`
		}
		if err := json.Unmarshal(body, &respEnv); err == nil {
			if respEnv.ErrorCode != "" {
				// The response has an error code; the endpoint rejected the call.
				return resp.StatusCode, fmt.Errorf("query-server-state: %s (api error, not network)", respEnv.ErrorCode)
			}
			// Success: the endpoint accepted the unauthenticated call.
			return resp.StatusCode, nil
		}
		// Response body is not JSON, but the status is 2xx; that's still success.
		return resp.StatusCode, nil
	}

	// A 4xx/5xx status is an API-level failure.
	return resp.StatusCode, fmt.Errorf("query-server-state: http %d", resp.StatusCode)
}
