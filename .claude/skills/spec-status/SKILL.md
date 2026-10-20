---
name: spec-status
description: "Use to count open, done, skipped and withdrawn tasks for each active spec on a fresh origin/master. Read-only; never changes the working tree."
---

## Read Master, Not the Local Branch

1. Fetch master. Do not check it out:
   ```sh
   git fetch origin master
   ```

2. List the spec folders on master. Skip archived folders (`done_*`):
   ```sh
   git ls-tree --name-only origin/master specs/ | sed 's#^specs/##' | grep -v '^done_'
   ```

3. For each folder, read its tasks file from master, not from the local checkout. Local branch state would skew the counts:
   ```sh
   git show origin/master:specs/<dir>/tasks.md
   ```
   If a folder has no `tasks.md`, list it as "no tasks file" and move on.

## Count the Tasks

4. Classify each task line. Task lines look like `- [ ] T012 ...` or `- [X] T012 ...`. Lowercase `[x]` counts as done too.

   | Line | Column |
   |---|---|
   | `[X]` or `[x]`, not withdrawn, not SKIPPED | Done |
   | `[ ]`, not withdrawn, not SKIPPED | Open |
   | Contains `SKIPPED` | Skipped |
   | Contains `withdrawn` (ticked or not) | Withdrawn |

   Withdrawn wins over Skipped and Done. A line that is both gets counted once, in Withdrawn.

5. Record the first open T-number in file order. That is the "next open" task.

6. Use this one-liner to do steps 2 to 5 in one pass. It prints one row per spec:
   ```sh
   git fetch origin master && git ls-tree --name-only origin/master specs/ | sed 's#^specs/##' | grep -v '^done_' | while read -r d; do
     git cat-file -e "origin/master:specs/$d/tasks.md" 2>/dev/null || continue
     git show "origin/master:specs/$d/tasks.md" | awk -v s="$d" '
       /^- \[[Xx]\] T[0-9]+/ { if (/withdrawn/) wd++; else if (/SKIPPED/) sk++; else done++; tot++; next }
       /^- \[ \] T[0-9]+/ { tot++; if (/withdrawn/) wd++; else if (/SKIPPED/) sk++; else { open++; if (!nxt) { match($0, /T[0-9]+/); nxt = substr($0, RSTART, RLENGTH) } }; next }
       END { printf "%s|%d/%d|%d|%d|%d|%s\n", s, done+0, tot+0, open+0, sk+0, wd+0, nxt }'
   done
   ```
   Output columns: spec, done/total, open, skipped, withdrawn, next open.

## Report

7. Print one table. Use these columns:

   | Spec | Done/Total | Open | Skipped | Withdrawn | Next open |
   |---|---|---|---|---|---|

8. Under the table, list every spec where Open is 0 and Skipped is above 0. Say these are waiting on captures or other work, not on code. Do not call them complete.

9. Note any spec with a tasks file that has no task lines at all.

## Rules

- Read-only. Do not switch branches, reset, commit, or push.
- Do not edit `tasks.md` to fix a count. If a count looks wrong, report the line.
- Do not count `done_*` folders. They are archived.
- Do not run tests, lint, or any `make` target for this.
