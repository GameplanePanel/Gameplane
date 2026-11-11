---
name: deps-check
description: "Identify which published peer dependency blocks a proposed npm upgrade."
---

## Steps

1. Read the failed install or `npm ls` output. Identify the proposed version and the package declaring the conflicting peer range.
2. Use `npm view <package>@<version> peerDependencies` for the locked version and `npm view <package>@latest version peerDependencies` for the current release.
3. Report which release admits the upgrade, or which range still blocks it. Preserve npm's peer checks and keep blocked PRs open.
