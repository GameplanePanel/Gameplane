---
name: deps-check
description: "Use at session start, or when a dependency upgrade is proposed, to check the blocked dependency upgrades listed in CLAUDE.md (TypeScript 7 and ESLint 10) against the npm registry."
---

This is the session-start check from `CLAUDE.md`. Run it every session. It only reads the registry. It changes nothing.

## Steps

1. Run the registry checks:
   ```sh
   npm view @typescript-eslint/parser@latest version peerDependencies.typescript
   npm view @typescript-eslint/parser@canary version peerDependencies.typescript
   npm view eslint-plugin-react@latest version peerDependencies.eslint
   ```
2. TypeScript 7 entry. It is blocked while the peer range ends `<7` (for example `<6.1.0`). Report blocked or unblocked, with the version and the peer range seen for both `@latest` and `@canary`. Unblocked means a release accepts TypeScript 7.
3. ESLint 10 entry. It is blocked while the `eslint-plugin-react` peer range lacks `^10`. Report blocked or unblocked, with the version and the peer range seen. Other plugins already accept `^10`: `eslint-plugin-react-hooks@7.1.1` and `@typescript-eslint/*@8.69.0`.
4. Report one line per entry: name, blocked or unblocked, version, peer range. Then say what blocks it, in one line.
5. If an entry is unblocked, list its unblock steps from `CLAUDE.md`. Do not perform them unless the maintainer asks.
   - TypeScript 7: do T055 and T056 in `specs/009-remediate-security-dependabot/tasks.md`. Bump `typescript` and `@typescript-eslint/*` in `web/package.json`. Fix real type errors, with no `@ts-ignore`. Merge Dependabot #272. Mark the tasks `[X]`. Delete the TypeScript entry from `CLAUDE.md`.
   - ESLint 10: bump `eslint` and `@eslint/js` together in `web/package.json`. Fix real lint errors, with no `eslint-disable`. Merge #386, and #387 if it is open. Delete the ESLint entry from `CLAUDE.md`.
6. Never close #272, #386 or #387. Never use `--legacy-peer-deps` or overrides to get around a peer range.
