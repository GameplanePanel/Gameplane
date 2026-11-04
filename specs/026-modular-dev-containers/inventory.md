# Spec 026: system dependency inventory (T001)

Read-only scout. Nothing was built, installed, tested or committed.

- Basis: working tree of branch `claude/project-thread-nv63wo` (clean at scout time).
- Submodules as checked out: `modules/` at `2835987` (`v0.3.0-24-g2835987`), `website/` at `ae95098` (`heads/main`), per `.gitmodules:1-4`.
- Paths are repo-relative. `Makefile:N` means the root `Makefile`. `GO_MODULES` is `Makefile:43`, `GO_UNIT_MODULES` is `Makefile:50`.
- Legend for targets: "covers" means the root Makefile loops over the folder, so the folder is built, tested or linted by that target.

## 1. Today's dev container registry

Source: `.devcontainer/devcontainer.json` and `.devcontainer/post-create.sh`.

| Entry | Kind | Evidence (declared / invoked) |
|---|---|---|
| `mcr.microsoft.com/devcontainers/base:ubuntu-24.04` | base image | devcontainer.json:3 |
| `common-utils:2` (zsh, user `vscode`) | feature | devcontainer.json:5-9 |
| `go` 1.26 | feature | devcontainer.json:10-12; post-create.sh:16, 62-63, 71, 81 |
| `node` 24 | feature | devcontainer.json:13-15; post-create.sh:86, 107-113 |
| `docker-in-docker` latest (moby, compose v2) | feature | devcontainer.json:16-20 |
| `kubectl-helm-minikube` kubectl 1.31, helm `latest`, minikube none | feature | devcontainer.json:21-25 |
| `github-cli` | feature | devcontainer.json:26 (no Makefile or script call) |
| `git` | not declared (base image only) | post-create.sh:29-30 (`git submodule update`) |
| `sudo`, `curl`, `dpkg` | not declared (base image) | post-create.sh:15, 37-39, 47-50, 91, 113 |
| `kind` v0.32.0 (curl binary) | script step | post-create.sh:34-41 |
| `oras` 1.2.0 (GitHub release tarball) | script step | post-create.sh:44-52 |
| `golangci-lint` v2.12.2 (`go install`) | script step | post-create.sh:58-64 |
| `setup-envtest@latest` then K8s 1.36.2 assets | script step | post-create.sh:69-74 (`Makefile:171` pins `release-0.24`) |
| `go mod download` for 16 modules | script step | post-create.sh:79-82 (list omits `gp-module`, which is in `go.work:10`) |
| `npm ci` in `web/` | script step | post-create.sh:86 |
| `uv` (astral curl installer) | script step | post-create.sh:89-92 |
| `specify-cli` v1.0.3 via `uv tool install` | script step | post-create.sh:95-99 |
| AI CLIs: `@anthropic-ai/claude-code`, `opencode-ai`, `@google/gemini-cli`, `@openai/codex` (warn-only) | script step | post-create.sh:107-115 |
| submodule init (`modules`, `website`) | script step | post-create.sh:28-31 |
| env `GOTOOLCHAIN=local`, `GAMEPLANE_REG_HOST_PORT=5001`, `CI=1` | container env | devcontainer.json:29-32 |
| forwarded ports 5173, 8080, 8443, 5001, 30565 | container | devcontainer.json:35 |
| VS Code extensions and editor settings | customizations | devcontainer.json:44-68 |

## 2. Components

### 2.1 Go modules (`go.work:3-21`, 17 folders)

Root-Makefile targets that cover every Go module: `build-go` (`Makefile:72-76`), `test-go` (`88-91`), `cover-go` (`107-112`, `go test -race`, which needs cgo), `cover-go-merge` (`124-132`, `go run` of gocovmerge), `cover-go-check` (`135-148`, `go run` of go-test-coverage), `lint-go` (`302-305`, `golangci-lint run`), `tidy` (`482-485`). `test/e2e` is covered by `test-go` only (`GO_UNIT_MODULES`, `Makefile:50`).

| Folder | Module-specific root-Makefile targets (evidence) | External tools and services those targets call | Dockerfile |
|---|---|---|---|
| `agent` | `image-%` via `IMAGES` (`Makefile:313, 330`); `e2e-images` (`:189`) | go, docker | `agent/Dockerfile` |
| `api` | `cover-go-integration` (`:115-121`); `test-integration` (`:174-179`); `i18n-sync` (`:272`, `cd api && go run ./cmd/messages-export`); `check-i18n` (`:281`); `e2e-images` (`:188`); `IMAGES` (`:313`) | go, setup-envtest and K8s 1.36.2 envtest assets (`:166-171`), jq (`:282`) | `api/Dockerfile` |
| `audit-syslog-bridge` | `image-audit-syslog` (`:333-334`); `ALL_IMAGE_NAMES` (`:323`) | docker | `audit-syslog-bridge/Dockerfile` |
| `capture-sidecar` | `IMAGES` (`:313`) | docker | `capture-sidecar/Dockerfile`. CI-only: `libcap2-bin` and a setcap proof (`.github/workflows/ci.yaml:507-508`) |
| `gameaction` | none beyond the GO_MODULES loops | go | none |
| `gameproto` | none beyond the GO_MODULES loops | go | none |
| `gp-module` | `gp-module` builds `bin/gp-module` (`:383-385`); `module-new`, `module-validate`, `module-preview`, `module-package` (`:387-397`) | go | none |
| `i18n` | `i18n-sync` (`:272-273`, `cd i18n && go run ./cmd/i18n-check`); `check-i18n` (`:280-283`) | go, jq (`:282`) | none |
| `mcp-server` | `IMAGES` (`:313`) | docker | `mcp-server/Dockerfile` |
| `netguard` | none beyond the GO_MODULES loops | go | none |
| `operator` | `generate` (`:347-348`, `go run controller-gen`); `manifests` (`:350-362`, controller-gen, then `hack/sync-chart-crds.sh`); `cover-go-integration`; `test-integration`; `i18n-sync` (`:275`, `go run ./hack/condition-ids`); `check-i18n` (`:280`); `e2e-images` (`:187`); `IMAGES` | go, controller-gen (via `go run`, no install), setup-envtest, docker | `operator/Dockerfile` |
| `sentinel` | `IMAGES` (`:313`); image loaded by `deploy/kind/e2e.sh:215-216` | docker | `sentinel/Dockerfile` |
| `svcutil` | none beyond the GO_MODULES loops | go | none |
| `telemetryschema` | none beyond the GO_MODULES loops | go | none |
| `telemetry-receiver` | `IMAGES` (`:313`); `e2e-images` (`:190`) | docker | `telemetry-receiver/Dockerfile` |
| `test/e2e` | `test-go` (`:50`); `test-e2e` (`:193-203`); `test-e2e-keep` (`:206-208`); `test-e2e-bucket` (`:211-213`); `e2e-images` (`:185-191`, incl. `test/e2e/Dockerfile.fakeoidc`); `e2e-up`, `e2e-down` (`:215-221`) | go, kind, kubectl, helm, docker, bash (`buckets.sh`), `deploy/kind/e2e.sh` | `test/e2e/Dockerfile` (`golang:1.27-alpine`, `:13`), `test/e2e/Dockerfile.fakeoidc:11` |
| `tunnel` | `image-tunnel-frp`, `image-tunnel-tailscale`, `image-tunnel-playit` (`:336-343`); `ALL_IMAGE_NAMES` (`:323`) | docker | `tunnel/Dockerfile.frp`, `.tailscale`, `.playit` |

Extra: `tunnel/frpc/go.mod` is a nested module that is not in `go.work`.

Images: `IMAGES := operator api web agent telemetry-receiver sentinel mcp-server capture-sidecar` (`Makefile:313`). `images` builds them with `docker build` (`:326`, `:330`).

#### In-repo `require` and `replace` targets (go.mod evidence)

| Module (go.mod) | In-repo `require` | `replace` target |
|---|---|---|
| `agent` | `netguard` (go.mod:5), `gameaction` (:11) | `../netguard` (:9), `../gameaction` (:15) |
| `api` | `netguard` (:5), `gameaction` (:11), `gp-module` (:18), `telemetryschema` (:25), `i18n` (:37) | `../netguard` (:9), `../gameaction` (:16), `../gp-module` (:23), `../telemetryschema` (:30), `../i18n` (:34) |
| `operator` | `netguard` (:5), `i18n` (:23) | `../netguard` (:16), `../i18n` (:20) |
| `gp-module` | `i18n` (:10) | `../i18n` (:7) |
| `sentinel` | `gameproto` (:6) | `../gameproto` (:13) |
| `telemetry-receiver` | `telemetryschema` (:6) | `../telemetryschema` (:15) |
| `test/e2e` | `gp-module` (:5), `i18n` (:9), `telemetryschema` (:13), `gameproto` (:17) | `../../gp-module` (:7), `../../i18n` (:11), `../../telemetryschema` (:15), `../../gameproto` (:19) |
| `capture-sidecar`, `mcp-server`, `gameproto`, `telemetryschema`, `svcutil`, `audit-syslog-bridge`, `gameaction`, `netguard`, `i18n`, `tunnel` | none | none |

Non-in-repo replace: `tunnel/frpc/go.mod:10` (yamux fork). This is outside `go.work`.

### 2.2 `web/` (React, Vite, TypeScript)

Root-Makefile targets: `build-web` (`Makefile:79-80`, `cd web && npm ci && npm run build`); `test-web` (`:94-95`, `npm test --if-present`); `cover-web` (`:151`); `lint-web` (`:308-309`); `web-dev` (`:477-478`); `test-web-e2e-mock` (`:230-231`, `npm ci && npx playwright install --with-deps chromium`); `test-web-e2e-live` (`:234-235`); `screenshots` (`:238-239`); `check-i18n` (`:279`, `npm run i18n:extract-only`); `IMAGES` (`:313`, docker build of `web/Dockerfile`).

`web/package.json` scripts (`web/package.json:6-28`):

| Script | Line | Tool or file invoked |
|---|---|---|
| `dev` | 7 | `vite` |
| `build` | 8 | `npm run i18n:compile && tsc -b && vite build` |
| `i18n:extract-only` | 9 | `formatjs extract` (`@formatjs/cli`) |
| `i18n:extract` | 10 | `node scripts/i18n-reference.mjs` |
| `i18n:compile` | 11 | `node scripts/i18n-compile.mjs` |
| `i18n:fixtures`, `i18n:pseudo` | 12-13 | `node scripts/i18n-fixtures.mjs`, `node scripts/i18n-pseudo.mjs` |
| `i18n:migrate` | 14 | `node scripts/i18n-migrate.ts` (needs Node type stripping) |
| `i18n:manifest` | 15 | `cd ../i18n && go run ./cmd/i18n-check --manifest ...` |
| `lint` | 17 | `eslint .` |
| `test`, `test:watch`, `test:cover` | 18-20 | vitest |
| `test:e2e:mock`, `test:e2e:live` | 21-22 | `playwright test` |
| `typecheck:e2e` | 23 | `tsc --noEmit -p e2e/tsconfig.json` |
| `screenshots*` | 24-26 | `playwright test` |
| `diff:screenshots` | 27 | `node scripts/compare-screenshots.mjs` |

Dev-dependency tools that need native or external pieces: `@playwright/test` (`web/package.json:54`), `sharp` (`:78`), `typescript` (`:80`). The file has no `engines` field. Container bases in `web/Dockerfile`: `golang:1.27-alpine` (`:11`), `node:26-bookworm-slim` (`:21`), `nginxinc/nginx-unprivileged:1.31-alpine` (`:45`).

### 2.3 `charts/gameplane/` (Helm chart)

Root-Makefile targets: `dev-install` (`Makefile:443-460`, `helm upgrade --install`, helm at `:451`); `dev-down` (`:463-467`, `helm uninstall` at `:464` in remote mode); `manifests` (`:350-362`, `controller-gen` then `hack/sync-chart-crds.sh`, which writes `charts/gameplane/charts/gameplane-crds/crds/` and `charts/gameplane/crd-manifests/`); `e2e-up` through `deploy/kind/e2e.sh` (`:216-217`).

CI (not local): `helm lint charts/gameplane` (`.github/workflows/ci.yaml:1170-1171`, `azure/setup-helm@v5.0.1` at `:1170`); `helm template` matrix (`ci.yaml:1230` onwards).

Chart runtime images: the CRD apply hook uses `registry.k8s.io/kubectl:v1.36.3` (`charts/gameplane/values.yaml:46`, `templates/crd-apply-hook.yaml:220-223`). Chart dependency `gameplane-crds` 0.3.0 (`charts/gameplane/Chart.yaml:21-23`).

`hack/sync-chart-crds.sh` needs bash 4 (`:38`) and `sha256sum` or `shasum` (`:45-49`).

### 2.4 `deploy/kind/` (kind bootstrap scripts)

| Script | Tools and services invoked (evidence) |
|---|---|
| `up.sh` | `need kind/kubectl/helm/docker` (`:195-199`, `type -P`); `kubectl()` wrapper (`:31`); `kind create cluster --config cluster.yaml` (`:207`); `docker run registry:2` as `kind-registry` on `localhost:5001` (`:19`, `:177-179`); `docker exec` containerd `hosts.toml` (`:220-223`); MetalLB `v0.14.9` (`:36`, applied `:51-52`, pools from `docker network inspect kind` at `:75-80`); ingress-nginx `controller-v1.15.1` (`:40`, applied `:245`, wait `:247`) |
| `e2e.sh` | `need()` (`:41`, `command -v`), `need kind/kubectl/helm/docker` (`:175-178`); MetalLB `v0.14.9` (`:45`, applied `:56-57`); `kind create cluster --wait 90s` (`:192`); `kind load docker-image` (`:225`, `:233`, `:245`); `docker build` (`:223`, `:243`); `helm upgrade --install gameplane` (`:384`) |
| `upgrade.sh` | `need` (`:40`), `need kind/kubectl/helm/docker` (`:44-47`); `kind create cluster` (`:54`); `docker build` (`:70`); `kind load` (`:72`); `helm install gameplane` (`:87`). Called by CI at `.github/workflows/ci.yaml:2077` |
| `down.sh` | `kind get clusters`, `kind delete cluster` (`:8`, `:13`). Not called by the Makefile or CI (`Makefile:466` deletes the cluster directly) |
| `cluster.yaml` | kind config (`:7-9`), `containerdConfigPatches` for `kind-registry:5000` (`:16-19`), node port mappings 8080, 8443, 30565 (`:28-39`) |
| `postgres.yaml` | applied by `e2e.sh` for the PostgreSQL variant (`e2e.sh:278`) |

Required by the bootstrap scripts: kind, kubectl, helm, docker, plus the service images MetalLB, ingress-nginx and `registry:2`.

### 2.5 `test/e2e/` (Go suite, separate module)

Root-Makefile targets are listed in 2.1. Runtime binaries that the Go suite executes (evidence):

| Binary | Evidence |
|---|---|
| `kubectl` | `test/e2e/env.go:167`, `:249`, `:392` |
| `helm` | `test/e2e/helm_install_e2e_test.go:359`, `:514`; `upgrade_e2e_test.go:119`, `:286`, `:307`, `:365`, `:521`, `:548`; `gatewayparity_e2e_test.go:235`, `:243`; `telemetry_e2e_test.go:664`; `telemetry_e2e_helpers_test.go:262` |
| `kind` | `test/e2e/multicluster_e2e_test.go:126` (`kind get kubeconfig`); bootstrap via `deploy/kind/e2e.sh` (`test/e2e/e2e_suite_test.go:69-71`) |
| `docker` | `test/e2e/gatewayparity_e2e_test.go:156`, `:170`; `multicluster_e2e_test.go:140` |
| `bash` | `test/e2e/ci_selection_e2e_test.go:64`, `:155` (`buckets.sh`) |
| `curl` | `test/e2e/helm_install_e2e_test.go` (one call) |

Bucket definitions: `test/e2e/buckets.sh` (called as `./buckets.sh regex <BUCKET>` from `Makefile:213`).

### 2.6 `website/` (Astro site, submodule)

The root Makefile has no target that builds `website/`. It is read by `check-features-sync` (`Makefile:259-260`, `hack/check-features-sync.sh:36` reads `website/src/content/docs/features.mdx`). `hack/check-submodule-freshness.sh:32-37` checks the submodule gitlinks; CI runs it at `.github/workflows/ci.yaml:712-714`.

`website/package.json`:
- engines: `"node": ">=22.12.0"` (`:7-9`).
- scripts (`:10-17`): `dev` (`astro dev`), `build` (`astro build`), `preview`, `check` (`astro check`), `lint` (`eslint .`), `astro`.
- dependencies: `astro` `^7.0.5` (`:23`), `@astrojs/mdx`, `@astrojs/sitemap`, `@fontsource-variable/*`.
- devDependencies: `eslint` (`:29`), `typescript-eslint` (`:33`), `@tailwindcss/vite`, `tailwindcss`, `@astrojs/check`.

`website/.github/workflows/ci.yaml` (`check` job): `actions/checkout@v7` (`:16`), `actions/setup-node@v6` with `node-version: 22` and `cache: npm` (`:17-20`), `npm ci` (`:21`), `npm run lint` (`:22`), `npm run check` (`:23`), `npm run build` (`:24`).

`website/.github/workflows/deploy.yaml`: `actions/configure-pages@v6` (`:23`), `withastro/action@v6` with no node version set (`:26`), `actions/deploy-pages@v5` (`:36`).

No `.nvmrc`, `.node-version` or `packageManager` field exists in the website root.

Website dev container has no tools beyond Node and npm (npm ci with `package-lock.json`).

### 2.7 `modules/` (game module bundles, submodule)

Root-Makefile targets: `modules-push` (`Makefile:472-473`, `./modules/build.sh push --registry $(MODULE_REGISTRY) --plain-http`); `module-schema` (`:364-366`, `python3 hack/gen-module-schema.py`); `module-pin` (`:368-376`, `python3 modules/validate.py --pin`, needs network); `dev-up` pushes modules to `localhost:5001` (`:427`).

`modules/build.sh`:
- Requires oras >= 1.2.0 (`:19`); checks `command -v oras` (`:105`); runs `oras push` (`:170`) and `oras tag` (`:211`).
- `--sign` requires cosign >= 2.0 (`:20`, `:107-108`) and `COSIGN_PRIVATE_KEY`; cosign runs at `:202`. The Makefile never passes `--sign`.

`modules/validate.py`:
- PyYAML imported at module load (`:103-109`), so it is required even for `--offline`.
- `--offline` runs `gp-module validate` (`:1319-1338`), with `gp-module` located on PATH or in `bin/` (`:1284-1294`, `shutil.which` at `:1285`). Missing binary exits 2 (`:1331`).
- Default run uses `curl` against registries (`:156`, `:202-207`, Docker Hub `:255-257`), which needs network.

`modules/test-build-sh.sh` and `modules/test-validate-py.sh` run `python3 validate.py` on fixtures (`test-validate-py.sh:50`).

`modules/.github/workflows/validate.yml` (module repo): `setup-python` 3.12 (`:32-34`); `pip install pyyaml` (`:36-37`); `python3 validate.py` (`:39-40`); `./test-build-sh.sh` (`:46-47`); `./test-validate-py.sh` (`:49-50`); `setup-go` 1.26 (`:52-54`); `go run github.com/GameplanePanel/gameplane/i18n/cmd/i18n-check@master` (`:63-64`).

`modules/.github/workflows/refresh-pins.yml` (module repo): `setup-python` 3.12 (`:37-39`); `pip install --user pyyaml` (`:41-42`); `python3 validate.py --pin` (`:52`); `git`, `gh pr create` (`:108-109`).

`modules/validate.py --pin` also needs network (registry reads).

## 3. Makefile targets from research R6 (external tools, file:line)

| Target | Lines | External tools invoked |
|---|---|---|
| `dev-up` (CLUSTER=kind) | `:418-429` | `deploy/kind/up.sh` (`:424`), `make images` (`:425`), `dev-load` (`:426`), `modules-push` (`:427`), `dev-install` (`:428`) |
| `dev-up` (CLUSTER=remote) | `:419-422` | `make images`, `dev-push` (`docker push`, `:437-440`), `modules-push`, `dev-install` |
| `dev-load` | `:432-434` | `kind load docker-image` (`:433`) |
| `dev-install` | `:443-460` | `helm upgrade --install` (`:451`); `KUBECONFIG` only when CLUSTER=remote (`:36`) |
| `modules-push` | `:472-473` | `modules/build.sh` → `oras` (`modules/build.sh:105`, `:170`, `:211`); `cosign` only with `--sign` (not passed) |
| `test-e2e` | `:193-203` | `docker build` via `e2e-images` (`:187-191`); `go test -tags=e2e` (`:198`, `:202`); the Go suite then runs kind, kubectl, helm, docker (2.5) |
| `test-e2e-bucket` | `:211-213` | `go test -tags=e2e`; `./buckets.sh regex` (`:213`) |
| `lint-go` | `:302-305` | `golangci-lint run` (`:304`) |
| `images` | `:326`, pattern `:330`, `:334`, `:337`, `:340`, `:343` | `docker build` |
| `web-dev` | `:477-478` | `npm run dev` → `vite` (`web/package.json:7`) |
| `build-web` | `:79-80` | `npm ci`, `npm run build` (`web/package.json:8`: formatjs-based i18n compile, `tsc -b`, `vite build`) |

Also: `dev-up` and `dev-load` call `kind`; `up.sh` and `e2e.sh` require kind, kubectl, helm and docker (`up.sh:195-199`, `e2e.sh:175-178`).

## 4. Scripts and per-step map of `post-create.sh`

| Step | Lines | Requires |
|---|---|---|
| arch and PATH setup | 15-23 | `dpkg`, `go env GOPATH`, `$HOME/.local/bin` |
| submodule init | 28-31 | `git` |
| kind | 33-41 | `curl`, `sudo install` |
| oras | 43-52 | `curl`, `tar`, `sudo` |
| golangci-lint | 54-64 | `go install` |
| envtest | 66-74 | `go install ...setup-envtest@latest`, `setup-envtest use 1.36.2` |
| go mod download | 76-82 | `go` (16 modules; `gp-module` not listed) |
| npm ci web | 84-86 | `npm` |
| uv | 88-92 | `curl` (astral installer), `sh` |
| specify | 94-99 | `uv tool install` from GitHub git ref |
| AI CLIs | 101-115 | `npm install -g` (sudo fallback) |
| summary | 117-135 | `go`, `node`, `docker`, `kubectl`, `helm`, `kind`, `oras`, `golangci-lint`, `uv`, `specify`, AI CLIs (all `\|\| true`) |

## 5. Services and pinned component images

| Service | Pin and evidence |
|---|---|
| kind cluster | `kind` v0.32.0 (`post-create.sh:34`); cluster name `gameplane-dev` (`deploy/kind/cluster.yaml:9`); node image is not pinned (kind default) |
| MetalLB | `v0.14.9` (`deploy/kind/up.sh:36`, `e2e.sh:45`) |
| ingress-nginx | `controller-v1.15.1` (`deploy/kind/up.sh:40`) |
| local OCI registry | `registry:2`, container `kind-registry`, host port `5001` (`deploy/kind/up.sh:177-179`, `:18-19`) |
| K8s for envtest and e2e | 1.36.2 (`Makefile:166`; `post-create.sh:73-74`) |
| `setup-envtest` | `release-0.24` (`Makefile:171`), `@latest` in devcontainer (`post-create.sh:71`) |
| CRD apply hook image | `registry.k8s.io/kubectl:v1.36.3` (`charts/gameplane/values.yaml:46`) |
| `test/e2e` images | `golang:1.27-alpine`, `gcr.io/distroless/static:nonroot` (`test/e2e/Dockerfile:13`, `:37`) |
| `web` images | `golang:1.27-alpine` (`web/Dockerfile:11`), `node:26-bookworm-slim` (`:21`), `nginxinc/nginx-unprivileged:1.31-alpine` (`:45`) |

Submodules: `modules` (`.gitmodules:1-2`, `GameplanePanel/module.git`), `website` (`.gitmodules:4-5`, `GameplanePanel/website.git`).

## 6. Answers

### (a) Command that validates modules without pushing

- Recommended for the modules profile check: `make module-validate MODULE=<name>` (`Makefile:390-391`). It runs `bin/gp-module validate modules/<name>`. Build the binary first with `make gp-module` (`Makefile:383-385`, Go only, no Python). Needs: go (for the binary). No Python, PyYAML or jsonschema.
- Python wrapper: `python3 modules/validate.py --offline [<name>]` (`modules/validate.py:1319-1338`). Needs python3 and PyYAML (`modules/validate.py:103-109`, module-level import). The `gp-module` binary is also required (`:1331`). No jsonschema is used in `modules/`.
- Not offline: `python3 modules/validate.py` (no flag) queries registries over the network (`validate.py:156`, `:202-207`). CI runs this (`modules/.github/workflows/validate.yml:39-40`). Do not use `make modules-push` (`Makefile:472-473`) or `modules/build.sh push` for the check, since both push.
- `python3 hack/gen-module-schema.py` (`Makefile:365`) imports yaml unguarded (`hack/gen-module-schema.py:21`).

### (b) Node version for the website

- Required: `>=22.12.0` (`website/package.json:7-9`, engines).
- CI: Node 22 with npm cache (`website/.github/workflows/ci.yaml:17-19`).
- Deploy: `withastro/action@v6` sets no node version (`website/.github/workflows/deploy.yaml:26`).
- No `.nvmrc` or `.node-version` file in `website/`.
- Root devcontainer provides Node 24 (`.devcontainer/devcontainer.json:13-15`), which satisfies the engines range.
- Other Node versions in the repo for reference: `web` CI uses Node 24 (`.github/workflows/ci.yaml:996`, `:1139`), `web/Dockerfile` uses Node 26 (`:21`). `web/package.json` has no `engines` field.

### (c) Registration needed for new `.github/workflows/devcontainers.yaml` and new `hack/` files

No registration is required for a new workflow or new hack files in these checks:
- `actionlint` lints every workflow with no arguments (`.github/workflows/ci.yaml:1592-1608`), so the new workflow is discovered automatically.
- `zizmor` lints `inputs: .github` (`ci.yaml:1639-1644`). This is the spec 008 SHA-pin gate (comment `:1634-1636`, rules in `.github/zizmor.yml:7-9`). No list of hardened workflows exists, so T023 has nothing to edit.
- The `github` path filter `.github/workflows/**` (`ci.yaml:278-279`) already triggers the `workflow-lint` job (`:1582-1585`).
- `hack/check-ci-report-coverage.sh:40` reads only `.github/workflows/ci.yaml`. No change.
- `hack/check-publish-edge-paths.sh:46` reads only `publish-edge.yaml`. No change.
- `hack/auto_label.py`: `area` comes from `AREA_BY_DIR` (`:51-65`) with fallback `area: shared` (`:99`). Neither `.devcontainer/` nor `hack/` is mapped, so the fallback applies. No change needed.
- `hack/ci_scope.py`: `.devcontainer`, `hack`, `.github`, `website`, `docs` are in `NO_E2E_DIRS` (`:42-44`), and `is_unmapped` (`:111-117`) therefore does not force full scope for them. No change needed.
- `test/e2e/lint-gate-verify.sh` parses only the `lint:` job in `ci.yaml` and `go.work` and `.golangci.yml` (`:63-64`, `:187`). It does not read the Makefile or workflow files. No change needed.

Required edits that do exist:
- `Makefile:299` (`lint:` prerequisites): add `check-devcontainers`. Add `.PHONY` entries for `devcontainers`, `check-devcontainers`, `test-gen-devcontainers` (T010).
- `docs/contributing.md` and `docs/dependencies.md` are in the fixed link and doc-version audit lists (`hack/check-links.sh:95-96`, `hack/check-doc-versions.sh:75`). Edits there (T030, T031) must keep anchors valid and doc-version markers consistent.

Optional, only if the generator tests should also run from `ci.yaml`: add `hack/gen-devcontainers.py` and `hack/test-gen-devcontainers.py` to the `github` filter (`ci.yaml:278-291`), and add steps to `workflow-lint` (`:1582-1641`). The plan (R7) runs them from `devcontainers.yaml` instead, so this is not needed.

### (d) Every `post-create.sh` reference repo-wide (excluding `specs/done_*`)

Non-spec references:
- `.devcontainer/devcontainer.json:34` (`postCreateCommand`).
- `.devcontainer/post-create.sh:2` (file header).
- `docs/dependencies.md:27` (`` `.devcontainer/post-create.sh`. ``).
- `docs/dependencies.md:410` (golangci-lint "pinned in `.devcontainer/post-create.sh`").

Spec 026 references (expected; these are the work items): `specs/026-modular-dev-containers/spec.md:13`, `:15`; `plan.md:13`, `:73`; `research.md:28`, `:53`, `:68`; `contracts/setup-and-need.md:8`; `tasks.md:37`, `:52`, `:66`, `:145`, `:151`.

Submodules: no references in `modules/` or `website/`. T033 covers `website/src/content/docs/local-development-setup.mdx`, which I did not grep because it is a submodule file the task asked to leave alone.

Unrelated matches of the word "post-create" in code comments (`operator/internal/controller/gameserver_envtest_test.go:1344`, `web/src/routes/tabs/settings/Version.tsx:10`) are not references to the script.

### (e) Makefile targets from R6 and the external tools they run

See section 3. Summary with evidence:
- `dev-up`: `deploy/kind/up.sh` (`Makefile:424`), `docker build` (`:425` via `images`), `kind load` (`:426`), `modules/build.sh` (`:427`), `helm upgrade --install` (`:428`).
- `dev-load`: `kind load docker-image` (`Makefile:433`).
- `dev-install`: `helm upgrade --install` (`Makefile:451`).
- `modules-push`: `modules/build.sh push` (`Makefile:473`) → `oras` (`modules/build.sh:105`, `:170`, `:211`).
- `test-e2e`: `docker build` (`Makefile:187-191`), `go test` (`:198`, `:202`), then kind, kubectl, helm and docker at runtime (`test/e2e/env.go:167`, `helm_install_e2e_test.go:514`, `multicluster_e2e_test.go:126`).
- `test-e2e-bucket`: `go test` (`Makefile:212-213`), `buckets.sh` (`:213`).
- `lint-go`: `golangci-lint run` (`Makefile:304`).
- `images`: `docker build` (`Makefile:330`, `:334`, `:337`, `:340`, `:343`).
- `web-dev`: `npm run dev` → `vite` (`Makefile:478`, `web/package.json:7`).
- `build-web`: `npm ci`, `npm run build` (`Makefile:80`, `web/package.json:8`).

### (f) Gaps

Tools needed by a component that the registry (today's `devcontainer.json` or `post-create.sh`) does not provide. Nothing is installed here.

| Tool | Needed by (evidence) | Status |
|---|---|---|
| `python3` | `Makefile:365`, `:375`, `:495`; `modules/validate.py`; `modules/test-*.sh`; `hack/*.py`; `.specify/scripts/bash/common.sh` | not declared; `post-create.sh` does not install it. Expected from the base image, not verified |
| PyYAML | `modules/validate.py:103-109`; `hack/gen-module-schema.py:21`; `modules/.github/workflows/validate.yml:36-37` | not declared, not installed |
| `jq` | `Makefile:282-283` (`check-i18n`) | not declared, not installed |
| `git` | `post-create.sh:29-30`; `Makefile` submodule flow | not declared; comes from the base image only |
| `gcc` (cgo toolchain) | `Makefile:110`, `:119` (`go test -race` needs cgo) | not declared |
| `cosign` (>= 2.0) | `modules/build.sh:20`, `:107-108`, `:202` (only with `--sign`) | not declared; release path only |
| Playwright browsers and their apt packages | `Makefile:231`, `:235` (`npx playwright install --with-deps chromium`) | not declared; needs apt and sudo |
| `bash` >= 4 | `hack/sync-chart-crds.sh:38` | base image (Ubuntu bash 5) |
| `sudo`, `curl`, `dpkg`, `sha256sum` | `post-create.sh:15`, `:37-50`, `:113`; `hack/sync-chart-crds.sh:45-49` | base image and common-utils; not declared as a dependency |
| `controller-gen`, `gocovmerge`, `go-test-coverage` | `Makefile:55-59`, `:145`, `:348`, `:351` (all via `go run`) | no install needed; fetched through the Go module cache |
| CI-only linters: `actionlint` v1.7.10, `hadolint` v2.14.0, `zizmor` v0.7.1, `govulncheck` v1.9.0, `trivy` | `.github/workflows/ci.yaml:754`, `:1592-1608`, `:1610-1628`, `:1639-1644`; `hack/trivy-scan-images.sh` | CI only; not required in the container |

Registry items used by a component but not present in any profile list yet: none beyond the above. Registry items present but not used by any repo script: `github-cli` (`devcontainer.json:26`). The `gh` CLI is used by agents and CI scripts (`modules/.github/workflows/refresh-pins.yml:108-109`), not by a Makefile target.

Discrepancies found while scouting (for the spec owner to decide, not changed here):
1. `post-create.sh:79-82` `go mod download` list omits `gp-module` (in `go.work:10`).
2. `kubectl` 1.31 (`devcontainer.json:22`) versus K8s 1.36.2 test assets and chart hook 1.36.3 (`values.yaml:46`). OD-5 covers this.
3. `helm` `latest` (`devcontainer.json:23`) is unpinned. OD-5 covers this.
4. `setup-envtest@latest` (`post-create.sh:71`) versus `release-0.24` (`Makefile:171`, `ci.yaml:917`, `:928`). OD-5 covers this.
5. `docs/dependencies.md:409` says `setup-envtest@release-0.19`, which is stale against `Makefile:171`.
6. `docs/dependencies.md:410` and `:467-469` say golangci-lint is not CI-enforced, but `ci.yaml:802-816` runs `golangci-lint-action` in the lint job. Stale doc.
7. Node versions differ across consumers: 24 (devcontainer and web CI), 22 (website CI and engines minimum 22.12), 26 (`web/Dockerfile:21`). No `.nvmrc` exists.
8. `test/e2e/Dockerfile` and `web/Dockerfile` use `golang:1.27-alpine` while `go.work:1` and every `go.mod` say 1.26.0.

## 7. Method note

Everything above was read with `grep` and `cat -n` on the working tree. No command was executed other than read-only inspection. No `.pen` file was read or touched. No submodule branch was changed. No test, lint or build was run.
