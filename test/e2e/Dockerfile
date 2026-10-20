# Per-game probe images for e2e game-bot tests.
#
# Builds one probe binary per game: for each directory under test/e2e/internal/
# that contains an app.go file, builds test/e2e/internal/<game>/app.go and
# installs the binary as /probe/<game>. These binaries run as Kubernetes Jobs
# *inside* the kind cluster, exercising the same network path a real in-cluster
# client uses instead of a `kubectl port-forward` tunnel.
#
# Each probe owns its own retry loop: a game server may accept TCP and answer
# status queries before it will accept a login, so a single attempt races world
# generation. Retrying in the probe (rather than in the test) keeps the Job a
# one-shot pass/fail.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY test/e2e/ ./
# Per-game probes import only the stdlib plus this module's bot packages, so
# they build against test/e2e's go.mod alone — no need to drag in the go.work
# workspace (and its k8s dependencies) just to compile a TCP client.
ENV GOWORK=off
ENV CGO_ENABLED=0

# Build every per-game probe: scan for internal/*/app.go, extract the game
# name (directory), and compile the binary.
# DL4006: fail the RUN if any command in a pipe fails.
SHELL ["/bin/ash", "-o", "pipefail", "-c"]
RUN mkdir -p /out/probe && \
    for appfile in internal/*/app.go; do \
        game=$(dirname "$appfile" | cut -d/ -f2); \
        echo "Building probe for $game..."; \
        go build -trimpath -ldflags "-s -w" -o "/out/probe/$game" "./internal/$game"; \
        if [ $? -ne 0 ]; then \
            echo "Failed to build probe for $game" >&2; \
            exit 1; \
        fi; \
    done

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/probe /probe
