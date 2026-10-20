// Package main implements a join-depth probe for ARK: Survival Evolved dedicated servers.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	a2s "github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/a2sproto"
	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
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

	depth, evidence, err := probeArkEvolved(ctx, *addr)

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

// probeArkEvolved executes join-depth probing against the target game server.
func probeArkEvolved(ctx context.Context, addr string) (joindepth.JoinDepth, string, error) {
	var info *a2s.Info
	err := retryWithContext(ctx, "ark-evolved-a2s-query", 15*time.Second, func(actx context.Context) error {
		var err error
		info, err = a2s.QueryInfo(actx, addr)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return joindepth.JoinDepth(-1), "", fmt.Errorf("ark survival evolved a2s query failed: %w", err)
	}

	evidence := fmt.Sprintf("A2S query response received: server=%q map=%q players=%d/%d",
		info.Name, info.Map, info.Players, info.MaxPlayers)
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
