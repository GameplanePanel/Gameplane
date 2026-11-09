# Open Decisions: 026-modular-dev-containers

Unsettled items. Do not treat any of the below as decided until valgul rules on it here.

## Open

No open decisions remain.

## Settled

- valgul, 2026-10-20: each component lists its own required system dependencies (US6, FR-004a, FR-004b).
- valgul, 2026-10-20, OD-1 (FR-002): main components + 3 parts. Components: web, api, operator, agent, website, modules, "Go services and libraries". Parts: dashboard + API, control plane, modules + gp-module. Plus full.
- valgul, 2026-10-20, OD-2 (US2, FR-005): the dashboard + API part includes in-container Docker and a kind cluster.
- valgul, 2026-10-20, OD-3 (FR-017): website and module profiles live in the main repo and also in the website and module repos.
- valgul, 2026-10-20, OD-4 (Principle I): the `devcontainers.yaml` CI build job (every profile built on amd64 and arm64, compile checks run) is this feature's E2E path. No `test/e2e/` Go test. Principle I exemption for spec 026 only; the generator still gets fixture tests in `hack/test-gen-devcontainers.py`.
- valgul, 2026-10-20, OD-5 (FR-003, research R10): align the full profile to the Makefile/CI values: kubectl 1.36 (matches envtest and kind K8s 1.36.2), setup-envtest `release-0.24`, and helm pinned in `versions.toml` instead of `latest`. FR-003 is amended to allow exactly these three changes, and the drift check enforces that the Makefile pins equal `versions.toml`.
