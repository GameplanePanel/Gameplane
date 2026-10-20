#!/usr/bin/env bash
# Devcontainer post-create bootstrap for Gameplane.
#
# Installs the extra tooling the Makefile expects but the devcontainer
# features don't provide (kind, oras, golangci-lint, uv, specify), pre-fetches Go
# modules + envtest assets, and installs the web npm deps so the first
# `make test` / `make web-dev` run is fast.
#
# Idempotent: every step is `command -v` guarded or version-checked.

set -euo pipefail

log() { printf "\n\033[1;34m[devcontainer]\033[0m %s\n" "$*"; }

ARCH="$(dpkg --print-architecture)"   # amd64 | arm64
GOBIN_DIR="$(go env GOPATH)/bin"
mkdir -p "$GOBIN_DIR"
case ":$PATH:" in *":$GOBIN_DIR:"*) ;; *) export PATH="$GOBIN_DIR:$PATH" ;; esac

# Ensure $HOME/.local/bin is on PATH for uv/specify.
LOCAL_BIN_DIR="$HOME/.local/bin"
mkdir -p "$LOCAL_BIN_DIR"
case ":$PATH:" in *":$LOCAL_BIN_DIR:"*) ;; *) export PATH="$LOCAL_BIN_DIR:$PATH" ;; esac

# ---------- submodules (modules/ lives in the GameplanePanel/module repo) ----------
# The game-module bundles are a git submodule at modules/; init them so
# `make modules-push` / `make dev-up` find modules/build.sh and the bundles.
if [ -f .gitmodules ]; then
	log "git submodule update --init --recursive"
	git submodule update --init --recursive
fi

# ---------- kind ----------
KIND_VERSION="v0.32.0"
if ! command -v kind >/dev/null 2>&1; then
	log "installing kind ${KIND_VERSION}"
	curl -fsSL -o /tmp/kind \
		"https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-${ARCH}"
	sudo install -m 0755 /tmp/kind /usr/local/bin/kind
	rm -f /tmp/kind
fi

# ---------- oras (≥ 1.2.0; required by modules/build.sh) ----------
ORAS_VERSION="1.2.0"
if ! command -v oras >/dev/null 2>&1; then
	log "installing oras ${ORAS_VERSION}"
	curl -fsSL -o /tmp/oras.tar.gz \
		"https://github.com/oras-project/oras/releases/download/v${ORAS_VERSION}/oras_${ORAS_VERSION}_linux_${ARCH}.tar.gz"
	sudo tar -C /usr/local/bin -xzf /tmp/oras.tar.gz oras
	sudo chmod 0755 /usr/local/bin/oras
	rm -f /tmp/oras.tar.gz
fi

# ---------- golangci-lint ----------
# v2 release binaries work with the in-container Go version. The
# .golangci.yml has been migrated from v1 to v2 schema (v2 introduces new
# linters and removes deprecated ones).
GOLANGCI_VERSION="v2.12.2"
GOLANGCI_BIN="/go/bin/golangci-lint"
if ! "$GOLANGCI_BIN" --version 2>/dev/null | grep -qE 'version v?2\.'; then
	log "installing golangci-lint ${GOLANGCI_VERSION}"
	GOBIN=/go/bin go install \
		"github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION}"
fi

# ---------- envtest assets (K8s 1.36.2; pulled lazily by the Makefile but
# we warm the cache here so the first `make test-integration` doesn't
# need network) ----------
if [ ! -x "$GOBIN_DIR/setup-envtest" ]; then
	log "installing setup-envtest"
	GOBIN="$GOBIN_DIR" go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
fi
log "fetching envtest binaries for K8s 1.36.2"
"$GOBIN_DIR/setup-envtest" use 1.36.2 >/dev/null

# ---------- Go module cache warmup ----------
# Go modules from go.work. Derived from 'use' entries in go.work.
# Keep in sync with go.work when adding/removing modules.
for m in agent api audit-syslog-bridge capture-sidecar gameaction gameproto i18n mcp-server netguard operator sentinel svcutil telemetry-receiver telemetryschema test/e2e tunnel; do
	log "go mod download ($m)"
	( cd "$m" && go mod download )
done

# ---------- Web deps ----------
log "npm ci (web)"
( cd web && npm ci )

# ---------- uv (Python package installer for spec-kit) ----------
if ! command -v uv >/dev/null 2>&1; then
	log "installing uv"
	curl -LsSf https://astral.sh/uv/install.sh | sh
fi

# ---------- specify (spec-kit CLI for GitHub specs) ----------
SPECKIT_VERSION="v1.0.3"
if ! command -v specify >/dev/null 2>&1; then
	log "installing specify (spec-kit) ${SPECKIT_VERSION}"
	uv tool install specify-cli --from "git+https://github.com/github/spec-kit.git@${SPECKIT_VERSION}"
fi

# ---------- AI coding CLIs ----------
# Installed globally so every shell has `claude`, `opencode`, `gemini`, and
# `codex` on PATH. No credentials are baked in: each authenticates interactively
# at runtime (login or its API-key env var), keeping the image sharable and
# secret-free. Falls back to sudo if the node feature's global prefix isn't
# user-writable; best-effort so a registry hiccup doesn't fail the whole setup.
AI_CLIS=(@anthropic-ai/claude-code opencode-ai @google/gemini-cli @openai/codex)
if ! command -v claude >/dev/null 2>&1 \
		|| ! command -v opencode >/dev/null 2>&1 \
		|| ! command -v gemini >/dev/null 2>&1 \
		|| ! command -v codex >/dev/null 2>&1; then
	log "installing AI coding CLIs (claude, opencode, gemini, codex)"
	npm install -g "${AI_CLIS[@]}" || sudo npm install -g "${AI_CLIS[@]}" || \
		log "WARN: AI CLI install failed; run 'npm install -g ${AI_CLIS[*]}' by hand"
fi

# ---------- Friendly summary ----------
log "tool versions"
{
	go version
	node --version
	npm --version
	docker version --format 'docker {{.Server.Version}}' 2>/dev/null || true
	kubectl version --client=true --output=yaml 2>/dev/null | grep gitVersion || true
	helm version --short
	kind version
	oras version | head -1
	golangci-lint --version | head -1
	uv --version
	specify --version
	echo "claude $(claude --version 2>/dev/null || echo '(installed; log in at runtime)')"
	echo "opencode $(opencode --version 2>/dev/null || echo '(installed; log in at runtime)')"
	echo "gemini $(gemini --version 2>/dev/null || echo '(installed; log in at runtime)')"
	echo "codex $(codex --version 2>/dev/null || echo '(installed; log in at runtime)')"
} || true

cat <<'EOF'

[devcontainer] ready.

Quick commands:
  make lint test         # Go + web unit tests, lint
  make test-integration  # envtest tier (operator + api)
  make dev-up            # kind cluster + helm install (~5 min)
  make web-dev           # Vite dev server (forwarded on port 5173)
  make test-e2e          # full kind-based e2e (~10–20 min)

Notes:
  * Docker runs *inside* the devcontainer (docker-in-docker). The kind
    cluster, local registry, and built images all live in this
    container's docker daemon — they're gone when the container is
    rebuilt.
  * Ports 5173 (web dev), 8080/8443 (ingress), and 5001 (OCI registry)
    are forwarded to your host.
  * AI CLIs (claude, opencode, gemini, codex) are installed but NOT logged
    in. Authenticate each on first use (its login flow or API-key env var);
    no credentials are baked into the image.
EOF
