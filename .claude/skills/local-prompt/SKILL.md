---
name: local-prompt
description: "Use when a piece of work needs Pencil design or frontend changes that a cloud session cannot run. Writes a copy-paste prompt for the maintainer's local agent, which has the Pencil MCP."
---

Cloud sessions cannot run Pencil MCP (`mcp__pencil__*`). Pencil runs only on the maintainer's local machine. This skill writes the prompt the maintainer pastes into a local Claude Code session.

## Steps

1. Confirm the spec is fully written. Check `specs/<NNN>-<slug>/` has `spec.md`, `plan.md` and `tasks.md`, and that `OPEN-DECISIONS.md` has no unsettled entries. If anything is open, stop. The spec must be finished first with the speckit skills. Never ask the local agent to finish or edit the spec.
2. Confirm the branch is pushed, and list what is already done: commits and PRs on that branch (`git log --oneline origin/<branch>`, `gh pr list --head <branch>`).
3. Collect the T-numbers for the work from `tasks.md` (`- [ ] T012 …` lines).
4. Fill in the template below. Replace every `<placeholder>`. Remove sections that do not apply, but keep Constraints and Done when.
5. Print the filled block in a fenced `text` block so the maintainer can copy it. If the maintainer named a shared folder, also save the same text there.
6. Tell the maintainer: paste the block into a local Claude Code session that has the Pencil MCP connected, and paste the local agent's report back into this thread.

## Template

````text
Task for the local agent. Read the repo CLAUDE.md first and follow it.

## Branch and repos
- Repo: <repo, e.g. GameplanePanel/Gameplane>
- Branch: <branch>
- Submodules involved: <none, or modules / website>

## Spec and tasks
- Spec folder: specs/<NNN>-<slug>/
- Tasks: <T-numbers, e.g. T012, T013, T014>

## What to design, then build
- Design first in design.pen (dashboard) or website/website.pen (website), using the `pencil-designer` agent.
- Then build the React code with the `web-builder` agent from the approved design export.
- Screens: <list>
- Components: <list>
- States and variants: <light/dark, mobile, empty, error, loading>
- Files expected: <list of paths>

## Already done
- <commit or PR, one per line, or "nothing yet">

## Constraints
- Design first: nothing in web/ until the maintainer approves the screenshots.
- Never hand-edit or read .pen files. Use Pencil MCP only.
- Use `pen-canvas-layout` for placement, `pen-componentize` at the end of the design pass, and `design-export` in the same commit as the design change.
- Compile checks only: `npx tsc --noEmit` in web/. Do not run tests or lint locally.
- Signed commits (`git commit -s`), conventional prefixes, one logical unit per commit.
- Co-Authored-By trailer only. No session links or Claude-Session trailers.
- Do not change the spec. If the spec seems wrong, stop and report.

## Done when
- [ ] Every screen and component in "What to design" is designed and exported.
- [ ] pen-componentize run; no copies of existing components left.
- [ ] Maintainer approved the screenshots.
- [ ] React code for the approved screens is written, and tsc passes.
- [ ] Branch pushed.

## How to hand back
- Push the branch, then reply with: the branch name, the commits, what is done against the checklist, and anything blocked or open.
````

## Notes

- A prompt that asks for work beyond the spec is not valid. Tell the maintainer and stop.
- Keep placeholders short and factual. Facts come from the spec and tasks, not from memory.
