---
name: merge-master
description: "Use to bring master into a long-lived PR branch with a merge commit (never a rebase), resolve conflicts with regenerated files, then push and re-check CI."
---

## Check the Branch

1. Check the PR live. It must be open, and the branch must not be master:
   ```sh
   gh pr view <PR_NUMBER> --json state,headRefName,mergeStateStatus
   ```

2. Stop if the branch is not yours to push to. A Dependabot branch belongs to Dependabot. A contributor branch needs the maintainer's word first. For a Dependabot branch, either push a fix commit on top or open a separate `claude/...` branch. Never rebase or force-push it.

3. Confirm a clean tree:
   ```sh
   git status
   ```
   Stop if there are uncommitted changes. Commit or stash them first. Ask the maintainer which.

## Merge

4. Check out the PR branch and fetch master:
   ```sh
   git switch <branch>
   git fetch origin master
   ```

5. Merge with a merge commit. Never rebase:
   ```sh
   git merge --no-ff origin/master
   ```

6. If there are no conflicts, skip to step 10.

## Resolve Conflicts

7. List the conflicted files:
   ```sh
   git status
   ```

8. Sort each conflicted file into one of three groups:
   - **Generated files.** Do not hand-resolve. Take either side, then regenerate with tooling:
     - CRD types and deepcopy, `operator/config/crd/`, `operator/config/rbac/`, `charts/gameplane/crds/`: `make generate && make manifests`
     - `web/package-lock.json`: `npm install` in `web/`
     - Go module files: `make tidy`
   - **Both sides changed the same logic.** Stop. Ask the maintainer which behavior wins. Do not pick one yourself.
   - **Everything else.** Resolve by hand, keeping both sides' intent. Keep the edit minimal.

9. Stage the resolved files by name. Do not use `git add -A`:
   ```sh
   git add <file> [<file> ...]
   ```

## Compile Check and Commit

10. Compile check only. Do not run tests or lint:
    ```sh
    go build ./...        # in each Go module the merge touched
    npx tsc --noEmit      # in web/, if web/ changed
    npm run build         # in website/, if website/ changed
    ```

11. Commit the merge signed, with the trailer. Do not use `--no-verify`:
    ```sh
    git commit -s -m "chore: merge master into <branch>" -m "Co-Authored-By: <model actually running this session> <noreply@anthropic.com>"
    ```
    The trailer names the model that is running. Do not add a `Claude-Session:` trailer.

## Push and Re-check

12. Push as a normal push. Never force:
    ```sh
    git push origin <branch>
    ```

13. Re-check CI on the new head. Do not merge:
    ```sh
    gh pr checks <PR_NUMBER>
    gh run watch
    ```

14. Expect the approval to drop. The ruleset dismisses stale reviews on push. Tell the maintainer the approval must be given again. Do not ask for a re-approval on your own.

15. Check the merge state after CI finishes:
    ```sh
    gh pr view <PR_NUMBER> --json mergeStateStatus
    ```
    A `BLOCKED` state with green checks means the approval is missing. It does not mean something is broken.

## Notes

- Never rebase. Never `git push --force`. Never amend a pushed commit.
- Never merge master into master.
- Never use `gh pr merge` or `--admin`. Agents do not merge PRs.
- If CI fails after the merge, hand the failure to `ci-triager`. Do not fix by disabling a test.
