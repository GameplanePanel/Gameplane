---
name: local-prompt
description: "Use when a piece of work needs design (OpenPencil for design.fig, Pencil for the website) or frontend changes that this session cannot run, or the maintainer asks for a local prompt. Writes a copy-paste prompt for the maintainer's local agent."
---

The OpenPencil app and its MCP server, and Pencil MCP (`mcp__pencil__*`, website only), run on the maintainer's local machine. Cloud sessions can edit `design.fig` headlessly with the OpenPencil CLI from a ValgulNecron/open-pencil `master` checkout, and the website with the Pen CLI (`pen interactive`, needs `PEN_CLI_KEY`; see the `design-export` skill), so check that first and use the `pencil-designer` agent in this session when it works. Use this skill when neither path works here, or when the maintainer asks for a local prompt. It writes the prompt the maintainer pastes into a local Claude Code session.

## Steps

1. Confirm the spec is fully written. Check `specs/<NNN>-<slug>/` has `spec.md`, `plan.md` and `tasks.md`, and that `OPEN-DECISIONS.md` has no unsettled entries. If anything is open, stop. The spec must be finished first with the speckit skills. Never ask the local agent to finish or edit the spec.
2. Confirm the branch is pushed, and list what is already done: commits and PRs on that branch (`git log --oneline origin/<branch>`, `gh pr list --head <branch>`).
3. Collect the T-numbers for the work from `tasks.md` (`- [ ] T012 …` lines).
4. Fill in the template below. Replace every `<placeholder>`. Remove sections that do not apply, but keep Constraints and Done when.
5. Print the filled block in a fenced `text` block so the maintainer can copy it. If the maintainer named a shared folder, also save the same text there.
6. Tell the maintainer: paste the block into a local Claude Code session that has OpenPencil (or, for the website, Pencil) connected, and paste the local agent's report back into this thread.

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
- Design first in design.fig (dashboard, OpenPencil) or website/website.pen (website, Pencil), using the `pencil-designer` agent.
- Then build the React code with the `web-builder` agent from the approved design export.
- Screens: <list>
- Components: <list>
- States and variants: <light/dark, mobile, empty, error, loading>
- Files expected: <list of paths>

## Already done
- <commit or PR, one per line, or "nothing yet">

## Constraints
- Design first: nothing in web/ until the maintainer approves the screenshots.
- Never hand-edit or read .fig or .pen files. Use OpenPencil (or Figma) for .fig and Pencil for .pen.
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
