# Contract: `system-deps.toml`

Location: the component's own folder (`api/system-deps.toml`, `web/system-deps.toml`, `deploy/kind/system-deps.toml`, `website/system-deps.toml`, `modules/system-deps.toml`, …).

```toml
# System dependencies for the API (spec 026). Language libraries stay in go.mod.
component = "api"

[[dep]]
name = "go"
kind = "toolchain"
version = "go"            # key in .devcontainer/versions.toml
reason = "Builds the API module (go.work member)."

[[dep]]
name = "envtest"
kind = "service"
version = "envtest_k8s"
reason = "Kubernetes API server + etcd binaries for the envtest suites."
```

A component with nothing beyond the baseline:

```toml
component = "docs-only-folder"   # hypothetical
none = true   # nothing beyond the baseline
```

Rules (checked by `gen-devcontainers.py --check`):
1. `component` equals the folder path relative to the repo root.
2. Every `version` is a key in `versions.toml`. Literal versions fail.
3. `kind` is one of `toolchain | cli | os-package | service | submodule`.
4. `reason` is not empty.
5. `none = true` and `[[dep]]` entries are mutually exclusive.
6. Unknown keys fail, so typos cannot slip through.
