---
name: ship-branch
description: "Use when a branch is ready to become a PR, or a merged PR needs closing out — encodes this repo's PR labels, ruleset, and branch-hygiene rules."
---

## Push & Watch CI

1. Ensure the branch is signed and all commits have the full trailer:
   ```text
   Co-Authored-By: <model actually running this session> <noreply@anthropic.com>
   ```
   
   The Co-Authored-By name MUST be the model actually running — never a value copied from CLAUDE.md, this skill, or a prior commit.

2. Push the feature branch (never to `master`):
   ```sh
   git push -u origin <branch-name>
   ```

3. Watch the CI run on GitHub Actions. The suite must **pass on CI**, not locally (rule 8):
   ```sh
   gh run watch
   # or for a specific run:
   gh run view <run-id> --watch
   ```

4. If CI fails, fix the issue and create a **new commit** (never amend):
   ```sh
   git commit -s -m "fix: <scope> - <description>
   
   Co-Authored-By: <model actually running this session> <noreply@anthropic.com>"
   git push
   ```
   - The new commit re-triggers CI automatically.
   - Repeat until the branch is green.

5. **Do not request review until CI is green.** Rule 12 / `dismiss_stale_reviews_on_push` means any later commits **drop an approval** — get the branch fully passing before asking.

## Create the PR

6. Open the PR:
   ```sh
   gh pr create --title "brief title (under 70 chars)" --body "$(cat <<'EOF'
   ## Summary
   - Change 1
   - Change 2

   ## Test plan
   - [ ] Manual test step 1
   - [ ] Manual test step 2

   🤖 Generated with [Claude Code](https://claude.com/claude-code)
   EOF
   )"
   ```

7. **Mandatory labels** (rule 14) — at least one `type:` and one `area:`, plus `breaking` if applicable:

   **Add labels via REST API** (rule 14: `gh pr edit` is broken on this repo):
   ```sh
   # Example: type:fix + area:api
   gh api -X POST repos/GameplanePanel/Gameplane/issues/<PR_NUMBER>/labels \
     -f "labels[]=type: fix" \
     -f "labels[]=area: api"
   ```

   **Type labels** (pick one): `type: feature`, `type: fix`, `type: refactor`, `type: test`, `type: ci`, `type: chore`, `type: docs`, `type: security`

   **Area labels** (pick one or more): `area: operator`, `area: api`, `area: agent`, `area: web`, `area: modules`, `area: chart`, `area: e2e`, `area: specs`, `area: shared`, `area: optional-components`

   **Special labels** (if applicable): `breaking` (for CRD/API/chart changes)

8. Verify labels were applied:
   ```sh
   gh api repos/GameplanePanel/Gameplane/issues/<PR_NUMBER>/labels \
     -q '[.[].name]|join(", ")'
   ```

## Await Maintainer Merge

9. The PR is now blocked waiting on the maintainer (rule 12: `required_approving_review_count: 1` and self-approval is impossible). Do not use `gh pr merge --admin` or any other bypass.

10. Watch for the maintainer's approval and merge. Once merged:

## Clean Up After Merge

11. Delete the remote branch:
    ```sh
    git push origin --delete <branch-name>
    ```

12. Delete the local branch:
    ```sh
    git branch -d <branch-name>
    ```

13. **Do not delete unmerged branches** or stacked children whose descendants still depend on them (merge bottom-up first).

## Notes

- **Master is protected** by a ruleset (rule 12): direct pushes refused, requires approval, dismisses stale reviews on later pushes.
- **Codegen output** (e.g., CRD regenerates per rule 7) goes in the same commit as the source change that triggered it.
- **Breaking changes** to CRDs, API, or the Helm chart require the `breaking` label (rule 14).
- **No session links**: never add `Claude-Session:` trailers or claude.ai session URLs to commits or PR bodies, and never add a "Requested by … · project thread" block to PR bodies.
