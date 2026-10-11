# Contributing

## Dev loop

```sh
make dev-up      # creates kind cluster, builds images, installs Helm chart
make web-dev     # starts the Vite dev server with proxy to in-cluster API
```

## Design first for UI changes

**Any change to the web dashboard's visual surface starts in
`design.pen` (Pencil), not in code.** Open `design.pen`, update the
relevant screen, review with the maintainers, *then* translate to
React. This keeps the 18 designed screens the source of truth and
prevents the dashboard from drifting into code-led redesigns.

Backend/operator/API changes do not need a Pencil pass.

## UI components

The dashboard is built on **HeroUI** (`@heroui/react`, v3) rather than hand-rolled or Radix primitives. When a screen needs a stock control (button, table, modal, form field, popover, etc.), import it directly from `@heroui/react` — see the [HeroUI docs](https://heroui.com) for the current v3 API surface. When a pattern recurs across screens (a confirm dialog, a status chip, a stat card, the authenticated shell), reuse or extend the Gameplane composition in `web/src/components/ui/` rather than re-wrapping HeroUI from scratch. New reusable compositions belong there too, each with a co-located `*.test.tsx`. Don't add `@radix-ui/*` or `class-variance-authority` — the dashboard's HeroUI rebuild (`specs/done_014-heroui-web-rebuild/`) removed both from `web/package.json`.

## Code style

- **Indentation**: tabs in Go, 2 spaces elsewhere (YAML, JSON, TypeScript, Markdown). LF line endings everywhere.
- **Go**: `gofmt`, `go vet`, `golangci-lint`. Errors are wrapped with `%w` to preserve the cause chain.
- **Linting**: Fix the underlying issue; do not add inline suppression directives (`//nolint:`, `// eslint-disable`). A few centralized exemptions exist in `.golangci.yml` (e.g., `_test.go` files skip errcheck/gosec/unparam, and controller builders skip revive's exported rule) — contributors should not add new inline exemptions on top of these.
- **TypeScript**: strict mode on. ESLint + Prettier. No `any` without a justification comment.
- **Comments**: default to writing none — let naming carry the weight. Add one only when the *why* is non-obvious (hidden invariant, workaround, or constraint a reader would ask about).

## CI and workflows

This repo pins every external GitHub Action to a full 40-hexadecimal commit SHA with a `# vX.Y.Z` trailing comment, enforced by the `zizmor` security scanner in the `workflow-lint` gate. If you're modifying a workflow or adding a new action:

1. Resolve the action's commit SHA using:
   ```sh
   git ls-remote --tags --refs https://github.com/<owner>/<repo>
   ```
2. Pin it in the workflow file:
   ```yaml
   - uses: owner/action@<40-hex-SHA> # vX.Y.Z
   ```
3. Dependabot's `github-actions` entry keeps all pins current — do not bump them by hand.

Before pushing changes to `.github/workflows/` or `.github/actions/`, run the workflow-lint checks locally (the one exception to the "don't run suites locally" rule, since they are static checks):

```sh
go install github.com/rhysd/actionlint/cmd/actionlint@latest
actionlint .github/workflows/*.yaml

uv tool install zizmor
zizmor .github/workflows/ .github/actions/
```

`actionlint` catches workflow schema errors and runs `shellcheck` against `run:` bodies. `zizmor` verifies SHA pinning, least-privilege permissions, dangerous triggers, and other security concerns.

Every job in every workflow must have:
- An explicit `permissions` block granting only what that job needs (top-level default is `contents: read`; anything more is granted only to the specific job that needs it)
- An explicit `timeout-minutes` (default budget is ≤30 minutes; anything above it requires an inline justification comment)

Documented exceptions with longer timeouts are the five e2e jobs (60 minutes each) and the image build in `publish-edge.yaml` (35 minutes).

## Testing

Run the whole suite:

```sh
make test
```

Per-component:

```sh
cd netguard && go test ./...
cd gameaction && go test ./...
cd gameproto && go test ./...
cd svcutil  && go test ./...
cd operator && go test ./...
cd api      && go test ./...
cd agent    && go test ./...
cd audit-syslog-bridge && go test ./...
cd telemetry-receiver && go test ./...
cd sentinel && go test ./...
cd capture-sidecar && go test ./...
cd tunnel   && go test ./...
cd gp-module && go test ./...
cd mcp-server && go test ./...
cd web      && npm test
```

## E2E testing

The e2e suite runs against a real kind cluster. CI splits it into parallel
buckets defined in `test/e2e/buckets.sh` — the `e2e bucket coverage` job fails on
any test that isn't in one, so add new tests to a bucket.

The two game-bot tests (`TestGameServer_MinecraftBotConnects`,
`TestGameServer_TerrariaBotConnects`) boot a real game server and run a
headless protocol bot as a **Job inside the cluster**, dialing the game
Service's DNS name — the same network path a real in-cluster client uses —
rather than tunnelling through `kubectl port-forward`.

The games namespace carries a `default-deny-egress` NetworkPolicy whose
`podSelector: {}` matches every pod in it. This allows DNS resolution but
blocks outbound connections, so an in-cluster helper pod placed there cannot
connect to the game Service. The probe Job therefore runs in the `default`
namespace, where no NetworkPolicy restricts it. Note that `kubectl
port-forward` bypasses NetworkPolicy entirely (it tunnels kubelet→pod), so
"it works via port-forward" is not evidence that in-cluster pod→game traffic
works.

The bot is `test/e2e/cmd/gameprobe`, built into `gameplane-test/gameprobe:e2e`
by the `e2e-gameprobe` bake target. That target sits outside the `e2e` bake
group so only the game-bot job pays to build it, and `deploy/kind/e2e.sh`
side-loads it into the cluster when present. The `e2e game bot (kind)` CI job
is now blocking rather than advisory.

The `ingress-nginx smoke (kind)` CI job is separate from the buckets above: it
boots a Kind cluster on the same node config `make dev-up` uses and applies
the dev ingress-nginx manifest pinned in `deploy/kind/up.sh`
(`INGRESS_NGINX_VERSION`), waiting for the controller Deployment's rollout
to finish (its replica available).
It only runs when `deploy/kind/**` or the CI workflow file itself changes.

## AI-assisted development

Much of this codebase is developed with AI coding assistants (Claude
Code). The project was started on Claude Opus 4.8 (`claude-opus-4-8`);
since June 2026 development continues on Claude Fable 5
(`claude-fable-5`). Agent-facing guidance lives in
[`CLAUDE.md`](../CLAUDE.md) — keep it current when project structure or
house rules change. All contributions, AI-assisted or not, go through
the same review, lint, and test gates below.

## Submitting a change

1. Fork + feature branch
2. **Do not run `make test` or `make lint` locally.** A quick compile check (`go build ./...` or `tsc --noEmit`) is fine to avoid pushing obviously-broken code, but the full test and lint suites run only on CI (GitHub Actions) — that is where they are gated and where failures surface. The exception is workflow changes: if you modify `.github/workflows/` or `.github/actions/`, run `actionlint` and `zizmor` locally before pushing (see CI and workflows section above).
3. For UI work, include the Pencil node id(s) touched in the PR description
4. Sign commits (`git commit -s`)
5. **PR labels**: every PR must carry at least one `type:` and one `area:` label. CodeRabbit (see Code review below) applies them automatically, and the `auto-label` workflow (`hack/auto_label.py`, every 15 minutes) fills in a missing `type:` from the conventional-commit title prefix and a missing `area:` from the changed paths; `type: security` is never derived. You are responsible for verifying they are correct. The `type:` taxonomy is `feature` / `fix` / `refactor` / `test` / `ci` / `chore` / `docs` / `security`. The `area:` taxonomy is `operator` / `api` / `agent` / `web` / `modules` / `chart` / `e2e` / `specs` / `shared` / `optional-components`. A breaking CRD, API, or chart-value change also takes `breaking`. The `status:` labels (`blocked`, `needs-maintainer`, `in-progress`) are optional. A PR spanning several components takes several `area:` labels rather than being left unlabelled.

Game-module changes (`modules/`) belong in the separate **`GameplanePanel/module`**
repo, which this repo vendors as a submodule. Open the module PR there; once it
merges, bump the submodule pointer here (`git add modules`) in a follow-up PR.

`.gitmodules` points both submodules (`modules/`, `website/`) at the upstream
GitHub URLs, so a fork of this repo clones them without forking them too. To
work on your own fork of a submodule, repoint it locally:
`git submodule set-url modules https://github.com/<you>/module.git`
(don't commit that change).

## Code review

PRs receive an automated advisory review from the CodeRabbit GitHub App, configured by `.coderabbit.yaml`. CodeRabbit's rules encode the repo's style guide and house rules — wrapping errors with `%w`, no unjustified `any`, fix rather than silence linter warnings, etc. Its feedback does not block merges. If you think a rule is wrong, raise it with the maintainers rather than working around it — this keeps the rules canonical and consistent across the codebase.

## Release process

Tags matching `v*` trigger the `release.yaml` workflow, which builds the
component images, pushes the Helm chart to the `gameplane` OCI registry, and
keyed-cosign-signs all images (component and chart) and official `modules/*`
bundles by digest, recording all signatures in the public Sigstore Rekor
transparency log.

Signing is **mandatory and fail-closed**: if `COSIGN_PRIVATE_KEY` is not
configured, the release job fails before anything is pushed. A one-time key
setup is required: run `cosign generate-key-pair`, set
`COSIGN_PRIVATE_KEY`/`COSIGN_PASSWORD` as secrets of the `release-signing`
environment, and publish `signing/cosign.pub`. See
[`module-authoring.md`](module-authoring.md#signing-official-bundles) for details.

Every job that signs (`release.yaml`, `publish-edge.yaml`, `images.yaml` and
`republish-modules.yaml`) runs in the `release-signing` environment. Its
deployment policy admits only `master` and `v*` tags, so a run on any other ref
never receives the key. Images are pushed by digest, signed and verified, and
only then tagged, so a published tag always names a signed image. The image
matrix does not cancel sibling legs when one fails. A leg that fails before tag
attachment leaves at most an untagged digest behind. A leg whose `oras tag`
succeeded but whose later `oras resolve` check failed may leave published tags,
so inspect the image's tags before re-running the failed jobs.

## Community Visibility & Outreach

[External Directory Submissions](../specs/012-docs-refresh-and-outreach/outreach.md)

The project maintains a tracked list of external directory submissions
(AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes) to grow
visibility and discoverability. See the outreach tracker for submission
status and history.
