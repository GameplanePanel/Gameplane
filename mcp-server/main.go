// Command mcp-server is a strictly read-only Model Context Protocol (MCP)
// server for Gameplane clusters: it lets an AI assistant read cluster state
// (7 of Gameplane's 9 CRDs — not Cluster or NetworkCapture — plus Pods,
// Events, pod logs) and propose fixes as plain text — SUGGESTED YAML or
// kubectl commands for a human operator to review
// and run. It never creates, updates, patches, deletes, or applies anything.
//
// That is a hard invariant, held by two layers:
//  1. The handler boundary: every tool handler in this package (tools.go,
//     fixadvice.go) takes a *kube.Client (internal/kube/client.go), whose
//     only exported methods are List/Get-shaped (ListCRD, GetCRD, ListPods,
//     GetPod, ListEvents, PodLogs). The typed and dynamic Kubernetes
//     clientsets behind it are unexported fields in a different package,
//     so a handler that holds only the *kube.Client cannot call
//     Create/Update/Delete/Patch/Apply through it. The boundary is on the
//     *kube.Client, not on this package: runServe loads the *rest.Config
//     itself, so keeping handlers on the *kube.Client (and having no
//     mutating call site anywhere in this module) is a code convention,
//     not a compile-time guarantee. main_test.go's
//     TestClientHasNoMutatingMethods is a tripwire on kube.Client's
//     exported method set.
//  2. By RBAC: the Helm chart's mcpServer.enabled ClusterRole grants only
//     get/list/watch (plus get on pods/log) — see
//     charts/gameplane/templates/mcp-server.yaml. This is the authoritative
//     layer and the only one that stops mutation by any code in the
//     process: the API server rejects a write from this ServiceAccount
//     whatever client makes it.
//
// A third, cosmetic layer: every tool this server installs (tools.go)
// carries ReadOnlyHint: true, and main_test.go's
// TestRegisteredToolsAreReadOnly asserts the registered tool set never
// contains a mutating verb — this makes the guarantee visible to MCP
// clients, it doesn't enforce it.
//
// It speaks MCP (JSON-RPC 2.0) over stdio only — never a network port — and
// is deliberately standalone (own module, like audit-syslog-bridge and
// telemetry-receiver) so it can run anywhere: in-cluster via the Helm
// chart's mcpServer.enabled, or locally against a kubeconfig for a dev
// cluster.
//
// Two subcommands:
//
//	idle   (default, no args) — block until terminated. This is what the
//	       Helm Deployment runs as its long-lived container process, purely
//	       so there is always a live container to `kubectl exec` into.
//	serve  — run the actual MCP stdio session for one client. Point an MCP
//	       host's launcher at:
//	         kubectl exec -i deploy/gameplane-mcp-server -n <ns> -- /mcp-server serve
//	       Each exec spawns an independent, isolated session sharing the
//	       pod's ServiceAccount credentials and network access — concurrent
//	       sessions don't interfere with each other or with the idle
//	       placeholder process.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/GameplanePanel/gameplane/mcp-server/internal/kube"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := "idle"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error
	switch cmd {
	case "idle":
		runIdle(ctx)
	case "serve":
		err = runServe(ctx)
	default:
		slog.Error("unknown subcommand", "name", cmd)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("mcp-server exited", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

// runIdle blocks until ctx is cancelled. It performs no cluster access and
// opens no client, so the Deployment's steady-state replica carries no
// standing credentials use beyond existing — real reads happen only inside
// a `serve` session.
func runIdle(ctx context.Context) {
	slog.Info("mcp-server idle: waiting for `kubectl exec -i ... -- /mcp-server serve` sessions", "version", Version)
	<-ctx.Done()
	slog.Info("mcp-server idle: shutting down")
}

// runServe builds a cluster client and runs one MCP session over stdio
// until the client disconnects or ctx is cancelled.
func runServe(ctx context.Context) error {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	client, err := kube.New(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	server := newMCPServer(client)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("mcp session: %w", err)
	}
	return nil
}

// newMCPServer builds the MCP server and installs every read-only tool
// against c.
func newMCPServer(c *kube.Client) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "gameplane-mcp-server",
		Version: Version,
	}, &mcp.ServerOptions{
		Instructions: "Strictly read-only access to a Gameplane cluster: list/get 7 of " +
			"Gameplane's 9 CRDs (not Cluster or NetworkCapture), Pods, Events, and pod " +
			"logs, and get suggested fixes as text via propose_fix. No tool here ever " +
			"creates, updates, patches, deletes, or applies anything.",
	})
	registerTools(server, c)
	return server
}
