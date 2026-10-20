# Gameplane — top-level Makefile.
# Delegates to per-component Makefiles where present, but exposes a single
# entrypoint for dev workflows (build/test/dev-up/dev-down).

SHELL          := /bin/bash
.DEFAULT_GOAL  := help

REGISTRY       ?= ghcr.io/gameplanepanel/gameplane
TAG            ?= dev
KIND_CLUSTER   ?= gameplane-dev
CHART_DIR      ?= charts/gameplane
CHART_RELEASE  ?= gameplane
NAMESPACE      ?= gameplane-system

# -------- target cluster selection --------
# CLUSTER selects where the dev/deploy targets act:
#   kind   (default) — a local kind cluster these targets create + load into
#   remote          — an existing cluster reached via REMOTE_KUBECONFIG
#                     (e.g. the kubelab k3s test cluster)
# Remote can't use `kind load`, so images must already be in a registry the
# remote nodes can pull (set REGISTRY + run `make dev-push`) and the modules
# pushed to a registry the in-cluster operator can reach (MODULE_REGISTRY +
# MODULE_SOURCE_URL). The kind defaults below keep `make dev-up` unchanged.
CLUSTER           ?= kind
REMOTE_KUBECONFIG ?= $(HOME)/kubelab.yaml
REMOTE_CONTEXT    ?= default
MODULE_SOURCE_URL      ?= kind-registry:5000
MODULE_SOURCE_INSECURE ?= true
# F-232: every helm/kubectl call the dev targets make must be pinned to the
# selected cluster's own context, never whatever context happens to be
# current on the operator's machine — the same hazard deploy/kind/up.sh's
# kubectl() wrapper guards against for direct kubectl calls in that script.
# kind-$(KIND_CLUSTER) matches the context name up.sh derives from the same
# KIND_CLUSTER variable.
ifeq ($(CLUSTER),remote)
KUBECONFIG_ENV := KUBECONFIG=$(REMOTE_KUBECONFIG)
KUBE_CONTEXT   := $(REMOTE_CONTEXT)
else
KUBECONFIG_ENV :=
KUBE_CONTEXT   := kind-$(KIND_CLUSTER)
endif

GO_MODULES     := netguard gameaction gameproto telemetryschema i18n gp-module operator api agent audit-syslog-bridge telemetry-receiver sentinel mcp-server capture-sidecar svcutil tunnel
GO_INTEGRATION_MODULES := operator api

# test/e2e is a separate module (its own go.mod, excluded from the coverage
# gates). Its per-game probe protocol packages carry untagged unit tests that
# must run on every PR, so test-go covers it — but coverage/build/tidy stay on
# GO_MODULES.
GO_UNIT_MODULES := $(GO_MODULES) test/e2e

GO_BUILDFLAGS  ?= -trimpath

# Pinned versions of coverage tooling — pulled via `go run` so no install step.
GO_TEST_COVERAGE_PKG := github.com/vladopajic/go-test-coverage/v2@v2.18.9
# Pinned, not @latest: an unpinned tool resolves to whatever the mirror serves
# that day, so the checksum database is the only thing standing between CI and
# a swapped upstream. gocovmerge has had one commit since 2016.
GOCOVMERGE_PKG       := github.com/wadey/gocovmerge@v0.0.0-20160331181800-b5bfa59ec0ad

# -------- help --------
.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z0-9_.-]+:.*##/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# -------- build --------
.PHONY: build
build: build-go build-web ## Build all components

.PHONY: build-go
build-go: ## Build all Go binaries
	@for m in $(GO_MODULES); do \
		echo ">> building $$m"; \
		(cd $$m && go build $(GO_BUILDFLAGS) ./...) || exit $$?; \
	done

.PHONY: build-web
build-web: ## Build web dashboard
	cd web && npm ci && npm run build

# -------- test --------
.PHONY: test
test: test-go test-web ## Run all tests

.PHONY: test-go
test-go: ## Run Go tests across all modules (no envtest)
	@for m in $(GO_UNIT_MODULES); do \
		echo ">> testing $$m"; \
		(cd $$m && go test ./...) || exit $$?; \
	done

.PHONY: test-web
test-web: ## Run web tests
	cd web && npm test --if-present

# -------- coverage --------
# Coverage profiles land in <module>/coverage/{unit,envtest,merged}.out.
# Threshold gates live in <module>/.testcoverage.yml and are enforced by
# go-test-coverage (run via `go run`, no install step required).

.PHONY: cover cover-go cover-go-integration cover-go-merge cover-go-check cover-web cover-ratchet

cover: cover-go cover-go-integration cover-go-merge cover-go-check cover-web ## Full coverage run + threshold gates

cover-go: ## Per-module Go unit coverage profiles (no envtest)
	@for m in $(GO_MODULES); do \
		echo ">> unit coverage ($$m)"; \
		mkdir -p $$m/coverage; \
		(cd $$m && go test -race -covermode=atomic -coverpkg=./... \
			-coverprofile=coverage/unit.out ./...) || exit $$?; \
	done

cover-go-integration: envtest-bin ## Per-module Go envtest coverage profiles (operator, api)
	@for m in $(GO_INTEGRATION_MODULES); do \
		echo ">> envtest coverage ($$m)"; \
		mkdir -p $$m/coverage; \
		(cd $$m && KUBEBUILDER_ASSETS="$$($(ENVTEST_BIN) use $(ENVTEST_K8S_VERSION) -p path)" \
			go test -race -tags=envtest -timeout 10m -covermode=atomic -coverpkg=./... \
			-coverprofile=coverage/envtest.out ./...) || exit $$?; \
	done

cover-go-merge: ## Merge unit + envtest profiles into coverage/merged.out per module
	@for m in $(GO_MODULES); do \
		mkdir -p $$m/coverage; \
		profiles=$$(ls $$m/coverage/unit.out $$m/coverage/envtest.out 2>/dev/null); \
		if [ -z "$$profiles" ]; then \
			echo ">> no profiles for $$m, skipping merge"; continue; \
		fi; \
		echo ">> merging coverage profiles ($$m)"; \
		go run $(GOCOVMERGE_PKG) $$profiles > $$m/coverage/merged.out || exit $$?; \
	done

cover-go-check: ## Run go-test-coverage threshold gates per module
	@for m in $(GO_MODULES); do \
		if [ ! -f $$m/.testcoverage.yml ]; then \
			echo ">> $$m has no .testcoverage.yml, skipping"; continue; \
		fi; \
		profile=$$m/coverage/merged.out; \
		[ -f $$profile ] || profile=$$m/coverage/unit.out; \
		if [ ! -f $$profile ]; then \
			echo ">> no coverage profile for $$m, skipping (run cover-go to gate it)"; continue; \
		fi; \
		echo ">> threshold check ($$m)"; \
		go run $(GO_TEST_COVERAGE_PKG) \
			--config=$$m/.testcoverage.yml \
			--profile=$$profile || exit $$?; \
	done

cover-web: ## Web coverage with vitest thresholds
	cd web && npm run test:cover --if-present

cover-ratchet: ## Print measured-vs-threshold delta per module to spot ratchet headroom
	@for m in $(GO_MODULES); do \
		profile=$$m/coverage/merged.out; \
		[ -f $$profile ] || profile=$$m/coverage/unit.out; \
		if [ ! -f $$profile ]; then \
			echo "$$m: no coverage profile (run make cover-go)"; continue; \
		fi; \
		measured=$$(cd $$m && go tool cover -func=$$(echo $$profile | sed "s|^$$m/||") | awk '/^total:/ {print $$3}'); \
		threshold=$$(awk '/^[[:space:]]*total:/ {print $$2; exit}' $$m/.testcoverage.yml 2>/dev/null); \
		printf "%-10s measured=%-7s threshold=%s\n" "$$m" "$${measured:-?}" "$${threshold:-unset}"; \
	done

# -------- integration tier (envtest) --------
ENVTEST_K8S_VERSION ?= 1.36.2
ENVTEST_BIN          := $(shell pwd)/operator/bin/setup-envtest

.PHONY: envtest-bin
envtest-bin: ## Install setup-envtest binary into operator/bin
	GOBIN=$(shell pwd)/operator/bin go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24

.PHONY: test-integration
test-integration: envtest-bin ## Run envtest-tagged integration tests
	@for m in operator api; do \
		echo ">> integration tests ($$m)"; \
		(cd $$m && KUBEBUILDER_ASSETS="$$($(ENVTEST_BIN) use $(ENVTEST_K8S_VERSION) -p path)" \
			go test -race -tags=envtest -timeout 10m ./...) || exit $$?; \
	done

# -------- e2e tier (kind + helm + real components) --------
KIND_E2E_CLUSTER ?= gameplane-e2e
KIND_E2E_TAG     ?= e2e

.PHONY: e2e-images
e2e-images: ## Build operator/api/agent/fakeoidc images tagged for e2e
	docker build -t gameplane-test/operator:$(KIND_E2E_TAG) -f operator/Dockerfile .
	docker build -t gameplane-test/api:$(KIND_E2E_TAG)      -f api/Dockerfile      .
	docker build -t gameplane-test/agent:$(KIND_E2E_TAG)    -f agent/Dockerfile    .
	docker build -t gameplane-test/telemetry-receiver:$(KIND_E2E_TAG) -f telemetry-receiver/Dockerfile .
	docker build -t gameplane-test/fakeoidc:$(KIND_E2E_TAG) -f test/e2e/Dockerfile.fakeoidc .

.PHONY: test-e2e
test-e2e: ## Run E2E tests (CLUSTER=kind spins an ephemeral cluster; CLUSTER=remote reuses REMOTE_KUBECONFIG)
ifeq ($(CLUSTER),remote)
	cd test/e2e && GAMEPLANE_E2E_REUSE_CLUSTER=1 \
		GAMEPLANE_E2E_CONTEXT=$(REMOTE_CONTEXT) KUBECONFIG=$(REMOTE_KUBECONFIG) \
		go test -tags=e2e -timeout 50m -v ./...
else
	$(MAKE) e2e-images
	cd test/e2e && GAMEPLANE_E2E_CLUSTER=$(KIND_E2E_CLUSTER) GAMEPLANE_E2E_TAG=$(KIND_E2E_TAG) \
		go test -tags=e2e -timeout 50m -v ./...
endif

.PHONY: test-e2e-keep
test-e2e-keep: ## Re-run E2E tests against an already-up cluster (skip create/destroy)
	cd test/e2e && GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CLUSTER=$(KIND_E2E_CLUSTER) \
		go test -tags=e2e -timeout 50m -v ./...

.PHONY: test-e2e-bucket
test-e2e-bucket: ## Run one CI e2e bucket against an already-up cluster (BUCKET=operator|api-auth|api-roles|api-rbac|api-agent|api-mods|ratelimit|bot-fast|bot-heavy|multicluster|upgrade). BUCKET=multicluster additionally needs a second kind cluster up and GAMEPLANE_E2E_CLUSTER_B set to its name (default gameplane-e2e-b).
	cd test/e2e && GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CLUSTER=$(KIND_E2E_CLUSTER) \
		go test -tags=e2e -timeout 50m -v -run "$$(./buckets.sh regex $(BUCKET))" ./...

.PHONY: e2e-up
e2e-up: e2e-images ## Bring up the e2e kind cluster + install chart (no tests)
	./deploy/kind/e2e.sh up $(KIND_E2E_CLUSTER) $(KIND_E2E_TAG)

.PHONY: e2e-down
e2e-down: ## Tear down the e2e kind cluster
	./deploy/kind/e2e.sh down $(KIND_E2E_CLUSTER)

# -------- web e2e (Playwright) --------
# Mock mode: vite + MSW intercepting fetches. No cluster needed.
# Live mode: vite proxies fetches to a kubectl port-forward globalSetup
# spawns against the gameplane-e2e cluster. Run after `make e2e-up` and
# the Go e2e suite (which writes the admin password to test/e2e/.tmp/).

.PHONY: test-web-e2e-mock
test-web-e2e-mock: ## Playwright tests against vite + MSW mocks (no cluster)
	cd web && npm ci && npx playwright install --with-deps chromium && npm run test:e2e:mock

.PHONY: test-web-e2e-live
test-web-e2e-live: ## Playwright tests against the live e2e cluster (requires e2e-up + Go e2e)
	cd web && npm ci && npx playwright install --with-deps chromium && npm run test:e2e:live

.PHONY: screenshots
screenshots: ## Capture dashboard screenshots via Playwright mock mode (CI or maintainers; agents do not run it locally, OD-15)
	cd web && npm ci && npm run screenshots

# -------- lint --------
.PHONY: check-specs
check-specs: ## Verify all modules have valid, non-empty specs.md
	hack/check-specs.sh

.PHONY: check-doc-versions
check-doc-versions: ## Verify documentation version strings match the chart appVersion
	hack/check-doc-versions.sh

.PHONY: test-doc-versions
test-doc-versions: ## Run the fixture tests for hack/check-doc-versions.sh
	hack/test-check-doc-versions.sh

.PHONY: check-links
check-links: ## Verify internal documentation links and anchors resolve
	hack/check-links.sh

.PHONY: check-features-sync
check-features-sync: ## Verify FEATURES.md and the website features page carry the same capability catalog
	bash hack/check-features-sync.sh

.PHONY: test-features-sync
test-features-sync: ## Run the fixture tests for hack/check-features-sync.sh
	bash hack/test-check-features-sync.sh

.PHONY: check-telemetry-catalog
check-telemetry-catalog: ## Verify telemetryschema/catalog.txt matches the chart's official module list
	hack/check-telemetry-catalog.sh

.PHONY: i18n-sync
i18n-sync: ## Write the API message registry (server.* and notify.*) and the operator condition.* ids into the English reference (i18n/locales/en/app.json)
	cd api && go run ./cmd/messages-export > ../i18n/.registry.json
	cd i18n && go run ./cmd/i18n-check --registry .registry.json --sync-server locales
	rm -f i18n/.registry.json
	cd operator && go run ./hack/condition-ids -app ../i18n/locales/en/app.json

.PHONY: check-i18n
check-i18n: ## Verify language packs, server.*, notify.*, operator condition.* drift, dashboard extraction drift and unused reference ids (spec 019)
	cd web && npm run i18n:extract-only
	cd operator && go run ./hack/condition-ids -check -app ../i18n/locales/en/app.json -ids ../i18n/.condition-ids.txt
	cd api && go run ./cmd/messages-export > ../i18n/.registry.json
	cd i18n && { jq -r 'keys[]' ../web/src/generated/i18n/extracted.json; jq -r '.[].id' .registry.json; cat .condition-ids.txt; } | sort -u > .used-ids.txt
	cd i18n && status=0; go run ./cmd/i18n-check --extracted ../web/src/generated/i18n/extracted.json --registry .registry.json --check-server --used-ids .used-ids.txt locales || status=$$?; rm -f .registry.json .condition-ids.txt .used-ids.txt; exit $$status

.PHONY: check-dev-load-images
check-dev-load-images: ## Verify `dev-load` loads every image `images` builds
	hack/check-dev-load-images.sh

.PHONY: check-coverage-gaps
check-coverage-gaps: ## Validate spec 013's coverage gap record and prove the validator can fail
	hack/check-coverage-gaps.sh
	hack/check-coverage-gaps_test.sh

.PHONY: test-up-registry
test-up-registry: ## Run the fixture tests for deploy/kind/up.sh's registry/context bootstrap
	hack/test-up-registry.sh

.PHONY: lint
lint: check-doc-versions check-links check-features-sync check-specs check-telemetry-catalog check-i18n check-dev-load-images check-coverage-gaps test-up-registry lint-go lint-web ## Run all linters

.PHONY: lint-go
lint-go: ## Run golangci-lint across all modules
	@for m in $(GO_MODULES); do \
		(cd $$m && golangci-lint run) || exit $$?; \
	done

.PHONY: lint-web
lint-web: ## Run web linters
	cd web && npm run lint --if-present

# -------- images --------
# Standard-shaped images (Dockerfile path = <dir>/Dockerfile)
IMAGES := operator api web agent telemetry-receiver sentinel mcp-server capture-sidecar

# The full set of image names `make images` builds, by their docker tag
# names (not their Makefile target names — audit-syslog-bridge and the three
# tunnel-* images use non-standard target names but standard tags). Kept in
# sync with `images`'s prerequisite list below; `dev-load` (F-255) loads
# every name here so opt-in components (mcp-server, capture, quiesce/
# sentinel, a tunnel relay, telemetry-receiver, the audit-syslog bridge)
# work out of the box on a Kind dev cluster instead of silently failing to
# pull their image.
ALL_IMAGE_NAMES := $(IMAGES) audit-syslog-bridge tunnel-frp tunnel-tailscale tunnel-playit

.PHONY: images
images: $(addprefix image-,$(IMAGES)) image-audit-syslog image-tunnel-frp image-tunnel-tailscale image-tunnel-playit ## Build all container images

# Pattern rule for standard-shaped images
image-%: ## Build container image
	docker build -t $(REGISTRY)/$*:$(TAG) -f $*/Dockerfile .

# Non-standard images (target name ≠ dir, or special Dockerfile paths)
image-audit-syslog: ## Build audit-syslog-bridge image
	docker build -t $(REGISTRY)/audit-syslog-bridge:$(TAG) -f audit-syslog-bridge/Dockerfile .

image-tunnel-frp: ## Build tunnel-frp image
	docker build -t $(REGISTRY)/tunnel-frp:$(TAG) -f tunnel/Dockerfile.frp .

image-tunnel-tailscale: ## Build tunnel-tailscale image
	docker build -t $(REGISTRY)/tunnel-tailscale:$(TAG) -f tunnel/Dockerfile.tailscale .

image-tunnel-playit: ## Build tunnel-playit image
	docker build -t $(REGISTRY)/tunnel-playit:$(TAG) -f tunnel/Dockerfile.playit .

# -------- codegen --------
.PHONY: generate manifests module-schema module-pin
generate: ## Run controller-gen deepcopy generators
	cd operator && go run sigs.k8s.io/controller-tools/cmd/controller-gen object paths=./api/...

manifests: ## Regenerate CRDs + RBAC manifests (and sync chart CRD copies)
	cd operator && go run sigs.k8s.io/controller-tools/cmd/controller-gen \
		crd:generateEmbeddedObjectMeta=true \
		rbac:roleName=manager-role \
		paths=./... \
		output:crd:artifacts:config=config/crd \
		output:rbac:artifacts:config=config/rbac
	# Copy the CRDs into the conditional CRD subchart and crd-manifests/ (the same CRDs
	# where Helm's .Files can read them -- it can't read the special crds/
	# dir -- so the crds.autoApply hook can apply them), stamping each copy
	# with a bundle hash the hook compares against the live CRDs to tell
	# leftover, stale CRDs apart from ones crds/ just created (F-218).
	hack/sync-chart-crds.sh

module-schema: ## Regenerate the editor JSON Schema for module template.yaml from the CRD
	python3 hack/gen-module-schema.py
	@echo "commit modules/.schema/gametemplate.schema.json in the GameplanePanel/module repo + bump the submodule pointer"

module-pin: ## Re-resolve every module image tag to its current digest (rewrites modules/*/template.yaml)
	# Module images are pinned by digest so an upstream retag can never change a
	# running game's binary. That also means new upstream builds only arrive when
	# someone runs this. Each changed digest is a game binary that changes for
	# every server on its next restart -- read the diff before committing.
	# Entries marked `# gameplane:floating` are skipped on purpose.
	# The GameplanePanel/module repo also runs this monthly via refresh-pins.yml.
	python3 modules/validate.py --pin
	@echo "commit the rewritten modules/*/template.yaml in the GameplanePanel/module repo + bump the submodule pointer"

BIN_DIR ?= bin
GP_MODULE ?= $(BIN_DIR)/gp-module

.PHONY: gp-module module-new module-validate module-preview module-package

gp-module: ## Build gp-module CLI binary
	@mkdir -p $(BIN_DIR)
	(cd gp-module && go build $(GO_BUILDFLAGS) -o ../$(GP_MODULE) ./cmd/gp-module)

module-new: gp-module ## Scaffold a new module (NAME=... ARCHETYPE=...)
	@$(GP_MODULE) init $(NAME) $(if $(ARCHETYPE),--archetype $(ARCHETYPE))

module-validate: gp-module ## Validate module(s) offline (MODULE=...)
	@$(GP_MODULE) validate $(if $(MODULE),modules/$(MODULE))

module-preview: gp-module ## Dry-run preview of module config (MODULE=... MEMORY=... VERSION=...)
	@$(GP_MODULE) preview modules/$(MODULE) $(if $(MEMORY),--memory $(MEMORY)) $(if $(VERSION),--version-id $(VERSION))

module-package: gp-module ## Package module bundle (MODULE=... REGISTRY=... OUTPUT=...)
	@$(GP_MODULE) package modules/$(MODULE) $(if $(REGISTRY),--registry $(REGISTRY)) $(if $(OUTPUT),--output $(OUTPUT))

# -------- local dev cluster (kind) --------
.PHONY: dev-up dev-down dev-load dev-push dev-install

# Which address manager the operator translates spec.networking.addressPool
# for. deploy/kind/up.sh installs MetalLB into the kind cluster, so the kind
# path must say so or every pool request comes back NoAddressManagerConfigured
# while MetalLB quietly hands out a default-pool address that looks like it
# worked. It has to be passed on *this* install: dev-install runs after
# up.sh and without --reuse-values, so a helm upgrade from the bootstrap
# script would be discarded here. A remote cluster's address manager is the
# admin's choice, so it defaults to none there and is overridden on the
# command line (`make dev-install ADDRESS_MANAGER=cilium`).
ifeq ($(CLUSTER),remote)
ADDRESS_MANAGER ?= none
else
ADDRESS_MANAGER ?= metallb
endif

dev-up: ## Create/prepare cluster + install Gameplane (CLUSTER=kind|remote)
ifeq ($(CLUSTER),remote)
	$(MAKE) images TAG=$(TAG)
	$(MAKE) dev-push TAG=$(TAG)
	$(MAKE) modules-push MODULE_REGISTRY=$(MODULE_REGISTRY)
	$(MAKE) dev-install
else
	./deploy/kind/up.sh $(KIND_CLUSTER)
	$(MAKE) images TAG=$(TAG)
	$(MAKE) dev-load
	$(MAKE) modules-push MODULE_REGISTRY=localhost:5001
	$(MAKE) dev-install
endif

dev-load: ## Load every image `make images` builds into the kind cluster (kind only)
	@for img in $(ALL_IMAGE_NAMES); do \
		kind load docker-image $(REGISTRY)/$$img:$(TAG) --name $(KIND_CLUSTER) || exit $$?; \
	done

dev-push: ## Push operator/api/web/agent images to REGISTRY (remote clusters)
	docker push $(REGISTRY)/operator:$(TAG)
	docker push $(REGISTRY)/api:$(TAG)
	docker push $(REGISTRY)/web:$(TAG)
	docker push $(REGISTRY)/agent:$(TAG)

dev-install: ## Install Gameplane Helm chart into the selected cluster
	# CRD schema changes land in-place on upgrade via the chart's pre-upgrade
	# hook (crds.autoApply), which runs `kubectl apply --server-side` — no
	# manual apply needed here anymore, and it works for any `helm upgrade`,
	# not just this target. On a first install the hook also fires on
	# pre-install, but only over leftover CRDs whose bundle stamp is stale or
	# missing (an earlier, uninstalled release's); fresh CRDs come from crds/.
	# Deploy the bundled telemetry receiver and point the API at it: never let
	# CI or dev installs reach the project provider (spec 022 R17).
	$(KUBECONFIG_ENV) helm upgrade --install $(CHART_RELEASE) $(CHART_DIR) \
		--kube-context $(KUBE_CONTEXT) \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.tag=$(TAG) \
		--set image.registry=$(REGISTRY) \
		--set operator.addressManager=$(ADDRESS_MANAGER) \
		--set api.telemetry.receiver.enabled=true \
		--set defaultModuleSource.type=oci \
		--set defaultModuleSource.oci.url=$(MODULE_SOURCE_URL) \
		--set defaultModuleSource.oci.insecure=$(MODULE_SOURCE_INSECURE)

dev-down: ## Tear down: delete the kind cluster, or uninstall on a remote cluster
ifeq ($(CLUSTER),remote)
	$(KUBECONFIG_ENV) helm uninstall $(CHART_RELEASE) --kube-context $(KUBE_CONTEXT) --namespace $(NAMESPACE)
else
	kind delete cluster --name $(KIND_CLUSTER)
endif

# -------- module bundles --------
.PHONY: modules-push
MODULE_REGISTRY ?= localhost:5001
modules-push: ## Push every modules/<name> as an OCI bundle to MODULE_REGISTRY
	./modules/build.sh push --registry $(MODULE_REGISTRY) --plain-http

# -------- web dev --------
.PHONY: web-dev
web-dev: ## Run web dashboard against in-cluster API
	cd web && npm run dev

# -------- tidy --------
.PHONY: tidy
tidy: ## go mod tidy across all modules
	@for m in $(GO_MODULES); do \
		(cd $$m && go mod tidy) || exit $$?; \
	done

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist
	@for m in $(GO_MODULES); do rm -rf $$m/bin; done
	rm -rf web/dist web/.vite

.PHONY: test-ci-scope
test-ci-scope: ## Verify dependency-aware CI and image selection (CI only)
	python3 -m unittest discover -s hack -p 'test_ci_scope.py' -v
