# Contract: `hack/gen-devcontainers.py`

```
python3 hack/gen-devcontainers.py [--check] [--submodules] [--affected PATH...]
```

| Mode | Effect | Exit |
|---|---|---|
| (none) | Writes every generated file (data-model "GeneratedProfile", ToolIndex). | 0, or 1 on invalid input |
| `--submodules` | Also writes `website/.devcontainer/*` and `modules/.devcontainer/*`. | same |
| `--check` | Generates in memory and diffs against disk (including submodule copies when those submodules are checked out). Prints each stale file and each rule broken. | 0 clean, 1 drift or invalid |
| `--affected PATH...` | Prints the profile ids affected by the given changed paths, one per line (used by the CI matrix). A change to `versions.toml`, `profiles.toml`, `setup.sh`, the source lock or the generator itself prints every id. | 0 |

Errors the check reports (each names the file and profile):
- a `system-deps.toml` rule from [system-deps-format](system-deps-format.md);
- a covered folder without `system-deps.toml` (FR-004a);
- a `go.work` module in no component profile (FR-014);
- a `versions.toml` key nobody uses;
- a generated file edited by hand (diff);
- a literal version anywhere in `profiles.toml` (FR-007);
- the `Makefile` pins `ENVTEST_K8S_VERSION` and the `setup-envtest@` ref differ from `versions.toml` (single source; R10).

Make targets: `make devcontainers` (generate, with `--submodules`), `make check-devcontainers` (`--check`, part of `make lint`).

Output is deterministic: sorted keys, tab-indented JSON like today's file, and a trailing newline.
