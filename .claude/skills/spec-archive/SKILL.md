---
name: spec-archive
description: "Use to archive a finished spec: git mv specs/<NNN>-<slug> to specs/done_<NNN>-<slug> in one commit with its reference updates, only after the spec's work is merged to master."
---

## Preconditions

Check each one live. If any fails, stop and report which one.

1. Every task in the spec's `tasks.md` is `[X]` or **withdrawn**. Withdrawn tasks need a date and a citation.
2. No task is SKIPPED. SKIPPED tasks block archiving. Ask the maintainer first.
3. The spec's PR is merged to master:
   ```sh
   gh pr list --head <branch> --state merged --json number,mergedAt
   ```
4. Every entry in the spec's `OPEN-DECISIONS.md` is settled. Only the maintainer's recorded answers count as settled.
5. The maintainer has not asked to keep the name. Example: spec 025 stays as is until a standalone e2e test exists.

## Procedure

6. Start a fresh branch from master:
   ```sh
   git fetch origin master
   git switch -c claude/archive-spec-<NNN> origin/master
   ```

7. Move the folder. Use `git mv`, and do not add an archive subfolder:
   ```sh
   git mv specs/<NNN>-<slug> specs/done_<NNN>-<slug>
   ```

8. Find every reference to the old path:
   ```sh
   grep -rn "specs/<NNN>-<slug>" --exclude-dir=node_modules .
   ```
   Update each hit to the new path. Check `CLAUDE.md`, `docs/`, other specs, `.github/workflows/`, and scripts such as `hack/check-coverage-gaps.sh`. Keep the changes in the same commit as the move.

9. Do not run `hack/check-links.sh` or any lint or test locally. CI checks links. Only do compile checks if a Go or web file changed, and then only `go build ./...` or `npx tsc --noEmit` in `web/`.

10. Stage the explicit paths. Do not use `git add -A`:
    ```sh
    git add specs/done_<NNN>-<slug> <each updated file>
    ```

11. Commit signed. Use the prefix `chore(specs):`:
    ```sh
    git commit -s -m "chore(specs): archive <NNN> to done_<NNN>" -m "Co-Authored-By: <model actually running this session> <noreply@anthropic.com>"
    ```
    The trailer names the model that is running. Do not copy it from another file or commit. Do not add a `Claude-Session:` trailer.

12. Push the branch. Never push to master:
    ```sh
    git push -u origin claude/archive-spec-<NNN>
    ```

## Open the PR

13. Open the PR against master. The body lists the moved folder and each reference you updated. Do not add claude.ai session links or a "Requested by" block:
    ```sh
    gh pr create --base master --title "chore(specs): archive <NNN> to done_<NNN>" --body "$(cat <<'EOF'
    ## Summary
    - Move specs/<NNN>-<slug> to specs/done_<NNN>-<slug>
    - Update references: <list each file>

    🤖 Generated with [Claude Code](https://claude.com/claude-code)
    EOF
    )"
    ```

14. Add labels with REST only. `gh pr edit` is broken on this repo:
    ```sh
    gh api -X POST repos/GameplanePanel/Gameplane/issues/<PR_NUMBER>/labels \
      -f "labels[]=type: chore" \
      -f "labels[]=area: specs"
    ```

15. Verify the labels:
    ```sh
    gh api repos/GameplanePanel/Gameplane/issues/<PR_NUMBER>/labels -q '[.[].name]|join(", ")'
    ```

## After Merge

16. Never merge the archive PR yourself. The maintainer approves and merges.

17. After the merge, delete the remote branch, then the local branch:
    ```sh
    git push origin --delete claude/archive-spec-<NNN>
    git branch -d claude/archive-spec-<NNN>
    ```

## Notes

- Archive only after the spec PR is merged. Never archive on an open branch.
- Keep the folder name, file names, and task text unchanged. Only the folder name changes.
- If a reference cannot be updated safely, stop and ask. Do not leave a dead link.
