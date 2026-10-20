# Data Model: Contributor Docs and Layout Map

Docs-only feature; the "entities" are document records.

| Entity | Fields | Where |
|---|---|---|
| Canonical instructions file | repo, real file, link names | GP root, WEB root; described in `docs/contributing.md` |
| Docs rule | kind of material → home | `docs/contributing.md` "Where design docs go", CLAUDE.md rule 15 |
| Layout kind | name, folder tree, folder roles, test location, allowed root files | `docs/layout.md` §1 |
| Layout map row | module, kind, target, gap today | `docs/layout.md` §2 (18 rows) |
| Layout exception | module, reason | `docs/layout.md` §3 |
| First-PR row | change type, owning folder, local check, CI wall clock, source run | `docs/contributing.md` "Your first PR" |
| Task-to-doc row | task, docs to read | `docs/agent-architecture.md` |
| Archived design | original path, banner, current-behavior doc | `docs/archive/superpowers/` (OD-2) |
