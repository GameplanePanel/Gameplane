# Open Decisions: 027-contributor-docs-layout-map

Unsettled items. Do not treat any of the below as decided until a maintainer rules on it here.

## Settled (valgul, 2026-10-20)

- **OD-1 (FR-001..FR-004): canonical agent instructions file.** `AGENTS.md` is the real file in both repos. Main repo: the content moves from `CLAUDE.md` into `AGENTS.md` (regular file), `CLAUDE.md` becomes a symlink to `AGENTS.md`, and the unused `AGENT.md` symlink is deleted. Website already matches (`AGENTS.md` real, `CLAUDE.md` link), so it is unchanged. (Premise corrected before asking: `AGENT.md` and `AGENTS.md` were already symlinks to `CLAUDE.md`, not copies.)

- **OD-2 (FR-005..FR-008): spec and design homes.** Two written roles: `specs/NNN-<slug>/` for changes (speckit), `<module>/specs.md` for how a module works today (names unchanged, constitution IV unchanged). `docs/superpowers/` moves with `git mv` to `docs/archive/superpowers/`, each file gets a "historical, superseded" banner. FR-008 does not apply. (Premise corrected before asking: the 19 live `specs.md` files are constitution IV component docs, not a competing spec home.)
- **OD-3 (FR-009, FR-011, FR-012): Go module layout.** Long-running services: thin `cmd/main.go` + `internal/<concern>/` (as `api`, `operator`, `agent`, `capture-sidecar` already do). Libraries stay one flat root package; CLIs use `cmd/<binary>/` (stated on the card as common to every option).

- **OD-4 (FR-010): web/src layout.** valgul's words (2026-10-20): "screen with part reused 2+ time get in components. if html file is more than 500line i get split in section using the following pages/screen_name.html if too big pages/screen_name/footer.html pages/screen_name/upper_section.html etc... then routes expose those pages". Recorded as: a new `web/src/pages/` holds one `pages/<Screen>.tsx` per screen ("html" read as the React `.tsx` files); a screen over 500 lines is split into section files under `pages/<Screen>/` (e.g. `pages/<Screen>/Footer.tsx`, `pages/<Screen>/UpperSection.tsx`) and `pages/<Screen>.tsx` composes them; `routes/` only exposes pages (route definitions, no screen code); `components/` holds parts used by 2+ screens. Tests stay next to the file they test (today's convention).
- **OD-5 (FR-013): layout enforcement.** Spec 028 (split big packages) adds a folder-shape check to `make lint` once every module matches the layout; this spec adds no check. Until then review enforces `docs/layout.md`.

No open decisions remain.
