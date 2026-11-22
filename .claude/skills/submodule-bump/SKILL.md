---
name: submodule-bump
description: "Use after a modules, website or component submodule PR has merged to its main branch, to bump the submodule pointer in the main Gameplane repo. Takes one argument: modules, website, mcp-server, audit-syslog-bridge, tunnel or capture-sidecar."
---

Argument: `modules`, `website`, or a component submodule from `.gitmodules` (`mcp-server`, `audit-syslog-bridge`, `tunnel`, `capture-sidecar`). From `.gitmodules`: `modules` is GameplanePanel/module (branch `main`), `website` is GameplanePanel/website (branch `main`, has its own CLAUDE.md and AGENTS.md). Repo mapping: `mcp-server` -> GameplanePanel/mcp; every other component -> GameplanePanel/<same name> (branch `main`).

## Preconditions

1. Name the submodule PR and confirm it is merged, live:
   ```sh
   gh pr view <pr-number> --repo GameplanePanel/module --json state,mergeCommit
   ```
   For website, use `--repo GameplanePanel/website`; for mcp-server use `--repo GameplanePanel/mcp`; for other components use `--repo GameplanePanel/<sub>`. Stop unless `state` is `MERGED`.
2. Check for a pending bump owned by another thread:
   ```sh
   gh pr list --repo GameplanePanel/Gameplane --state open --search "bump <sub> submodule"
   ```
   If one is open, stop and tell the maintainer. Only one bump PR runs at a time.

## Bump the pointer

3. Start a fresh branch from master:
   ```sh
   git fetch origin master
   git checkout -b claude/bump-<sub>-submodule origin/master
   git submodule update --init <sub>
   ```
4. Record the old pointer, then move the submodule to the merged commit on its `main`:
   ```sh
   git -C <sub> fetch origin main
   git -C <sub> checkout <merged-sha>
   ```
5. List the submodule commits the bump includes:
   ```sh
   git -C <sub> log --oneline <old-sha>..<new-sha>
   ```
6. Stage only the pointer:
   ```sh
   git add <sub>
   ```
   If the included submodule PRs bring a change users, operators or module authors will notice, also add `changelog/unreleased/<PR number>-<slug>.md` to the bump PR once it is open (CLAUDE.md rule 19).

## Commit, push, open the PR

7. Commit signed, naming the submodule PRs included:
   ```text
   chore: bump <sub> submodule to <short sha>

   Includes:
   - <submodule PR title> (#<n>)

   Co-Authored-By: <model actually running this session> <noreply@anthropic.com>
   ```
   Use `git commit -s`. Do not add `Claude-Session:` trailers or session links.
8. Push the branch (never to `master`):
   ```sh
   git push -u origin claude/bump-<sub>-submodule
   ```
9. Open the PR with a short Summary (the submodule PRs included) and a Test plan. Labels:
   - `modules`: `type: chore` + `area: modules`
   - `website`: `type: docs` + `area: shared`
   - `mcp-server`, `audit-syslog-bridge`, `tunnel`, `capture-sidecar`: `type: chore` + `area: optional-components`

   Add them via REST, as in `ship-branch` step 7:
   ```sh
   gh api -X POST repos/GameplanePanel/Gameplane/issues/<PR_NUMBER>/labels \
     -f "labels[]=type: chore" \
     -f "labels[]=area: modules"
   ```
10. Follow `ship-branch` for CI watch, the maintainer's approval wait, and branch cleanup after merge.

## Notes

- Never edit submodule contents from the main repo. Module or website changes go through their own PR first.
- Never bump to a commit that is not on the submodule's `main`.
- Never force-push or rebase the bump branch. Use a new commit for fixes.
- If CI on the bump PR fails, diagnose with `ci-triager` before changing anything.
