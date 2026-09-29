# CLI Interface Contract: `gp-module`

**Branch**: `010-easy-module-building` | **Date**: 2026-08-27 | **Spec**: [spec.md](../spec.md)

This contract specifies the command-line interface, subcommands, arguments, flags, output formats, and exit codes for the Gameplane Module Toolkit (`gp-module`).

---

## 1. Overview & Invocation

The toolkit is compiled as a Go binary into `bin/gp-module` and can be invoked directly, through `go run`, or via top-level Makefile convenience targets:

```sh
# Direct binary invocation
bin/gp-module <subcommand> [options] [args]

# go run invocation
go run ./gp-module/cmd/gp-module <subcommand> [options] [args]

# Makefile convenience targets
make module-new NAME=<name> [ARCHETYPE=<archetype>]
make module-validate [MODULE=<name>]
make module-preview MODULE=<name> [VERSION=<id>] [MEMORY=<limit>]
make module-package MODULE=<name> [REGISTRY=<ref>]
```

---

## 2. Standard Exit Codes

| Exit Code | Meaning |
| :---: | :--- |
| `0` | Success: Command completed with zero blocking errors. |
| `1` | Failure: One or more validation errors, schema violations, or operation failures detected. |
| `2` | Usage Error: Invalid or unrecognized flags, missing required positional arguments. |

---

## 3. Subcommands

### 3.1 `init` (Scaffold New Module)

Creates a new, schema-compliant module directory with `module.yaml`, `template.yaml`, `README.md`, and default placeholder `icon.png`.

```text
Usage: gp-module init [NAME] [options]
```

#### Arguments & Flags:

| Argument / Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `NAME` (positional) | String | None | Module slug (e.g. `my-game`). Must be 1-63 characters and conform to DNS-1123 label regex: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| `--name <str>` | String | `NAME` | Alternate flag for module name. |
| `--display-name <str>`| String | Titleized `NAME` | Human-readable title (e.g. "My Game"). |
| `--archetype <type>` | Enum | `generic` | Starter archetype preset: `steamcmd`, `java`, or `generic`. |
| `--image <ref>` | String | Archetype default | Default container image (prompted or archetype preset). |
| `--port <port/proto>` | String (repeatable) | Archetype default | Primary game ports (e.g. `7777/udp`, `25565/tcp`). Can be specified multiple times. |
| `--category <str>` | String (repeatable) | Archetype default | Category taxonomy (e.g. `Survival`, `Sandbox`). Can be specified multiple times. |
| `--summary <str>` | String | None | One-line summary for catalog card. |
| `--output-dir <path>` | Path | `modules/<name>` | Target directory for generated module files. |
| `--non-interactive`, `-y` | Flag | `False` | Run in non-interactive mode using flags and archetype defaults. |
| `--overwrite`, `-f` | Flag | `False` | Force overwriting existing directory. |

#### Behavior:
- In interactive mode (default unless `--non-interactive`/`-y` is set), prompts the user for missing fields with sensible defaults shown in brackets.
- There is no TTY detection: `init` will block on `os.Stdin` even when not attached to a terminal, so scripted/CI invocations must pass `--non-interactive`/`-y` explicitly.
- Validates the module name against DNS-1123 label constraints before creating directories.
- If target directory exists and `--overwrite` is not set, exits with code 1 and a non-destructive error message.

---

### 3.2 `validate` (Offline Validation & Linting)

Performs static validation of module manifests against CRD and metadata schemas without requiring a live cluster or internet access.

```text
Usage: gp-module validate [MODULE_PATH...] [options]
```

#### Arguments & Flags:

| Argument / Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `MODULE_PATH...` | Paths | All `modules/*` dirs | One or more module paths or directory names to validate. If omitted, discovers all module directories. |
| `--offline` | Flag | `True` | Enforce pure offline validation without contacting container registries. |
| `--strict` | Flag | `False` | Treat warnings as blocking errors (fail with code 1 if warnings exist). |
| `--json` | Flag | `False` | Output results in machine-readable JSON format conforming to the diagnostics schema. |
| `--rules <rule,...>` | String | All | Comma-separated list of specific rules to evaluate. |

#### Output Format (Human-Readable):
```text
== modules/my-game ==
  ERROR [invalid-port-number] template.yaml:45:7: port 70000 exceeds maximum allowable port 65535.
        Remediation: Change containerPort to a valid port number between 1 and 65535.
  WARN  [custom-category] module.yaml:7: category 'MyCustomCat' is not in canonical catalog taxonomy.
        Remediation: Choose from [Survival, Sandbox, Shooter, Simulation, Building, Adventure, Horror, Co-op, PvP, Modded, Creative].

---
SUMMARY: 1 module(s) checked, 1 error(s), 1 warning(s).
```

---

### 3.3 `preview` (Dry-Run Manifest Rendering)

Evaluates a module's `template.yaml` against test configuration inputs, simulating the Gameplane operator's config materialization and environment variable synthesis using native Kubernetes types.

```text
Usage: gp-module preview <MODULE_PATH> [options]
```

#### Arguments & Flags:

| Argument / Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `MODULE_PATH` | Path | Required | Path to the module directory to preview. |
| `--version-id <id>` | String | Default image | Select a specific version entry from `spec.versions`. |
| `--memory <qty>` | String | `4Gi` | Simulated container memory limit for computing `autoFromMemoryLimit` fields (e.g. `4Gi`, `2048Mi`). |
| `--config <key=val>` | String (repeatable) | Empty | Provide custom configuration values to simulate server creation. |
| `--config-file <path>`| Path | None | Path to a YAML/JSON file of key-value configuration values. |
| `--json` | Flag | `False` | Output the preview as machine-readable JSON instead of the default human-readable text. |

#### Output Content:
- Effective container image (resolved version image or fallback default).
- Resolved environment variables with origin annotations (`[template]`, `[version]`, `[configSchema]`).
- Computed dynamic values (e.g. JVM memory calculated via `autoFromMemoryLimit: {percent: 75}` with 4Gi limit $\to$ `3072M`).
- Configured port declarations and volume mounts.

---

### 3.4 `package` (OCI Artifact Packaging)

Validates module assets and bundles the directory into an OCI-compliant artifact.

```text
Usage: gp-module package [MODULE_PATH] [options]
```

#### Arguments & Flags:

| Argument / Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `MODULE_PATH` | Path | `.` (current directory) | Path to module directory. |
| `--output <path>` | Path | None | Build a local `.tar.gz` OCI bundle archive instead of pushing. |
| `--registry <url>` | String | None | Registry destination (e.g. `localhost:5001` or `ghcr.io/valgulnecron/gameplane-modules`). |
| `--tag <str>` | String | `module.yaml#version` | OCI tag to push. |
| `--tag-latest` | Flag | `False` | Also push the `:latest` tag. |
| `--plain-http` | Flag | `False` | Allow plain HTTP registry connections (e.g. local Kind). |
| `--insecure` | Flag | `False` | Skip TLS verification. |
| `--max-icon-size <bytes>`| Integer | 524288 (512 KiB) | Maximum icon size warning threshold. |
| `--max-bundle-size <bytes>`| Integer| 1048576 (1 MiB) | Maximum bundle size warning threshold. |

#### Behavior:
- Inspects all bundle files before packaging.
- Validates file sizes against limits and warns on excessive size.
- Verifies that all required layers (`module.yaml`, `template.yaml`, `README.md`) are present.
- Executes `oras push` with canonical media types when `--registry` is provided, or packages a `.tar.gz` bundle archive when `--output` is specified.
