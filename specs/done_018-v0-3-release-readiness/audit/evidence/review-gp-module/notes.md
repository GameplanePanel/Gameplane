# Review: gp-module

- **Date**: 2026-09-24
- **Reviewer tier**: opus (verification pending)
- **Checked against**: `gp-module/specs.md`; `specs/done_010-easy-module-building/spec.md` (FR-008, FR-012, FR-016, edge cases, SC-002); `specs/done_010-easy-module-building/contracts/cli-contract.md`; `specs/done_010-easy-module-building/contracts/diagnostics-contract.md`; `specs/done_010-easy-module-building/contracts/archetypes-contract.md`; `docs/module-authoring.md:13-141`; the GameTemplate CRD `operator/config/crd/gameplane.local_gametemplates.yaml` and `operator/api/v1alpha1/gametemplate_types.go`; `modules/.schema/module.schema.json`

## Scope reviewed

Read in full (all non-test source): `cmd/gp-module/{main,init,validate,preview,package}.go`; `internal/archetypes/archetypes.go`; `internal/common/{validation,yaml_ast}.go`; `internal/packager/packager.go`; `internal/preview/{preview,memory}.go`; `internal/scaffold/scaffold.go`; `internal/validator/{validator,rules,schema,report}.go`; `pkg/{archetypes,packager,preview,scaffold,validator}/*.go`; `specs.md`; `go.mod`; `.testcoverage.yml`.

Test files (`internal/*/*_test.go`, 7 files): only the list of test functions and a grep for enum/boolean/`gameplaneMinVersion` coverage. Not read line by line. `go.sum` not read.

Supporting code read to compare against runtime behaviour: `operator/internal/controller/gameserver_config.go:60-230` (config materialization and `autoMemoryValue`), `operator/internal/controller/gameserver_version.go:15-60` (`resolveVersion`), `operator/internal/controller/gameserver_controller.go:1859-1866` (`effectiveResources`), `operator/api/v1alpha1/gametemplate_types.go:1056-1130,836-878`, `operator/internal/modsrc/bundle.go:55-96`, `operator/internal/oci/bundle.go:1-35`, `modules/build.sh:100-200`, `api/internal/handlers/modules_builder.go:130-250,330-432`, `Makefile:283-333`.

## Method

- Went through each responsibility and invariant in `gp-module/specs.md` and each rule, flag and output format in the spec 010 contracts, and traced them through the code.
- Built the CLI (`go build ./cmd/gp-module` into the session scratchpad) and ran it read-only against the checked-out `modules/` submodule (`gameplane-module` @ `ff19983`, main repo @ `3de03ab0`). I also ran it against modules scaffolded into the scratchpad. These are CLI runs, not the test or lint suites. The one repo side effect was that the in-workspace `go build` rewrote the tracked `gp-module/go.sum`. I restored it with `git checkout -- gp-module/go.sum`; see C-gp-module-13.
- Compared validator rules with the generated GameTemplate CRD schema (the `required` lists and `enum`s read with Python/PyYAML).
- Compared preview semantics with the operator's `materializeConfig`, `resolveVersion` and `effectiveResources`.

Results of `gp-module validate --json` over all 30 shipped modules: 13 errors, 29 warnings. 12 of the errors are `invalid-config-type` from C-gp-module-01, in `7-days-to-die` (3), `minecraft-java` (2), `nuclear-option` (1), `terraria` (4) and `tmodloader` (2). One is a real `duplicate-port-collision` in `left-4-dead-2` (see Questions). All 29 warnings are `unrecognized-category`.

## Observations (no finding)

- Packaging matches the canonical push in `modules/build.sh:139-156`: the same artifact type and layer media types (`packager.go:19-24`), the `<registry>/<name>:<tag>` reference, layer titles taken from the files' base names via `cmd.Dir`, and `oras tag … latest`. It also matches the operator's pull contract in `operator/internal/oci/bundle.go:10-34`.
- `CalculateAutoMemory` (`memory.go:24`) uses the same formula as the operator's `autoMemoryValue` (`gameserver_config.go:225`): `bytes*percent/100/(1<<20)` with an `M` suffix. That matches invariant 3.
- Invariant 4 (no overwrite without `--overwrite`) holds: `scaffold.go:239-243`. A second `init` of the same name exits 1 with the documented message.
- Invariant 1 (offline) holds: nothing in scaffold, validate or preview opens a network connection. Only `PushOCI` execs `oras`.
- Scaffolded `steamcmd`, `java` and `generic` modules validate with 0 errors and 0 warnings, which matches SC-002.
- `module.yaml` checks follow `modules/.schema/module.schema.json`: the same key set, required list and `apiVersion` const.
- Archive entry names are checked for traversal (`packager.go:62-77`), and symlinks are refused (`packager.go:209-211`).
- Cosmetic: a YAML parse error is reported as "failed to parse YAML: failed to parse YAML: …" because `common.ParseYAMLNode` (`yaml_ast.go:16`) and the validator (`validator.go:105,134`) each add the prefix.
- Held candidates: 1 (see OD-019).

## Candidate findings

### C-gp-module-01: The validator requires `options:` for enum config fields, but the CRD field is `enum:`, so every CRD-valid enum field is an ERROR and the web builder refuses to export it

- **Location**: `gp-module/internal/validator/rules.go:324-340`
- **Category**: correctness
- **Suggested severity**: S3 (workaround: build the bundle outside the builder and use the upload path, which doesn't run this validator)
- **Observation / repro**:
  1. `rules.go:325` reads `common.FindNode(item, "options")` for `type: enum` and emits ERROR `invalid-config-type` "must specify a non-empty options list" when it is missing.
  2. The GameTemplate CRD's `ConfigField` has `Enum []string json:"enum,omitempty"` (`gametemplate_types.go:1081-1083`, line 1083). The generated CRD schema has an `enum` property and no `options` property (`operator/config/crd/gameplane.local_gametemplates.yaml`, `spec.configSchema.items.properties`). The operator validates against `f.Enum` (`gameserver_config.go:151-155`).
  3. `gp-module validate modules/terraria modules/7-days-to-die` gives 7 errors such as `ERROR [invalid-config-type] template.yaml:211:7: configSchema enum field "AUTOCREATE" must specify a non-empty options list`, for fields declared as `enum: ["1", "2", "3"]`. Across all shipped modules there are 12 such errors in 5 modules (see Method).
  4. The dashboard builder export (`api/internal/handlers/modules_builder.go:358-368`) returns 400 "module validation failed" whenever the report is not clean. A builder template with a CRD-correct enum field therefore can't be exported or installed. A template written with `options:` passes the validator, but `options` isn't a CRD field: the API server prunes it, `f.Enum` stays empty, and the operator rejects every value.
- **Expected**: `specs/done_010-easy-module-building/spec.md` FR-008: validate "against … `GameTemplate` CRD schemas". FR-012: validate "enum lists". The validator should check the CRD's `enum:` list (non-empty, default among the values).
- **Actual**: CRD-valid enum fields fail validation, and the only spelling that passes produces a broken template at runtime.

### C-gp-module-02: The validator accepts `type: boolean`, which the CRD rejects, and its error message leaves out `bool`, the only accepted spelling

- **Location**: `gp-module/internal/validator/rules.go:17-24` (`allowedConfigTypes` includes `"boolean"`), `rules.go:280` (message)
- **Category**: correctness
- **Suggested severity**: S3 (the module installs and then fails; workaround: use `bool`)
- **Observation / repro**:
  1. Scratch module with a configSchema entry `{name: HARDCORE, type: boolean, default: "true"}`: `gp-module validate` reports `OK (no findings)`.
  2. The CRD enum for `spec.configSchema[].type` is `["string","int","bool","enum","password"]` (`gametemplate_types.go:1073`, and the same in the generated CRD YAML). Applying the template gives an `Unsupported value: "boolean"` rejection from the API server.
  3. The error message for an invalid type says "allowed: string, int, enum, boolean, password" (`rules.go:280`). That steers authors to the spelling the CRD rejects and omits `bool`.
- **Expected**: FR-008 (validate against the CRD schema). The accepted set should equal the CRD enum. Note that FR-012 and `diagnostics-contract.md:22` list `boolean` themselves, so the spec text needs the same correction.
- **Actual**: `boolean` passes `gp-module validate` (and the builder export gate) and then fails at install.

### C-gp-module-03: Template validation doesn't check CRD-required port fields: a port with no `name` validates clean

- **Location**: `gp-module/internal/validator/rules.go:145-237` (`validatePorts` checks only `containerPort` and `protocol`), `gp-module/internal/validator/schema.go:219-354` (`validateTemplateSchema` checks only `apiVersion`, `kind`, `metadata.name`, `spec.game`, `spec.image`)
- **Category**: correctness
- **Suggested severity**: S4
- **Observation / repro**:
  1. Scratch module whose only port is `{containerPort: 7777, protocol: UDP}` with no `name`: `gp-module validate` reports `OK (no findings)`.
  2. The generated CRD marks `spec.ports[].name` as required (`required: ['containerPort', 'name']`), with `MinLength=1` in `gametemplate_types.go:852-854`. The API server rejects this template at install.
- **Expected**: `specs.md:11`: "Static offline checking of `module.yaml` metadata, CRD schemas, … port bounds/collisions". `spec.md` SC-003: "Offline validation detects 100% of schema violations". FR-008.
- **Actual**: A CRD-required field can be missing and the module still validates clean.

### C-gp-module-04: `preview` without `--version-id` ignores the template's default version, so it shows a different image/env than the operator would run

- **Location**: `gp-module/internal/preview/preview.go:97-127` (the version overlay runs only when `opts.VersionID != ""`; otherwise `selectedVer = "default"` and the base `spec.image`/`spec.env` are used)
- **Category**: correctness
- **Suggested severity**: S3 (workaround: pass `--version-id` explicitly)
- **Observation / repro**:
  1. The operator's `resolveVersion` (`gameserver_version.go:30-51`) picks, when `spec.version` is empty, "the entry marked default, else the first", and applies that version's image and env (`gameserver_controller.go` `buildGameContainer`, which appends `ver.Env`).
  2. `modules/minecraft-java/template.yaml` marks `1.21.4-paper` as `default: true`, with env `TYPE=PAPER`, `VERSION=1.21.4`.
  3. `gp-module preview modules/minecraft-java --json` reports `versionId: "default"`, and `effectiveEnv` has neither `TYPE` nor `VERSION`. With `--version-id 1.21.4-paper`, it shows `TYPE: PAPER`, `VERSION: 1.21.4`. For templates whose first or default version image differs from `spec.image`, the image is wrong too.
- **Expected**: `gp-module/specs.md:12`: "Simulation of runtime server creation from `template.yaml` … environment variable precedence". A preview with no version choice should mirror what the operator does with no version choice.
- **Actual**: The default preview omits the version layer that every real server gets.

### C-gp-module-05: `preview` ignores the memory limit the template declares and always simulates 4Gi, even for a template with no limit

- **Location**: `gp-module/internal/preview/preview.go:92-95`; `gp-module/cmd/gp-module/preview.go:44` (`--memory` default `"4Gi"`)
- **Category**: correctness
- **Suggested severity**: S3 (workaround: pass `--memory` matching the template)
- **Observation / repro**:
  1. The operator computes `autoFromMemoryLimit` from `effectiveResources(gs, tmpl).Limits.Memory()`, meaning the GameServer's resources if set, else the template's `spec.resources` (`gameserver_controller.go:1859-1866`, `gameserver_config.go:217-230`). With no memory limit it returns `""` and leaves the field unset (`gameserver_config.go:222-224`; the CRD doc at `gametemplate_types.go:1103-1104` says "a template without a memory limit leaves the field unset").
  2. Preview never reads `spec.resources`. It uses `opts.MemoryLimit`, defaulting to 4Gi.
  3. Concrete case: `gp-module init my-java --archetype java -y` scaffolds `MAX_MEMORY` with `autoFromMemoryLimit: {percent: 75}` and no `spec.resources` (the scaffold never writes one). `gp-module preview modules/my-java` reports `MAX_MEMORY: 3072M`. The operator leaves `MAX_MEMORY` unset unless the GameServer supplies resources. For a template declaring `limits.memory: 8Gi`, preview shows 3072M where the operator gives 6144M. The only shipped user, `minecraft-java`, declares 4Gi, so today the default hides the gap.
- **Expected**: `specs.md:21`: "Replicate Gameplane operator runtime logic for memory percentage calculations". `specs.md:12`: "evaluating dynamic memory limits". With no `--memory`, the preview should use the template's `spec.resources.limits.memory`, or report the field unset when there is none.
- **Actual**: A fixed 4Gi is used unless `--memory` is passed.

### C-gp-module-06: The validator requires `metadata.name` in template.yaml; `docs/module-authoring.md` tells authors to omit it and the operator doesn't need it

- **Location**: `gp-module/internal/validator/schema.go:294-309`
- **Category**: docs-drift / correctness
- **Suggested severity**: S4
- **Observation / repro**:
  1. `schema.go:294-309` emits ERROR `template-schema-violation` "metadata.name is required in template.yaml" when it is absent.
  2. `docs/module-authoring.md:139-141`: "`template.yaml` is the same `GameTemplate` you would write today, with one difference: omit `metadata.name`. The name is set on install from `module.yaml#name`". `docs/module-authoring.md:102`: "template.yaml   # GameTemplate spec (no metadata.name)". The operator's bundle loader documents the template as "without metadata.name set" (`operator/internal/modsrc/bundle.go:58-61`) and needs only `module.yaml#name`.
  3. A module written to the docs fails `gp-module validate` and the builder export gate (`modules_builder.go:365-368`). All 30 shipped templates do carry `metadata.name`, so which rule is authoritative has to be decided.
- **Expected**: The validator and the authoring guide agree.
- **Actual**: They contradict each other.

### C-gp-module-07: The `docs/module-authoring.md` quickstart commands fail as written

- **Location**: `docs/module-authoring.md:33`, `:43`, `:58`, `:73`; `Makefile:326-333`; `gp-module/cmd/gp-module/init.go:279-306`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**:
  1. `docs/module-authoring.md:29-34` passes `--ports="game:27015/udp:adv"`. `parsePortDef` splits on `/` and runs `Atoi` on `"game:27015"`. Running the documented command in a scratch dir exits 1 with `error: invalid port "game:27015/udp:adv": invalid port number "game:27015"`. The only accepted forms are `PORT` and `PORT/PROTOCOL` (`init.go:279-298`, and `cli-contract.md:58`).
  2. `docs/module-authoring.md:43`: `make module-validate MODULE=modules/cs2-match`. The target already adds the prefix (`Makefile:327`: `validate $(if $(MODULE),modules/$(MODULE))`), and `make -n` shows `bin/gp-module validate modules/modules/cs2-match`. The same doubled prefix affects `:58` (`module-preview MODULE=modules/minecraft-java`, `Makefile:330`) and `:73` (`module-package MODULE=modules/cs2-match`, `Makefile:333`).
- **Expected**: Documented commands run.
- **Actual**: The `init` example exits 1, and the three `make` examples point at a directory that doesn't exist.

### C-gp-module-08: The image-unpinned remediation tells authors to run `gp-module pin`, which doesn't exist

- **Location**: `gp-module/internal/validator/rules.go:139`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**:
  1. Remediation text: "Pin image digest using 'gp-module pin' / 'make module-pin' or append '# gameplane:floating' if intentionally dynamic."
  2. `main.go:40-59` has no `pin` command, so `gp-module pin` prints `error: unknown command "pin"` and exits 2. `make module-pin` exists (`Makefile:304-311`) but runs `modules/validate.py --pin` over every module.
  3. `diagnostics-contract.md:17` gives different advice: "Resolve image digest using `crane digest <image>` or `docker inspect` and append `@sha256:...`".
- **Expected**: The remediation names a command that exists and matches the diagnostics contract.
- **Actual**: It names a nonexistent subcommand.

### C-gp-module-09: The CLI differs from `cli-contract.md`: preview has no `--format`, the validate summary line has a different format, and `init` prompts even without a TTY

- **Location**: `gp-module/cmd/gp-module/preview.go:43-47`; `gp-module/internal/validator/report.go:134-136`; `gp-module/cmd/gp-module/init.go:92-96,116-230`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**:
  1. `cli-contract.md:121`: "`--format <type>` | Enum (`yaml`, `json`, `text`) | `text`". The code defines only `--json` (`preview.go:47`). `gp-module preview modules/minecraft-java --format json` exits 2 ("flag provided but not defined"). No YAML output exists. (`docs/module-authoring.md:64` uses `--json`, which works.)
  2. `cli-contract.md:99`: "SUMMARY: 0 clean, 0 warn-only, 1 with errors (of 1 scanned)." The code prints "SUMMARY: %d module(s) checked, %d error(s), %d warning(s)." (`report.go:135`). Finding lines also differ: contract `ERROR [template.yaml:45] [invalid-port-number] …` + `-> Remediation:` vs code `ERROR [invalid-port-number] template.yaml:45:7: …` + `Remediation:`.
  3. `cli-contract.md:66`: "In interactive mode (default when attached to a TTY and missing arguments)". The code prompts whenever `--non-interactive` is not set, whether or not stdin is a TTY and whether or not the field was supplied elsewhere (`init.go:92,116,153`). For example, `gp-module init x --archetype steamcmd … </dev/null` still prints the summary/storage prompts and falls back on EOF.
- **Expected**: The CLI matches its contract, or the contract is updated.
- **Actual**: As listed above.

### C-gp-module-10: `gp-module/specs.md` lists a dependency and three source files that don't exist, and the pkg doc gives the wrong icon size

- **Location**: `gp-module/specs.md:5`, `gp-module/specs.md:52-55`; `gp-module/pkg/archetypes/archetypes.go:22`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**:
  1. `specs.md:5`: "**Dependencies:** `gopkg.in/yaml.v3`, `k8s.io/apimachinery`, `github.com/ValgulNecron/gameplane/operator/api/v1alpha1`". `go.mod` doesn't require the operator module, and no gp-module file imports it (grep for `operator/api` in `gp-module/` finds nothing). Only `k8s.io/apimachinery/pkg/api/resource` is used (`memory.go:8`). Every CRD rule is hand-written, which is how C-gp-module-01/02/03 could drift from the CRD.
  2. `specs.md:53-55` lists `internal/archetypes/steamcmd.go`, `java.go`, `generic.go`. They don't exist: all three presets are in `internal/archetypes/archetypes.go:117-220`.
  3. `pkg/archetypes/archetypes.go:22`: "PlaceholderIconBytes returns default 128x128 PNG icon bytes." The implementation (`internal/archetypes/archetypes.go:82-85`) and `archetypes-contract.md:230` both say 256x256.
- **Expected**: The spec's dependency list and layout match the module.
- **Actual**: As listed above.

### C-gp-module-11: The `--offline` flag and `ValidateOptions.Offline` do nothing

- **Location**: `gp-module/cmd/gp-module/validate.go:25,36,71`; `gp-module/internal/validator/validator.go:15`
- **Category**: dead-code
- **Suggested severity**: S4
- **Observation / repro**:
  1. `grep -rn Offline gp-module --include=*.go` (non-test) finds only the field declaration, the flag definition and the assignment. No code reads `opts.Offline`.
  2. `gp-module validate --offline=false` behaves exactly like the default. The help text says "Enforce pure offline validation without contacting registries (default: true)", which implies that `false` enables registry checks. There are none.
- **Expected**: The flag either controls behaviour or is removed. `cli-contract.md:85` also lists it.
- **Actual**: It is a no-op, exported through `pkg/validator` and set by the API builder (`modules_builder.go:198-201,358-361`).

### C-gp-module-12: A `gameplaneMinVersion` newer than the tooling is not reported

- **Location**: `gp-module/internal/validator/schema.go:201-214`
- **Category**: correctness
- **Suggested severity**: S4
- **Observation / repro**:
  1. `module.yaml` with `gameplaneMinVersion: 9.0.0` validates clean. The rule checks only that the value is semver.
  2. There is also no Gameplane version for it to compare against: the CLI version is a hard-coded `"1.0.0"` (`main.go:9`), unrelated to the Gameplane release line.
- **Expected**: `specs/done_010-easy-module-building/spec.md:102` (Edge Cases): "If a module references a `gameplaneMinVersion` higher than the current tooling version, validation must notify the author of potential version discrepancies."
- **Actual**: No notification.

### C-gp-module-13: `gp-module/go.sum` is missing `/go.mod` hashes, so a normal build dirties the tree and a standalone read-only build fails

- **Location**: `gp-module/go.sum` (whole file); `Makefile:319-321` (the `gp-module` target runs `cd gp-module && go build …`)
- **Category**: correctness (build hygiene)
- **Suggested severity**: S4
- **Observation / repro**:
  1. From a clean tree, `cd gp-module && go build ./cmd/gp-module` (workspace mode, which is what `make gp-module` does) adds four lines to the tracked `gp-module/go.sum`: the `/go.mod h1:` hashes for `github.com/fxamacker/cbor/v2 v2.9.2`, `github.com/x448/float16 v0.8.4`, `gopkg.in/inf.v0 v0.9.1` and `sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730`. After that, `git status` shows `M gp-module/go.sum`.
  2. With the file restored, `cd gp-module && GOWORK=off GOFLAGS=-mod=readonly go list ./...` exits 1: `gopkg.in/inf.v0@v0.9.1: missing go.sum entry for go.mod file` (and the same for `fxamacker/cbor/v2` and `sigs.k8s.io/json`). All of these are pulled in by `k8s.io/apimachinery`.
- **Expected**: The committed `go.sum` is complete (`go mod tidy`-clean), so builds don't change tracked files and the module resolves outside the workspace.
- **Actual**: Every local `make gp-module` / `make module-*` run leaves `gp-module/go.sum` modified, and a standalone read-only resolution fails. I didn't check whether CI builds this module outside the workspace.

## Questions (not findings)

- `left-4-dead-2` declares `27015/UDP` twice (`gp-module validate` reports `duplicate-port-collision` at `template.yaml:51`, first declared at `:47`). That is a `modules/` question. Is it intentional (for example a query port sharing the game port), and does the operator's Service then get duplicate port/protocol entries?
- `schema.go:253` also accepts `apiVersion: gameplane.io/v1alpha1`, although the CRD group is `gameplane.local` and the error text says it "must be gameplane.local/v1alpha1". Is that a deliberate legacy alias?
- Preview and operator treat an explicitly empty user value differently. The operator: key set to `""` means the default is not applied, `autoFromMemoryLimit` is tried, else the field is skipped (`gameserver_config.go:117-129`). Preview: `""` falls back to the default (`preview.go:182-187`). The CLI can produce this with `--config KEY=`. Worth aligning?
- `preview.go:136,148` formats template env values with `fmt.Sprintf("%v", eMap["value"])`, so an env entry using `valueFrom` (allowed by the CRD's `corev1.EnvVar`) would show `<nil>`. No shipped module uses `valueFrom` today.
- Interactive `init` replaces the archetype's full port list with the one port the user types, named `port-<n>`, so the java preset loses its `rcon` port and `steamcmd` loses `query` (`init.go:166-183,232-241`). Is that the intended UX?
- `gp-module package --registry` pushes unsigned bundles. `modules/build.sh --sign` signs with cosign. If a ModuleSource requires signatures, gp-module-pushed bundles won't verify. Should `package` offer signing, or should the docs say this?
