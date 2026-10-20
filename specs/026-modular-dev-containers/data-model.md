# Data Model: Modular Dev Containers

All entities are files in git. "Generated" files are written only by `hack/gen-devcontainers.py`.

## VersionSource (`.devcontainer/versions.toml`, hand-written, one file)

| Field | Type | Rule |
|---|---|---|
| `[tools].<key>` | string | One entry per tool, e.g. `go = "1.26"`, `kind = "v0.32.0"`. Each key is used by at least one dependency or the baseline (the generator fails on an unused key). |

## ComponentDeps (`<dir>/system-deps.toml`, hand-written, one per component folder)

| Field | Type | Rule |
|---|---|---|
| `component` | string | Folder id (e.g. `api`, `web`, `deploy/kind`). Must match the file's folder. |
| `none` | bool | `true` means the folder needs nothing beyond the baseline. It must then have no `[[dep]]` entries (spec edge case). |
| `[[dep]].name` | string | Tool id, unique within the file. Matches a `setup.sh` step or a feature in the source lock. |
| `[[dep]].kind` | enum | `toolchain`, `cli`, `os-package`, `service` or `submodule`. |
| `[[dep]].version` | string | A `versions.toml` key. A literal version is rejected (FR-007). Optional only for `os-package` taken from the base image. |
| `[[dep]].reason` | string | One line, not empty. |

Validation: every folder named by a profile has this file (FR-004a). Every Go module in `go.work` is listed by some component profile (FR-014).

## Profile (catalogue entry in `.devcontainer/profiles.toml`, hand-written)

| Field | Type | Rule |
|---|---|---|
| `[profile.<id>].size` | enum | `component`, `part` or `full`. Exactly one `full`. |
| `title` | string | Shown in the editor's picker as `Gameplane · <title>`. |
| `dirs` | list | Component folders (component size) |
| `includes` | list | Profile ids (part size). Each must be a component profile. |
| `extras` | list of dep tables | Part-level additions, such as docker-in-docker for a cluster. Same shape as `[[dep]]`. |
| `submodules` | list | Submodules setup initialises (`website`, `modules`). |
| `ports` | list | `{port, label, onAutoForward}`. |
| `extensions` | list | Editor extension ids, beyond the baseline. |
| `checks` | list | Compile-check commands run by CI and printed in the ready message. |
| `env` | table | Extra `containerEnv`. |

`full` has no `dirs`: it is the union of every profile plus `[full].personal` (themes and other extensions only the full profile carries, per the spec assumption).

## Baseline (`[baseline]` in `profiles.toml`)

`deps` (same shape as `[[dep]]`), `extensions`, `settings` (editor settings), `env` (`GOTOOLCHAIN`, `CI`) and `remoteUser`. Merged into every profile (FR-006).

## GeneratedProfile (output, per profile)

`devcontainer.json`, `devcontainer-lock.json`, `setup.env`, `ready.txt`. The full profile's JSON is `.devcontainer/devcontainer.json`, and its other files go in `.devcontainer/full/`.

Invariant (FR-004b): the set of deps in a generated profile equals the baseline deps, plus the deps of its `dirs` (or of its `includes`' dirs), plus its `extras`. Duplicate names collapse to one entry.

## ToolIndex (`.devcontainer/tool-index.tsv`, generated)

`<tool>\t<profile ids, smallest first>\t<declaring dir>`. Read by `hack/need.sh`.
