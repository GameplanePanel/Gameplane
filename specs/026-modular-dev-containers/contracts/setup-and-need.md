# Contract: `.devcontainer/setup.sh`, `setup.env`, `ready.txt`, `hack/need.sh`

## setup.sh

`bash .devcontainer/setup.sh <path to setup.env>` (the profile's `postCreateCommand`).

- Sources `setup.env`: `STEPS="submodules kind oras …"`, one `<TOOL>_VERSION=` line per versioned step, `GO_MODULES="api netguard …"`, `SUBMODULES="modules"`, `READY=<path to ready.txt>`.
- Runs each listed step's function in a fixed order. Each step is idempotent (`command -v` or version guard, as in today's `post-create.sh`).
- Required steps fail the setup on error. Steps marked optional in the catalogue (`ai_clis`) log `WARN:` and continue (FR-011).
- Ends by printing tool versions for the installed steps only, then `cat "$READY"`.
- Never reads or writes credentials (FR-012).

## ready.txt (generated)

```
[devcontainer] ready: Gameplane · <title>

Works here:
  <check commands>
  <make targets whose need.sh tools are all installed>

Ports: <port  label> …
Tests and lint run in CI (CLAUDE.md rule 8).
Need something else? <tool> → profile "<id>" (one line per profile that adds tools)
```

## hack/need.sh

`hack/need.sh <tool>...`: exits 0 when every tool is on `PATH`. Otherwise, for each missing tool it prints to stderr:

```
need: kind is not installed in this dev container.
      Open the "control-plane" or "dashboard-api" profile (declared in deploy/kind/system-deps.toml).
```

and exits 127. It falls back to a plain "not installed" message when `.devcontainer/tool-index.tsv` is absent, for example in a non-container checkout.
