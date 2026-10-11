# H20 review: fix/018-harden-steamcmd-preset-defaults

- Reviewer tier: opus (held-group rule 6)
- Worktree: /tmp/claude-1000/-home-valgul-project-Gameplane/0cbfe030-6e64-40ca-9a18-d291dbf5b40a/scratchpad/wt-H20
- Commit: 3814393785cfed97d9917021f509058b2f1f975b; parent = merge-base = origin/master 61f50265 (the brief's base)
- Verdict: **pass**

## Scope and brief conformance

- `git diff origin/master...HEAD --stat` lists exactly the 8 files of brief section 5 (+244, -0). The working tree is clean.
- The 7 fast-path files are byte-identical to the staged files in `briefs/files/H20/` (`cmp`). The CHANGELOG hunk matches the brief's AFTER block.
- The commit message matches the brief, is signed off (`-s`), and carries the Co-Authored-By and Claude-Session trailers.
- Nothing under `specs/*/audit/held/` and no `SECURITY_AUDIT.md` in the diff.

## Against the finding and the decisions

- F-169 expects the steamcmd preset to produce a template whose game container isn't root: either a non-root image or a `spec.security` block with a non-root uid/gid. D14 (questions.md, session 4) picked the `spec.security` block, and that's what the branch does: `steamcmd` gets `DefaultSecurity{1000,1000,1000}`, and `GenerateFiles` puts it in `spec.security`. The image is unchanged, so OD-022 (`images/`) isn't triggered.
- The emitted keys `runAsUser`, `runAsGroup` and `fsGroup` (yaml.v3 tags) match `GameSecuritySpec`'s JSON tags (`operator/api/v1alpha1/gametemplate_types.go:176-199`). The operator decodes `template.yaml` with a yaml-to-JSON unmarshal (`module_controller.go:188`), so the fields go through. `gameContainerSecurityContext` (`gameserver_controller.go:1981`) turns runAsUser/runAsGroup into the game container's SecurityContext, and fsGroup covers the volume. No CRD change is needed.
- `java` and `generic` are unchanged (their `DefaultSecurity` is nil, so there's no block and no README section).
- Legitimate callers:
  - The API builder (`modules_builder.go`) serialises `ArchetypeDefinition` and gains an optional `defaultSecurity`, which is additive. `TestBuilderArchetypes` checks only order and `STEAMAPPID`.
  - `TestBuilderScaffold` (steamcmd with a custom image) makes no exact-output assertion on template.yaml.
  - The validator has no unknown-field check on template.yaml; only module.yaml gets one. So the new block raises no finding.
  - Web tests that match the image string still hold because the image is unchanged.
- Behaviour change for a custom image under the steamcmd preset: the block stays, which is intended and documented in specs.md and the generated README. That's the right default for a "non-root" preset.

## Tests

- `TestSteamcmdArchetypeDefaultsToNonRoot` (unit) references the new field, so it can't pass before the fix. It checks uid and gid > 0 and fsGroup equal to the run group.
- `TestGenerateFiles_SteamcmdTemplateRunsNonRoot` (unit) parses the rendered template.yaml and fails before the fix, because it finds no `spec.security`. It covers the default image and a custom image, and checks the README section. `gopkg.in/yaml.v3` is already a direct dependency in `gp-module/go.mod`.
- `TestModule_SteamcmdScaffoldKeepsNonRootSecurity` (E2E): scaffold, then upload ConfigMap, then ModuleSource, then Module Ready, then check the materialized GameTemplate's `spec.security`.
  - It follows the existing `TestModule_ScaffoldAndPackage` pattern: `t.Parallel()`, a unique `e2e-steam-<suffix>` name, and cleanups.
  - It is listed once in `buckets.sh`, in the `operator` bucket, next to its sibling.
  - The file has no `APIClient` use, so it adds 0 logins and `operator` stays at 5 (rule 7).
  - This is a new test rather than the plan's "extend `TestModule_ScaffoldAndPackage`". Rule 7 explicitly allows a new test in `operator`, and a separate test keeps the generic-archetype test unchanged. That's acceptable.
- No existing test is changed or weakened.

## Docs, specs, CHANGELOG

- `gp-module/specs.md` gains invariant 5, which matches the code (uid/gid/fsGroup 1000, the block kept for a custom image, the README note).
- CHANGELOG:
  - `### Security hardening` is created at the end of `## [Unreleased]`, directly before `## [0.3.0-rc.1]`, and there was no earlier heading.
  - The line reads "- **gp-module:** hardened the steamcmd preset so scaffolded templates run the game as a non-root user.", which is the required format.

## Wording-leak check

- Every added line, and the commit message, was grepped for F-IDs, H-IDs, D/OD IDs, audit, exploit, attack, vuln, escalat and repro. There were no hits. The one match, "findings" in an E2E fatal message, refers to validator findings and copies the existing sibling test.
- Comments and test names state the control ("defaults to non-root", "keeps non-root security") and don't describe any gap.

## Compile checks (run by the reviewer)

- `gp-module`: `gofmt -l .` printed nothing; `go build ./...` and `go vet ./...` passed.
- `api`: `go build ./...` passed.
- `test/e2e`: `gofmt -l .` printed nothing; `go vet -tags e2e ./...` passed.

## Non-blocking notes (maintainer, not git)

1. uid 1000 comes from upstream's Dockerfile (`useradd -u 1000 -m steam`). The pinned digest `4d830b...` wasn't pulled. Confirm on one real steamcmd module that `/home/steam/steamcmd` is owned by 1000 in that digest (as the brief also says).
2. The E2E requires `valReport.Clean` for a default steamcmd scaffold. That wasn't checked by running it (no tests allowed). A static read of the validator rules shows nothing the default preset would trip: the image is digest-pinned, summary and categories are defaulted, and there's no template unknown-field rule. CI's `e2e operator` job confirms it.
3. There's an optional follow-up: add the `security` block to the `template.yaml` example in `specs/done_010-easy-module-building/contracts/archetypes-contract.md` section 2.1. There's also a release note for authors of steamcmd modules scaffolded earlier (see brief HELD NOTES).
