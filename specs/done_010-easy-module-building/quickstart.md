# Quickstart & Validation Guide: Easy Module Building Toolkit

**Branch**: `010-easy-module-building` | **Date**: 2026-08-27 | **Spec**: [spec.md](spec.md)

This guide provides runnable scenarios to validate the **Easy Module Building & Authoring Toolkit** (`gp-module`) end-to-end.

---

## Prerequisites

- **Go**: 1.26.0 installed
- **ORAS**: $\ge$ 1.2.0 installed (required only for packaging & pushing)
- **Repository Setup**:
  ```sh
  git checkout 010-easy-module-building
  git submodule update --init modules
  make build-go # or: go build -o bin/gp-module ./gp-module/cmd/gp-module
  ```

---

## Scenario 1: Guided Interactive Module Scaffolding

Prove that an author can interactively scaffold a new module directory without memorizing schema formats.

### Command:
```sh
bin/gp-module init my-steam-game
# or: make module-new NAME=my-steam-game
```

### Interactive Prompts & Inputs:
1. **Display Name**: `My Steam Game`
2. **Archetype**: Select `[1] steamcmd`
3. **Container Image**: Press Enter to accept archetype default or enter custom image
4. **Game Port**: Press Enter to accept `27015/udp`
5. **Categories**: Press Enter to accept `[Survival, Co-op]`
6. **Summary**: `Dedicated server for My Steam Game`
7. **Storage Size**: Press Enter to accept `20Gi` (steamcmd archetype default)
8. **Storage Mount Path**: Press Enter to accept `/serverdata` (steamcmd archetype default)

### Expected Outcome:
- Directory `modules/my-steam-game/` is created containing:
  - `module.yaml`: Conforming to [ModuleMetadata](data-model.md#22-module-metadata-modulemetadata)
  - `template.yaml`: Conforming to [steamcmd archetype](contracts/archetypes-contract.md#21-archetype-steamcmd), including:
    - `spec.storage.size: 20Gi`
    - `spec.storage.mountPath: /serverdata`
  - `README.md`: Containing getting-started instructions with storage documentation
  - `icon.png`: Valid 256x256 placeholder PNG
- Command exits with code `0`.

---

## Scenario 2: Automated Non-Interactive Scaffolding

Prove that CI pipelines or power users can generate module directories non-interactively using CLI flags.

### Command:
```sh
bin/gp-module init valheim-custom \
  --display-name "Valheim Custom" \
  --archetype steamcmd \
  --image "ghcr.io/valgulnecron/valheim-server:latest@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
  --port 2456/udp \
  --port 2457/udp \
  --category Survival \
  --summary "Custom Valheim Dedicated Server" \
  --non-interactive
```

### Expected Outcome:
- Directory `modules/valheim-custom/` is created without any interactive prompts.
- All supplied ports (`2456/udp` and `2457/udp`) are declared in `template.yaml`.
- Command exits with code `0`.

---

## Scenario 3: Offline Validation & Diagnostic Feedback

Prove that offline validation catches common authoring errors with precise line numbers and actionable remediation.

### 1. Test Valid Module (Baseline Clean Pass):
```sh
bin/gp-module validate modules/valheim-custom
# or: make module-validate MODULE=valheim-custom
```
**Expected Outcome**:
```text
== modules/valheim-custom ==
  OK (no findings)

---
SUMMARY: 1 clean, 0 warn-only, 0 with errors (of 1 scanned).
```
Exits with code `0`.

### 2. Introduce Test Errors into `modules/valheim-custom/template.yaml`:
- Change a port to `70000` (exceeds 65535).
- Add an unpinned image tag without digest: `spec.image: mygame:latest`.
- Declare a credential field `spec.configSchema[0].name: RCON_PASSWORD` with `type: string`.

### 3. Run Validation:
```sh
bin/gp-module validate modules/valheim-custom
```
**Expected Outcome**:
```text
== modules/valheim-custom ==
  ERROR [template.yaml:15] [image-unpinned] default image 'mygame:latest' is not pinned with a digest.
    -> Remediation: Pin the image with @sha256:... or append '# gameplane:floating' if intentional.
  ERROR [template.yaml:18] [invalid-port-number] port 70000 exceeds maximum allowable port 65535.
    -> Remediation: Change containerPort to an integer between 1 and 65535.
  ERROR [template.yaml:32] [credential-field-not-password] config field 'RCON_PASSWORD' looks credential-shaped but type='string'.
    -> Remediation: Set type: password so operator stores this value securely in a Secret.

---
SUMMARY: 0 clean, 0 warn-only, 1 with errors (of 1 scanned).
```
Command exits with code `1`.

---

## Scenario 4: Dry-Run Manifest Rendering & `autoFromMemoryLimit` Preview

Prove that the preview engine accurately resolves environment variables and computes memory limits without deploying to a live Kubernetes cluster.

### Command:
```sh
bin/gp-module preview modules/minecraft-java --memory 4Gi
# or: make module-preview MODULE=minecraft-java MEMORY=4Gi
```

### Expected Outcome:
The tool displays the simulated runtime configuration:
```text
== Preview: minecraft-java ==
Resolved Image: itzg/minecraft-server:java21@sha256:f71555873bdaa6f4b663563de58678cca334bc51bdfbe548f7958ee9f516cf4a

Computed Configuration Values:
  MAX_MEMORY = 3072M  (derived from 75% of 4Gi memory limit)

Effective Environment Variables:
  EULA = TRUE [source: template]
  ENABLE_RCON = true [source: template]
  RCON_PORT = 25575 [source: template]
  MAX_MEMORY = 3072M [source: configSchema]

Ports:
  25565/TCP (game, advertise=True, wake=minecraft)
  25575/TCP (rcon, advertise=False)
```

---

## Scenario 5: Packaging & Integrity Verification

Prove that the packager enforces asset size constraints and packages the OCI bundle.

### Command:
```sh
bin/gp-module package modules/valheim-custom --output /tmp/valheim-custom.tar.gz
# or: make module-package MODULE=valheim-custom
```

### Expected Outcome:
- Checks asset sizes (`icon.png` $\le$ 512 KiB, total $\le$ 1 MiB).
- Validates bundle structure.
- Creates `/tmp/valheim-custom.tar.gz` archive.
- Command exits with code `0`.

---

## Scenario 6: End-to-End Cluster Lifecycle Test

Run the full end-to-end verification test in the local development cluster or CI.

### Command:
```sh
# Run the focused E2E test via canonical make target:
make test-e2e BUCKET=operator TEST_ARGS='-run ^TestModule_ScaffoldAndPackage$'

# Or against an already-running E2E cluster:
make test-e2e-bucket BUCKET=operator TEST_ARGS='-run ^TestModule_ScaffoldAndPackage$'
```

### Expected Outcome:
1. Test invokes `gp-module init` to generate a fresh module.
2. Test executes `gp-module validate` asserting 0 errors.
3. Test executes `gp-module package --registry localhost:5001` pushing to test registry.
4. Test applies `ModuleSource` and `Module` CRs.
5. Operator reconciles and materializes the corresponding in-cluster `GameTemplate`.
6. Test passes with exit code `0`.

---

## Scenario 7: Web Dashboard Module Builder Verification

Prove that a user can author, validate, preview, and install a module directly through the web interface.

### Steps:
1. Navigate to `http://localhost:5173/modules` (or dashboard URL) and sign in.
2. Click the **"Create module"** button next to "Upload module".
3. Select the **SteamCMD** archetype preset.
4. Fill in:
   - Module Name: `palworld-community`
   - Display Title: `Palworld Community Edition`
   - Image: `jammsen/palworld-dedicated-server:latest@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
   - Port: `8211/udp`
5. Observe the **Live Preview & Validation** pane:
   - Validation displays a green "Valid Module Manifest" badge.
   - Adjusting memory slider dynamically shows calculated `autoFromMemoryLimit` values.
   - Code viewer displays generated `module.yaml` and `template.yaml`.
6. Click **"Download Bundle"**:
   - Browser receives `palworld-community.tar.gz` bundle containing `module.yaml`, `template.yaml`, `README.md`, and `icon.png`.
7. Click **"Install to Cluster"**:
   - Bundle uploads to the active `upload`-type ModuleSource ConfigMap.
   - The operator discovers and indexes the module bundle into the cluster catalog, making it available for installation as a `Module` CR.
