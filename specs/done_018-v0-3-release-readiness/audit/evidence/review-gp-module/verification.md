# T045 libs chunk: independent verification (opus)

Method: I tried to refute each of the 13 candidates in `notes.md` (reviewer: opus) against master `13a859ff`. None of the cited files differ between this branch and master, and the `modules/` submodule is at `ff19983`. I read the validator (`internal/validator/{rules,schema,validator,report}.go`), `internal/preview/preview.go`, `internal/archetypes/archetypes.go`, `cmd/gp-module/{main,init,validate,preview}.go`, `pkg/archetypes/archetypes.go`, `specs.md` and `go.mod`. I also read the spec 010 contracts that govern them (`cli-contract.md`, `diagnostics-contract.md`, `archetypes-contract.md`), `spec.md` FR-008 to FR-016 and the Edge Cases section, the CRD types and generated schema (`gametemplate_types.go`, `operator/config/crd/gameplane.local_gametemplates.yaml`, `modules/.schema/gametemplate.schema.json`), the operator code that consumes a module (`module_controller.go:186-248`, `gameserver_config.go:100-230`, `gameserver_version.go`, `gameserver_controller.go:1859-1866`), the API builder (`api/internal/handlers/modules_builder.go`) and the web builder (`web/src/components/modules/BuildModuleDialog.tsx`). I built the CLI into the session scratchpad (`go build`, which left the tree clean) and ran `validate`, `preview` and `init` read-only against `modules/` and against scratch modules in the scratchpad. I ran `make -n` for the documented make targets and `GOWORK=off GOFLAGS=-mod=readonly go list ./...` for C-gp-module-13. I ran no test or lint suite. I checked `audit/findings.md` for existing entries: F-030 and F-035 name gp-module, but only for CLAUDE.md and `docs/dependencies.md`, so none of these candidates is a duplicate. The held candidate is verified separately, off-git (OD-019).

| Candidate | Verdict (kept/rejected) | Severity | Reason |
|---|---|---|---|
| C-gp-module-01 | kept | S3 | Confirmed. The enum rule reads `options`, but the CRD field is `enum` (`gametemplate_types.go:1083`). `validate` reports 7 false errors on `terraria` and `7-days-to-die`. A scratch module that uses `options:` validates clean, but `options` is dropped when the operator decodes the template, so every value is then rejected at runtime. The builder export refuses any report with errors, and a template can be hand-edited in the builder. Workaround: build the bundle outside the builder and use the upload path, or avoid `type: enum`. |
| C-gp-module-02 | kept | S3 | Confirmed. `type: boolean` validates clean (scratch module). The CRD enum is `string;int;bool;enum;password` (`gametemplate_types.go:1073`), and `modules/.schema/gametemplate.schema.json` agrees, so the Module install is rejected by the API server. The error text at `rules.go:280` steers authors to `boolean` and leaves out `bool`. FR-012 and `diagnostics-contract.md:22` list `boolean` too, but FR-008 and the diagnostics contract's own `template-schema-violation` rule (validate against `gametemplate.schema.json`) take priority. Workaround: write `bool`. |
| C-gp-module-03 | kept | S3 | Confirmed, and the gap is wider. A port with no `name` validates clean, and so does a template with no `spec.displayName` and no `spec.version`. The CRD and `gametemplate.schema.json` require `ports[].name` and `spec.{displayName,game,image,version}`, but `validateTemplateSchema` checks only `game` and `image`. I raised it from S4: the check is missing, not just worded badly. The install fails with an API-server error, which works as a workaround. |
| C-gp-module-04 | kept | S3 | Confirmed. Without `--version-id`, preview reports `versionId: default`, the base image and no `TYPE`/`VERSION` for `minecraft-java`. The operator applies the entry marked `default: true` (`1.21.4-paper`), which sets `TYPE=PAPER` and `VERSION=1.21.4`. The web builder's preview never sends a version id (`BuildModuleDialog.tsx:236-239,258`), so it always shows the base layer. Workaround on the CLI: pass `--version-id`. |
| C-gp-module-05 | rejected | n/a | The spec allows this. `cli-contract.md:118` sets the `--memory` default to `4Gi`, described as the "Simulated container memory limit". The output shows both the limit and the source of each computed value ("Memory Limit: 4Gi", "autoFromMemoryLimit: 75% of 4Gi"), and the web builder has its own memory control. Simulating an explicit limit is the documented design, not a divergence. |
| C-gp-module-06 | kept | S4 | Confirmed, but the location is wrong. The validator's requirement is set by the spec: `archetypes-contract.md` §3 (lines 214-222) says "The offline validator … requires `metadata.name` to be present", and that the operator overwrites it (`module_controller.go:207`). The side that contradicts the spec is `docs/module-authoring.md:102` and `:139-141` ("omit `metadata.name`"). A module written to those docs fails `validate` and the builder export gate. The fix belongs in the docs. |
| C-gp-module-07 | kept | S4 | Confirmed. The documented `init … --ports="game:27015/udp:adv"` exits 1 (`invalid port number "game:27015"`). `make -n module-validate MODULE=modules/cs2-match` expands to `validate modules/modules/cs2-match`, and `module-preview` behaves the same way. |
| C-gp-module-08 | kept | S4 | Confirmed. `gp-module pin` exits 2 ("unknown command"). `make module-pin` exists, but it re-pins every module through `modules/validate.py --pin`. `diagnostics-contract.md:17` gives different advice (`crane digest`). |
| C-gp-module-09 | kept | S4 | Confirmed for the flag and the output format. `preview --format json` exits 2 because only `--json` exists, while `cli-contract.md:121` lists `--format yaml|json|text`. The summary line and the per-finding layout differ from `cli-contract.md:92-99`. The `init` point is weaker than stated: prompts appear only for values not given by a flag, and on EOF the defaults are used, so a non-TTY run does not hang. The only differences are the extra prompt text (storage size and path, summary) and no TTY detection, which contradicts `cli-contract.md:66`. |
| C-gp-module-10 | kept | S4 | Confirmed. `specs.md:5` lists the operator module as a dependency, but `go.mod` doesn't require it and no file imports it. `specs.md:53-55` lists `steamcmd.go`, `java.go` and `generic.go`, which don't exist (`internal/archetypes/` holds only `archetypes.go` and its test). `pkg/archetypes/archetypes.go:22` says 128x128, while the implementation and `archetypes-contract.md:230` say 256x256. |
| C-gp-module-11 | rejected | n/a | This is dead code with no user-visible defect. The contract (`cli-contract.md:85`) defines only the default (`--offline`, True, "pure offline validation"), and that default holds because the validator never contacts a registry. No document says that `--offline=false` adds registry checks. "Implies online checks" is an inference. |
| C-gp-module-12 | kept | S4 | Confirmed. `module.yaml` with `gameplaneMinVersion: 9.0.0` validates clean (scratch module) against the tool's own version `1.0.0`. `spec.md:102` (Edge Cases) says that validation "must notify the author". The operator does refuse too-new bundles at install (`module_controller.go:144-147`), so this is only about warning authors early. |
| C-gp-module-13 | rejected | n/a | Not reproduced, and unreachable. From a clean tree, `go build ./cmd/gp-module` in workspace mode left `gp-module/go.sum` unchanged, including with an empty build cache. `GOWORK=off GOFLAGS=-mod=readonly go list ./...` does fail with three missing `/go.mod` hashes, but nothing builds gp-module that way. The Makefile and both CI matrices (`ci.yaml:389,467`) use the workspace. The API image resolves gp-module through a `replace` and so uses `api/go.sum`. No release workflow builds a gp-module binary. |

### C-gp-module-01

**Location:** `gp-module/internal/validator/rules.go:324-361` (the `case "enum":` branch reads `options`). Consumers: `api/internal/handlers/modules_builder.go:358-368` (export gate) and `:198-201` (validate endpoint).

**Repro / observation:**
1. `cd gp-module && go build -o /tmp/gp-module ./cmd/gp-module`, then from the repo root run `/tmp/gp-module validate modules/terraria modules/7-days-to-die`. You get 7 errors of the form `ERROR [invalid-config-type] template.yaml:211:7: configSchema enum field "AUTOCREATE" must specify a non-empty options list`.
2. `modules/terraria/template.yaml:211-216` declares that field as `type: enum` with `enum: ["1", "2", "3"]` and `default: "2"`.
3. The CRD field is `Enum []string json:"enum,omitempty"` (`operator/api/v1alpha1/gametemplate_types.go:1083`). In the generated `operator/config/crd/gameplane.local_gametemplates.yaml`, `spec.configSchema.items.properties` has `enum` and no `options`. `modules/.schema/gametemplate.schema.json` is the same.
4. The operator checks values against `f.Enum` (`operator/internal/controller/gameserver_config.go:151-155`).
5. Copy a scaffolded module and add a config field with `type: enum`, `options: ["a", "b"]` and `default: "a"`. `validate` prints `OK (no findings)`. The operator decodes `template.yaml` into the typed `GameTemplate` with `sigs.k8s.io/yaml` (`module_controller.go:188`), which drops `options`. `f.Enum` stays empty, and `materializeConfig` returns `"a" is not one of []` for every GameServer.
6. The dashboard builder lets the author edit `template.yaml` directly (`web/src/components/modules/BuildModuleDialog.tsx:750-765`) and exports through `modules_builder.go:358-368`, which returns 400 "module validation failed" whenever the report has an error.

**Expected:** The validator checks the CRD's `enum:` list (non-empty, and the default is one of its values), as FR-008 ("against … `GameTemplate` CRD schemas") and FR-012 ("enum lists") require.

**Actual:** CRD-correct enum fields fail validation (12 errors across 5 shipped modules), and the only spelling that passes produces a template whose enum field rejects every value.

### C-gp-module-02

**Location:** `gp-module/internal/validator/rules.go:17-24` (`allowedConfigTypes` includes `"boolean"`) and `rules.go:280` (the error message).

**Repro / observation:**
1. Scaffold a scratch module (`/tmp/gp-module init tbool --archetype generic -y` in a scratch directory). Append a `configSchema` entry `{name: HARDCORE, type: boolean, default: "true"}` to `spec`.
2. `/tmp/gp-module validate modules/tbool` prints `OK (no findings)`.
3. The CRD restricts `spec.configSchema[].type` to `string;int;bool;enum;password` (`operator/api/v1alpha1/gametemplate_types.go:1073`; the same enum is in `operator/config/crd/gameplane.local_gametemplates.yaml` and `modules/.schema/gametemplate.schema.json`). The Module reconciler creates the GameTemplate from the typed struct (`module_controller.go:186-236`), and the API server rejects `type: boolean` with `Unsupported value`.
4. For an invalid type, `rules.go:280` prints "allowed: string, int, enum, boolean, password", which leaves out `bool`, the only boolean spelling the CRD accepts.

**Expected:** The accepted set equals the CRD enum (FR-008, and the diagnostics contract's `template-schema-violation` rule against `gametemplate.schema.json`). The message lists `bool`. The spec text that also lists `boolean` (`spec.md` FR-012, `diagnostics-contract.md:22`) is corrected at the same time.

**Actual:** `boolean` passes `validate` and the builder export gate, and the install then fails.

### C-gp-module-03

**Location:** `gp-module/internal/validator/schema.go:219-354` (`validateTemplateSchema` checks `apiVersion`, `kind`, `metadata.name`, `spec.game` and `spec.image` only); `gp-module/internal/validator/rules.go:145-237` (`validatePorts` checks `containerPort` and `protocol` only).

**Repro / observation:**
1. In a scratch copy of a scaffolded generic module, remove `name: game` from the only port. `validate` prints `OK (no findings)`.
2. In another copy, remove `spec.displayName` and `spec.version`. `validate` prints `OK (no findings)`.
3. The generated CRD requires `spec.ports[].name` (`required: [containerPort, name]`, `MinLength=1` at `gametemplate_types.go:852-854`) and `spec: [displayName, game, image, version]`. `modules/.schema/gametemplate.schema.json` has the same `required` lists. The API server rejects both templates when the Module reconciler creates the GameTemplate.

**Expected:** `diagnostics-contract.md` `template-schema-violation`: "`template.yaml` structure does not conform to `gametemplate.schema.json`". `spec.md` SC-003: offline validation "detects 100% of schema violations". The CRD-required fields are checked.

**Actual:** CRD-required fields can be missing and the module still validates clean. The first report comes from the API server at install.

### C-gp-module-04

**Location:** `gp-module/internal/preview/preview.go:97-127` (the version overlay runs only when `opts.VersionID != ""`); `web/src/components/modules/BuildModuleDialog.tsx:236-239,258` (the builder never sends a version id).

**Repro / observation:**
1. `/tmp/gp-module preview modules/minecraft-java --json` reports `versionId: "default"` with image `itzg/minecraft-server:java21@sha256:f7155587…`, and `effectiveEnv` has no `TYPE` and no `VERSION`.
2. `modules/minecraft-java/template.yaml:121-126` marks `1.21.4-paper` as `default: true`.
3. The operator's `resolveVersion` (`operator/internal/controller/gameserver_version.go:30-51`) picks, when `spec.version` is empty, "the entry marked default, else the first", and the game container gets that version's image and env.
4. `/tmp/gp-module preview modules/minecraft-java --version-id 1.21.4-paper --json` shows `TYPE: PAPER` and `VERSION: 1.21.4`, which is what a real server with no version choice gets.

**Expected:** `cli-contract.md:105`: preview simulates "the Gameplane operator's config materialization and environment variable synthesis". With no version choice, it applies the version the operator would apply.

**Actual:** A preview with no version choice leaves out the version layer that every real server receives. The dashboard builder's preview always shows that incomplete result.

### C-gp-module-06

**Location (corrected):** `docs/module-authoring.md:102` and `docs/module-authoring.md:139-141`. They conflict with `specs/done_010-easy-module-building/contracts/archetypes-contract.md:214-222` (§3) and its implementation at `gp-module/internal/validator/schema.go:294-309`.

**Repro / observation:**
1. `docs/module-authoring.md:139-141` says "`template.yaml` is the same `GameTemplate` you would write today, with one difference: omit `metadata.name`". The directory sketch at `:102` says "template.yaml   # GameTemplate spec (no metadata.name)".
2. `archetypes-contract.md` §3 says: "The offline validator (`gp-module validate`) requires `metadata.name` to be present and non-empty in any `template.yaml`; bundles lacking this field fail validation", and that the operator overwrites the name (`operator/internal/controller/module_controller.go:207`, `desired.Name = mod.Name`).
3. Copy a scaffolded module and delete `metadata.name` from `template.yaml`. `validate` reports `ERROR [template-schema-violation] template.yaml:5: metadata.name is required in template.yaml`, and the builder export gate (`modules_builder.go:365-368`) refuses it.

**Expected:** The authoring guide and the spec agree. Under the spec 010 contract, the guide says to include `metadata.name` (any value; the operator replaces it with the Module name).

**Actual:** An author who follows the guide gets a validation error.

### C-gp-module-07

**Location:** `docs/module-authoring.md:29-34` (init example), `:43`, `:58`, `:73` (make examples); `Makefile:326-333`; `gp-module/cmd/gp-module/init.go:279-306` (`parsePortDef`).

**Repro / observation:**
1. In a scratch directory, run the documented `gp-module init cs2-match --archetype=steamcmd … --ports="game:27015/udp:adv" --categories="Shooter,Co-op" -y`. It exits 1 with `error: invalid port "game:27015/udp:adv": invalid port number "game:27015"`. Only `PORT` and `PORT/PROTOCOL` parse, which matches `cli-contract.md:58`.
2. `make -n module-validate MODULE=modules/cs2-match` prints `bin/gp-module validate modules/modules/cs2-match`, because the target already prefixes `modules/` (`Makefile:327`).
3. `make -n module-preview MODULE=modules/minecraft-java MEMORY=8Gi` prints `bin/gp-module preview modules/modules/minecraft-java --memory 8Gi`. `module-package` (`Makefile:333`, doc `:73`) has the same doubled prefix.

**Expected:** The quickstart commands run as written. That means a port example in `PORT/PROTOCOL` form and `MODULE=<name>` in the make examples.

**Actual:** The `init` example exits 1, and the three make examples point at `modules/modules/<name>`, which doesn't exist.

### C-gp-module-08

**Location:** `gp-module/internal/validator/rules.go:139` (the `image-unpinned` remediation text).

**Repro / observation:**
1. Validate any module whose `spec.image` has no digest. The remediation says "Pin image digest using 'gp-module pin' / 'make module-pin' or append '# gameplane:floating' if intentionally dynamic."
2. `/tmp/gp-module pin` prints `error: unknown command "pin"` and exits 2. `cmd/gp-module/main.go:40-59` has only `init`, `validate`, `preview` and `package`, plus their aliases.
3. `make module-pin` (`Makefile:304-311`) runs `python3 modules/validate.py --pin` and rewrites every module's `template.yaml`, not just the module being authored.
4. `diagnostics-contract.md:17` gives different remediation: "Resolve image digest using `crane digest <image>` or `docker inspect` and append `@sha256:...`".

**Expected:** The remediation names a command that exists and matches the diagnostics contract.

**Actual:** It names a subcommand that doesn't exist, and a make target that re-pins the whole catalog.

### C-gp-module-09

**Location:** `gp-module/cmd/gp-module/preview.go:43-47` (flags); `gp-module/internal/validator/report.go:125-136` (human output); `gp-module/cmd/gp-module/init.go:92-96,116-230` (prompts).

**Repro / observation:**
1. `cli-contract.md:121` lists `--format <type>` with values `yaml`, `json` and `text`. `/tmp/gp-module preview modules/minecraft-java --format json` exits 2 with "flag provided but not defined". Only `--json` exists, and YAML output doesn't exist at all. (`docs/module-authoring.md:64` uses `--json`, which works.)
2. `cli-contract.md:92-99` shows findings as `ERROR [template.yaml:45] [invalid-port-number] …` followed by `-> Remediation:`, and the summary as `SUMMARY: 0 clean, 0 warn-only, 1 with errors (of 1 scanned).` The code prints `ERROR [invalid-port-number] template.yaml:45:7: …`, then `Remediation:`, then `SUMMARY: N module(s) checked, N error(s), N warning(s).` (`report.go:125-136`).
3. `cli-contract.md:66` says interactive mode is the "default when attached to a TTY and missing arguments". The code has no TTY check. Without `-y` it prompts for every value no flag supplied, and it always prompts for storage size and mount path, which have no flag. On a closed stdin, each prompt falls back to its default, so a script doesn't hang, but the prompt text is printed.

**Expected:** The CLI matches `cli-contract.md`, or the contract is updated to match the CLI.

**Actual:** There is no `--format`, the human output format differs from the contract, and there is no TTY detection.

### C-gp-module-10

**Location:** `gp-module/specs.md:5` and `:52-55`; `gp-module/pkg/archetypes/archetypes.go:22`.

**Repro / observation:**
1. `specs.md:5` lists `github.com/ValgulNecron/gameplane/operator/api/v1alpha1` as a dependency. `gp-module/go.mod` doesn't require it, and `grep -rn 'operator/api' gp-module --include='*.go'` finds nothing. The only Kubernetes import is `k8s.io/apimachinery/pkg/api/resource`.
2. `specs.md:53-55` lists `internal/archetypes/steamcmd.go`, `java.go` and `generic.go`. `ls gp-module/internal/archetypes/` shows only `archetypes.go` and `archetypes_test.go`. All three presets are in `archetypes.go:117-220`.
3. `pkg/archetypes/archetypes.go:22` says "PlaceholderIconBytes returns default 128x128 PNG icon bytes." The implementation (`internal/archetypes/archetypes.go:82-85`) draws a 256x256 image, and `archetypes-contract.md:230` says 256x256.

**Expected:** The spec's dependency list and file layout, and the package doc, match the module.

**Actual:** The spec lists one dependency and three files that don't exist, and the package doc gives the wrong icon size.

### C-gp-module-12

**Location:** `gp-module/internal/validator/schema.go:201-214` (only a semver format check).

**Repro / observation:**
1. Copy a scaffolded module and append `gameplaneMinVersion: 9.0.0` to `module.yaml`. `validate` prints `OK (no findings)`.
2. `gp-module --version` prints `1.0.0` (`cmd/gp-module/main.go:9`). No other version is available to compare against.
3. `specs/done_010-easy-module-building/spec.md:102` (Edge Cases): "If a module references a `gameplaneMinVersion` higher than the current tooling version, validation must notify the author of potential version discrepancies."
4. The operator does refuse a bundle that needs a newer operator at install (`operator/internal/controller/module_controller.go:144-147`). The gap is only the early warning the spec asks for.

**Expected:** A warning when `gameplaneMinVersion` is above the version the tool represents. That also requires deciding which version that is, since the hard-coded `1.0.0` is unrelated to the Gameplane release line.

**Actual:** No notification.
