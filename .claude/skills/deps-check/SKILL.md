---
name: deps-check
description: "Use at session start, or when a dependency upgrade is proposed, to check the blocked dependency upgrades listed in CLAUDE.md (TypeScript 7 and ESLint 10) against the npm registry."
---

Run the session-start check in [AGENTS.md](../../../AGENTS.md) every session. This check only reads the registry.

## Steps

1. Run the registry queries in the blocked dependency upgrades section of `AGENTS.md`.
2. For TypeScript (#272) and the paired ESLint engine and preset (#857 and #628), check whether each published peer range accepts the proposed version. Report TypeScript results for both the latest and canary tags.
3. Report each upgrade on one line with its status, package versions and peer ranges. Identify the package that blocks the upgrade, if any.
4. If an upgrade is unblocked, list its migration steps from `AGENTS.md`. Perform them only when the maintainer asks.
5. Keep the tracked PRs open. Never use `--legacy-peer-deps` or overrides to bypass a peer range.
