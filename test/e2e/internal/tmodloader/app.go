// Package main implements a join-depth probe for tModLoader dedicated servers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/terraria/terrariaproto"
)

// main is the probe entrypoint.
func main() {
	log.SetFlags(log.Ltime)

	addr := flag.String("addr", "", "game server host:port (in-cluster Service DNS)")
	deadline := flag.Duration("deadline", 4*time.Minute, "overall deadline for probe")
	expectDepthStr := flag.String("expect-depth", "QUERY", "expected join depth: QUERY | PARTIAL | JOINED")
	expectFail := flag.Bool("expect-fail", false, "if set, probe must NOT reach -expect-depth")
	flag.Parse()

	if *addr == "" {
		verdict := &joindepth.ProbeVerdict{
			ReachedDepth: joindepth.JOINED,
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

	depth, evidence, err := probeTModLoader(ctx, *addr)

	verdict := &joindepth.ProbeVerdict{
		ReachedDepth: depth,
		Detail:       evidence,
		Err:          err,
	}

	line, encodeErr := verdict.Encode()
	if encodeErr != nil {
		fmt.Printf("VERDICT\tFAIL_INTERNAL_ERROR\tUNKNOWN\tFailed to encode verdict: %v\n", encodeErr)
		os.Exit(1)
	}
	fmt.Println(line)

	exitCode := joindepth.ExitCodeFromVerdict(verdict, expectedDepth, *expectFail)
	os.Exit(exitCode)
}

// probeTModLoader executes join-depth probing against the target game server.
func probeTModLoader(ctx context.Context, addr string) (joindepth.JoinDepth, string, error) {
	var evidence string
	err := retryWithContext(ctx, "tmodloader-handshake", 15*time.Second, func(actx context.Context) error {
		conn, res, err := terrariaproto.Connect(actx, addr)
		if errors.Is(err, terrariaproto.ErrPasswordRequired) {
			evidence = "Password required prompt received from tModLoader server"
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		evidence = fmt.Sprintf("tModLoader handshake accepted on slot %d (version %s)", res.Slot, res.Version)
		return nil
	})

	if err != nil {
		return joindepth.JoinDepth(-1), "", fmt.Errorf("tmodloader probe failed: %w", err)
	}
	return joindepth.QUERY, evidence, nil
}

// retryWithContext executes an operation repeatedly until it succeeds or context is canceled.
func retryWithContext(ctx context.Context, opName string, perAttemptTimeout time.Duration, fn func(context.Context) error) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		actx, acancel := context.WithTimeout(ctx, perAttemptTimeout)
		err := fn(actx)
		acancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("%s timed out: last error: %w", opName, lastErr)
		case <-ticker.C:
		}
	}
}
