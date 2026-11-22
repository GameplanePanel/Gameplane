# Brief: PR 4, shared CI configuration mapped per job (T042–T046)

Branch `claude/project-thread-63yei6-pr4`, stacked on PR 3. Facts below were checked
once by the scout; fix agents apply them blind. Every edit is exact: find the quoted
"old" text and replace it with "new". Do not touch any other line. Never edit an
existing test, except section 4b, which needs valgul's sign-off first.

Scout facts:

- In `ci.yaml`, job keys are the two-space keys under `jobs:`. `needs:` appears as
  `    needs: [a, b]`, as a `- a` list, or as a multi-line `[ … ]` flow list (job
  `report`).
- `ci.yaml` and `.golangci.yml` force everything today in three places:
  `FORCE_ALL_FILES`; the `shared` set plus the `.github/` prefix in `affected_modules`;
  and the paths-filter `ci` list.
- The `lint` job reads `lint-exclude`, `go` reads `go-exclude` and `GO_PACKAGES`,
  `web` reads `web-tests`, the `e2e-go*` jobs read `e2e-run`, and `e2e-game-bot` reads
  `e2e-bot-matrix`. A ci.yaml edit inside one of those jobs must therefore widen that
  job's own selection. A job-level `if:` is not enough on its own.

## 1. `hack/ci_scope.py`

### 1a. `FORCE_ALL_FILES`

old:
```python
FORCE_ALL_FILES = {
    ".github/workflows/ci.yaml", "hack/ci_scope.py", "hack/test_ci_scope.py", "Makefile",
    "go.work", "go.work.sum", "test/e2e/buckets.sh", ".golangci.yml",
    "hack/check_changed_coverage.py", "hack/test_check_changed_coverage.py",
}
```
new:
```python
FORCE_ALL_FILES = {
    "hack/ci_scope.py", "hack/test_ci_scope.py", "Makefile", "go.work", "go.work.sum",
    "test/e2e/buckets.sh", "test/e2e/suite-components.json",
    "hack/check_changed_coverage.py", "hack/test_check_changed_coverage.py",
}
```

### 1b. `NO_E2E_FILES`: add `.golangci.yml` (otherwise it is unmapped)

old:
```python
    "design.pen", "LICENSE", ".claudeignore", ".coderabbit.yaml", ".editorconfig",
```
new:
```python
    "design.pen", "LICENSE", ".claudeignore", ".coderabbit.yaml", ".editorconfig", ".golangci.yml",
```

### 1c. Constants: insert after the `WEB_ALL_FILES = {...}` statement (two lines)

```python
CI_FILE = ".github/workflows/ci.yaml"
# ci.yaml jobs that compute or report the selection; an edit there forces full scope (R8).
CI_SELECTION_JOBS = {"changes", "report"}
CI_JOB_KEY = re.compile(r"^  ([A-Za-z0-9_-]+):\s*(?:#.*)?$")
CI_TOP_KEY = re.compile(r"^[A-Za-z_][\w-]*:")
# e2e jobs whose own switch output a ci.yaml edit inside them turns on (mode e2e).
E2E_JOB_SWITCHES = {
    "e2e-multicluster": "e2e-multicluster", "e2e-multicluster-arm64": "e2e-multicluster",
    "e2e-upgrade": "e2e-upgrade", "e2e-upgrade-arm64": "e2e-upgrade",
    "e2e-web-live": "e2e-web-live", "e2e-web-live-arm64": "e2e-web-live",
    "e2e-capture-overhead": "e2e-capture-overhead",
}
# Summary row prefixes and the ci.yaml jobs behind them; the first match wins.
ROW_JOBS = (
    ("lint (", ("lint",)),
    ("go (test/e2e)", ("go-e2e-unit",)),
    ("go (", ("go",)),
    ("api (postgres)", ("api-postgres",)),
    ("web e2e (mock)", ("web-e2e-mock",)),
    ("web", ("web",)),
    ("helm lint", ("helm",)),
    ("chart render", ("chart-template",)),
    ("standalone Compose smoke", ("standalone-compose",)),
    ("workflow lint", ("workflow-lint",)),
    ("docs and repository checks", ("docs",)),
    ("codegen drift", ("codegen-drift",)),
    ("submodule freshness", ("submodule-freshness",)),
    ("vuln (go)", ("vuln-go",)),
    ("capture-sidecar CAP_NET_RAW survival", ("capture-sidecar-setcap-proof",)),
    ("dependency review", ("dependency-review",)),
    ("telemetry default gate", ("telemetry-default-gate",)),
    ("build e2e images", ("build-images", "build-images-arm64")),
    ("vuln (images)", ("vuln-images",)),
    ("e2e bucket coverage", ("e2e-buckets",)),
    ("e2e multicluster", ("e2e-multicluster", "e2e-multicluster-arm64")),
    ("e2e upgrade", ("e2e-upgrade", "e2e-upgrade-arm64")),
    ("e2e postgres", ("e2e-postgres",)),
    ("e2e game bot", ("e2e-game-bot",)),
    ("e2e web live", ("e2e-web-live", "e2e-web-live-arm64")),
    ("e2e capture overhead", ("e2e-capture-overhead",)),
    ("ingress-nginx smoke", ("e2e-kind-ingress-smoke",)),
    ("e2e ", ("e2e-go", "e2e-go-arm64")),
)
```

### 1d. `affected_modules`

old:
```python
    shared = {"Makefile", "go.work", "go.work.sum", ".golangci.yml", "test/e2e/buckets.sh"}
    if paths is None or any(
        p in shared or p.startswith((".github/", "hack/ci_scope", "hack/test_ci_scope",
```
new:
```python
    # ci.yaml and .golangci.yml map to their own jobs through ci_jobs() (OD-3).
    shared = {"Makefile", "go.work", "go.work.sum", "test/e2e/buckets.sh"}
    if paths is None or any(
        p in shared or p.startswith((".github/actions/", "hack/ci_scope", "hack/test_ci_scope",
```

### 1e. New functions: insert right before `def verify_suites(suites, buckets, modules):`

```python
def ci_blocks(text):
    """Return ({line number: job id or None}, {job id: needed job ids}) for a workflow."""
    owner, needs = {}, {}
    job, in_jobs, collecting = None, False, None
    for number, line in enumerate(text.splitlines(), 1):
        if CI_TOP_KEY.match(line):
            in_jobs, job, collecting = line.split(":", 1)[0] == "jobs", None, None
        elif in_jobs and CI_JOB_KEY.match(line):
            job, collecting = CI_JOB_KEY.match(line)[1], None
            needs.setdefault(job, set())
        elif job and line.startswith("    needs:"):
            collecting = job
            line = line[len("    needs:"):]
        elif re.match(r"^    \S", line):
            collecting = None
        owner[number] = job
        if collecting:
            needs[collecting].update(re.findall(r"[A-Za-z0-9_-]+", line.split("#", 1)[0]))
    return owner, needs


def changed_ci_lines(root, base, head):
    """Return ([(old line, text)], [(new line, text)]) for ci.yaml lines changed base..head."""
    diff = subprocess.run(["git", "diff", "-U0", "--no-color", base, head, "--", CI_FILE],
                          cwd=root, check=True, capture_output=True, text=True).stdout
    removed, added, old, new, started = [], [], 0, 0, False
    for line in diff.splitlines():
        hunk = re.match(r"^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@", line)
        if hunk:
            old, new, started = int(hunk[1]), int(hunk[2]), True
        elif started and line.startswith("-"):
            removed.append((old, line[1:]))
            old += 1
        elif started and line.startswith("+"):
            added.append((new, line[1:]))
            new += 1
    return removed, added


def git_text(root, revision, path):
    """Return a file's text at revision, or "" when it does not exist there."""
    result = subprocess.run(["git", "show", f"{revision}:{path}"], cwd=root,
                            capture_output=True, text=True, check=False)
    return result.stdout if result.returncode == 0 else ""


def ci_jobs(root, base, head, paths):
    """Map shared CI file changes to ci.yaml job ids (research R8, OD-3).

    Returns (jobs, forced). jobs holds every job whose ci.yaml lines changed, `lint` for
    .golangci.yml, `workflow-lint` for any .github/ or .golangci.yml change, and every job
    they need. forced is True when a changed ci.yaml line that is not blank or a comment
    sits outside any job or inside the changes or report job.
    """
    jobs, forced, needs = set(), False, {}
    if any(p.startswith(".github/") or p == ".golangci.yml" for p in paths):
        jobs.add("workflow-lint")
    if ".golangci.yml" in paths:
        jobs.add("lint")
    if CI_FILE in paths:
        removed, added = changed_ci_lines(root, base, head)
        for revision, changes in ((base, removed), (head, added)):
            owner, found = ci_blocks(git_text(root, revision, CI_FILE))
            needs.update(found)
            for number, text in changes:
                if not text.strip() or text.strip().startswith("#"):
                    continue
                job = owner.get(number)
                if job is None or job in CI_SELECTION_JOBS:
                    forced = True
                else:
                    jobs.add(job)
    queue = list(jobs)
    while queue:
        for name in needs.get(queue.pop(), ()):
            if name not in jobs:
                jobs.add(name)
                queue.append(name)
    return sorted(jobs - CI_SELECTION_JOBS), forced


def scoped(env, base, head):
    """Return (paths, reason, ci.yaml jobs): the diff, why everything runs, edited jobs."""
    event_reason = full_scope_reason(env, [])
    paths = None if event_reason else changed_paths(ROOT, base, head)
    reason = full_scope_reason(env, paths)
    if reason:
        return paths, reason, []
    jobs, forced = ci_jobs(ROOT, base, head, paths)
    return paths, ("selection-rule change" if forced else None), jobs


def widen_e2e(values, full, jobs):
    """Run every test of each e2e job whose ci.yaml block changed (research R8)."""
    if {"e2e-go", "e2e-go-arm64"} & set(jobs):
        values["e2e-run"] = full["e2e-run"]
    for job, key in E2E_JOB_SWITCHES.items():
        if job in jobs:
            values[key] = True
    if "e2e-game-bot" in jobs:
        values["bot-matrix"], values["bots-any"] = full["bot-matrix"], full["bots-any"]
    return values


def widen_units(values, jobs):
    """Run every package or web test of a go or web job whose ci.yaml block changed."""
    if "go" in jobs:
        values["go-packages"] = {m: ["./..."] for m in values["go-packages"]}
    if "web" in jobs:
        values["web-tests"] = "all"
    return values
```

### 1f. `summarize`: rows turned on by an edited ci.yaml job

old:
```python
    units = load_json(env, "UNITS_OUTPUTS")
```
new:
```python
    units = load_json(env, "UNITS_OUTPUTS")
    edited_jobs = set(jlist(env, "CI_JOBS"))
```

old:
```python
    def add(suite, run, feed=(), run_reason="", skip_reason="not affected", note=""):
        """Append one selection row with a run or skip decision and its reason."""
```
new:
```python
    def add(suite, run, feed=(), run_reason="", skip_reason="not affected", note=""):
        """Append one selection row with a run or skip decision and its reason."""
        jobs = next((found for prefix, found in ROW_JOBS if suite.startswith(prefix)), ())
        if not run and edited_jobs & set(jobs):
            run, run_reason = True, "ci.yaml: lines in this job changed"
```

### 1g. `main`

Mode choices. old:
```python
                                         "e2e", "verify-suites", "units"))
```
new:
```python
                                         "e2e", "verify-suites", "units", "jobs"))
```

Mode e2e. old:
```python
            event_reason = full_scope_reason(env, [])
            paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
            values = e2e_outputs(ROOT, paths, full_scope_reason(env, paths))
```
new:
```python
            paths, reason, jobs = scoped(env, args.base, args.head)
            values = e2e_outputs(ROOT, paths, reason)
            if jobs:
                values = widen_e2e(values, e2e_outputs(ROOT, None, "ci.yaml job edit"), jobs)
```

Mode units. old:
```python
            event_reason = full_scope_reason(env, [])
            paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
            reason = full_scope_reason(env, paths)
            modules = workspace_modules(ROOT)
            unmapped = [p for p in paths or [] if is_unmapped(p, modules)]
            if reason is None and unmapped:
                reason = f"unmapped path {unmapped[0]}"
            values = unit_outputs(ROOT, paths, reason)
```
new:
```python
            paths, reason, jobs = scoped(env, args.base, args.head)
            modules = workspace_modules(ROOT)
            unmapped = [p for p in paths or [] if is_unmapped(p, modules)]
            if reason is None and unmapped:
                reason = f"unmapped path {unmapped[0]}"
            values = widen_units(unit_outputs(ROOT, paths, reason), jobs)
```

New mode `jobs`: insert right before the line `    if args.mode == "units":`
```python
    if args.mode == "jobs":
        try:
            _, reason, jobs = scoped(os.environ, args.base, args.head)
        except Exception as exc:  # any doubt selects the full suite (FR-002)
            print(f"::warning::ci.yaml job mapping failed, running everything: {exc}")
            reason, jobs = f"error: {exc}", []
        emit({"all": bool(reason), "reason": reason or "", "ci-jobs": jobs})
        return
```

Mode modules. old:
```python
        event_reason = full_scope_reason(env, [])
        paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
        if paths is not None and any(is_unmapped(p, workspace_modules(ROOT)) for p in paths):
            paths = None  # mode e2e reports the path; every module runs (FR-002)
        selected = affected_modules(ROOT, paths)
    except Exception as exc:  # any doubt selects the full suite (FR-002)
        print(f"::warning::test selection failed, running everything: {exc}")
        paths = None
        selected = sorted(workspace_modules(ROOT))
```
new:
```python
        paths, reason, jobs = scoped(env, args.base, args.head)
        if paths is not None and reason and full_scope_reason(env, paths) is None:
            paths = None  # a ci.yaml edit outside any job or in changes/report (R8)
        if paths is not None and any(is_unmapped(p, workspace_modules(ROOT)) for p in paths):
            paths = None  # mode e2e reports the path; every module runs (FR-002)
        selected = affected_modules(ROOT, paths)
    except Exception as exc:  # any doubt selects the full suite (FR-002)
        print(f"::warning::test selection failed, running everything: {exc}")
        paths, jobs = None, []
        selected = sorted(workspace_modules(ROOT))
```
old:
```python
        "lint-exclude": [{"module": m} for m in modules if m not in selected],
        "go-exclude": [{"module": m} for m in modules if m not in selected and m != "test/e2e"],
```
new:
```python
        # An edit inside the lint or go job of ci.yaml runs that job on every module.
        "lint-exclude": [{"module": m} for m in modules
                         if m not in selected and "lint" not in jobs],
        "go-exclude": [{"module": m} for m in modules
                       if m not in selected and m != "test/e2e" and "go" not in jobs],
```

## 2. `.github/workflows/ci.yaml`

2a. `changes` job outputs. old:
```yaml
      web-tests: ${{ steps.units.outputs.web-tests }}
```
new:
```yaml
      web-tests: ${{ steps.units.outputs.web-tests }}
      ci-jobs: ${{ steps.jobs.outputs.ci-jobs }}
```

2b. New step. Insert before `      - uses: dorny/paths-filter@` (right after the `units` step):
```yaml
      - name: map edited ci.yaml lines to their jobs
        id: jobs
        # A ci.yaml edit runs only the jobs it sits in, plus the jobs they need;
        # an edit outside any job or in changes/report sets `all` (spec 023 US3, OD-3).
        env:
          BASE_SHA: ${{ github.event.pull_request.base.sha || github.event.before }}
          HEAD_SHA: ${{ github.sha }}
          CI_FULL_RUN: "${{ contains(github.event.pull_request.labels.*.name, 'ci: full-run') }}"
        run: python3 hack/ci_scope.py jobs --base "$BASE_SHA" --head "$HEAD_SHA"
```

2c. paths-filter `ci` list. old:
```yaml
            ci:
              - '.github/workflows/ci.yaml'
              - '.github/actions/**'
```
new:
```yaml
            ci:
              # ci.yaml and .golangci.yml map to their own jobs (step `jobs`, OD-3).
              - '.github/actions/**'
```
old:
```yaml
              - 'test/e2e/buckets.sh'
              # golangci-lint walks up from `working-directory` to find this
              # config, so every `lint (…)` leg picks up an edit here even
              # though none of them declare it as an input. Force the lint
              # job on so a config change is tested against real code
              # instead of silently taking effect on the next unrelated PR.
              - '.golangci.yml'
            github:
```
new:
```yaml
              - 'test/e2e/buckets.sh'
              - 'test/e2e/suite-components.json'
            github:
```

2d. combine. old:
```yaml
          CI: ${{ steps.filter.outputs.ci == 'true' || steps.scope.outputs.all == 'true' || steps.e2e.outputs.all == 'true' }}
```
new:
```yaml
          CI: ${{ steps.filter.outputs.ci == 'true' || steps.scope.outputs.all == 'true' || steps.e2e.outputs.all == 'true' || steps.jobs.outputs.all == 'true' }}
```

2e. Summary env. old:
```yaml
          UNITS_OUTPUTS: ${{ toJSON(steps.units.outputs) }}
```
new:
```yaml
          UNITS_OUTPUTS: ${{ toJSON(steps.units.outputs) }}
          CI_JOBS: ${{ steps.jobs.outputs.ci-jobs }}
```

2f. Job gates (T045): run this script once from the repo root. It is a
deterministic rewrite: one `if:` line per listed job.
```sh
python3 - <<'EOF'
import re
from pathlib import Path
path = Path(".github/workflows/ci.yaml")
lines = path.read_text(encoding="utf-8").split("\n")
jobs = ["build-images", "build-images-arm64", "capture-sidecar-setcap-proof", "vuln-images",
        "docs", "codegen-drift", "submodule-freshness", "vuln-go", "lint", "go", "web",
        "web-e2e-mock", "helm", "standalone-compose", "chart-template", "workflow-lint",
        "go-e2e-unit", "api-postgres", "e2e-buckets", "e2e-go", "e2e-go-arm64",
        "e2e-multicluster", "e2e-multicluster-arm64", "e2e-upgrade", "e2e-upgrade-arm64",
        "e2e-web-live", "e2e-web-live-arm64", "e2e-game-bot", "e2e-postgres",
        "e2e-kind-ingress-smoke"]
done, job, in_jobs = set(), None, False
for i, line in enumerate(lines):
    if re.match(r"^[A-Za-z_][\w-]*:", line):
        in_jobs, job = line.startswith("jobs:"), None
    elif in_jobs and re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line):
        job = line.strip()[:-1]
    elif job in jobs and job not in done and line.startswith("    if: "):
        expr = line[len("    if: "):]
        assert not expr.startswith(">"), job
        lines[i] = (f"    if: ({expr}) || contains(fromJSON(needs.changes.outputs.ci-jobs || '[]'), "
                    f"'{job}')")
        done.add(job)
missing = set(jobs) - done
assert not missing, missing
path.write_text("\n".join(lines), encoding="utf-8")
print("gated", len(done))
EOF
```
Expected output: `gated 30`.

2g. `e2e-capture-overhead` (folded `if: >-`, edited by hand). old:
```yaml
      (github.event_name == 'workflow_dispatch' && needs.changes.outputs.e2e == 'true')
    runs-on: ubuntu-latest
    timeout-minutes: 45
```
new:
```yaml
      (github.event_name == 'workflow_dispatch' && needs.changes.outputs.e2e == 'true') ||
      contains(fromJSON(needs.changes.outputs.ci-jobs || '[]'), 'e2e-capture-overhead')
    runs-on: ubuntu-latest
    timeout-minutes: 45
```

## 3. Specs

3a. `specs/done_023-change-based-test-selection/contracts/ci-scope-cli.md`. old:
```markdown
| `ci-jobs` | JSON list of job ids selected by the `ci.yaml` line mapper | Folded into each job's gate |
```
new:
```markdown
| `ci-jobs` | JSON list of job ids selected by the `ci.yaml` line mapper | Emitted by mode `jobs`. Folded into each job's gate |
```
Insert before the line `## Mode \`bots\` — changed`:
```markdown
## Mode `jobs` (new)

Maps shared CI file changes to `ci.yaml` job ids (research R8, settled OD-3). The `jobs`
step of the `changes` job runs right after `units`. Outputs: `all` (boolean),
`reason` (`selection-rule change` when a changed line sits outside any job or inside
`changes` or `report`, else empty), and `ci-jobs`.

`ci-jobs` lists every job whose block holds a changed line that is neither blank nor a
comment, plus every job those jobs need. `.golangci.yml` adds `lint`, and any `.github/`
or `.golangci.yml` change adds `workflow-lint`. Lines are read from `git diff -U0`:
removed lines map against the base file and added lines against the head file. Each
listed job's gate is ORed with `contains(fromJSON(needs.changes.outputs.ci-jobs), '<job>')`.
Modes `modules`, `units` and `e2e` widen the job's own selection, so the job runs all of
its legs, packages or tests. Still forcing everything (FR-014): `hack/ci_scope.py`,
`hack/test_ci_scope.py`, `hack/check_changed_coverage.py` and its test, `Makefile`,
`go.work`, `go.work.sum`, `.github/actions/**`, `test/e2e/buckets.sh`,
`test/e2e/suite-components.json`.

```

3b. `specs/done_023-change-based-test-selection/tasks.md`: change `- [ ] T042` … `- [ ] T046`
(five lines) to `- [X]`.

## 4. Tests: `hack/test_ci_scope.py`

### 4a. New tests (no existing test changes)

Import: old
```python
                      verify_suites, go_packages, web_tests,
```
new
```python
                      verify_suites, go_packages, web_tests, CI_FILE, ci_blocks, ci_jobs,
                      widen_e2e, widen_units,
```

Insert right before `if __name__ == "__main__":`:

```python
GIT = ["git", "-c", "user.name=test", "-c", "user.email=test@example.com",
       "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"]
CI_FIXTURE = """name: ci
on:
  push:
permissions:
  contents: read
concurrency:
  group: ci
env:
  A: "1"
jobs:
  changes:
    runs-on: ubuntu-latest
    steps:
      - run: echo changes
  web:
    needs: [changes]
    steps:
      - run: echo web
  build-images:
    needs:
      - changes
    steps:
      - run: echo build
  e2e-go:
    needs: [changes, build-images]
    steps:
      - run: echo e2e
  report:
    needs:
      [
        changes,
        web,
      ]
    steps:
      - run: echo report
"""


class CiJobsTests(unittest.TestCase):
    """ci.yaml lines map to the jobs they sit in (spec 023 T042, research R8, OD-3)."""

    def setUp(self):
        """Create a git repository holding the fixture workflow."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")
        write_file(self.root, CI_FILE, CI_FIXTURE)
        self.base = self.commit()

    def git(self, *args):
        """Run git in the fixture repository and return its trimmed output."""
        return subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True,
                              text=True).stdout.strip()

    def commit(self):
        """Commit every change and return the new commit id."""
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", "change")
        return self.git("rev-parse", "HEAD")

    def edit(self, old, new):
        """Apply one edit to the base workflow and return ci_jobs for it."""
        self.git("reset", "-q", "--hard", self.base)
        text = (self.root / CI_FILE).read_text(encoding="utf-8")
        self.assertIn(old, text)
        write_file(self.root, CI_FILE, text.replace(old, new, 1))
        return ci_jobs(self.root, self.base, self.commit(), [CI_FILE])

    def test_needs_are_parsed_in_every_form(self):
        self.assertEqual(ci_blocks(CI_FIXTURE)[1], {
            "changes": set(), "web": {"changes"}, "build-images": {"changes"},
            "e2e-go": {"changes", "build-images"}, "report": {"changes", "web"}})

    def test_comment_or_blank_line_selects_only_workflow_lint(self):
        for new in ("      # note\n      - run: echo web\n", "\n      - run: echo web\n"):
            with self.subTest(new=new):
                self.assertEqual(self.edit("      - run: echo web\n", new),
                                 (["workflow-lint"], False))

    def test_step_change_selects_its_job(self):
        self.assertEqual(self.edit("echo web\n", "echo web2\n"), (["web", "workflow-lint"], False))

    def test_selected_job_brings_the_jobs_it_needs(self):
        self.assertEqual(self.edit("echo e2e\n", "echo e2e2\n"),
                         (["build-images", "e2e-go", "workflow-lint"], False))

    def test_removed_line_maps_against_the_base_file(self):
        self.assertEqual(self.edit("      - run: echo build\n", ""),
                         (["build-images", "workflow-lint"], False))

    def test_top_level_and_selection_job_edits_force_full_scope(self):
        for old, new in (("  push:\n", "  push:\n  workflow_dispatch:\n"),
                         ("contents: read", "contents: write"), ("group: ci", "group: ci2"),
                         ('A: "1"', 'A: "2"'), ("echo changes", "echo changes2"),
                         ("echo report", "echo report2")):
            with self.subTest(old=old):
                self.assertTrue(self.edit(old, new)[1])

    def test_golangci_selects_lint_legs_only(self):
        self.assertEqual(ci_jobs(self.root, self.base, self.base, [".golangci.yml"]),
                         (["lint", "workflow-lint"], False))
        self.assertIsNone(full_scope_reason({}, [".golangci.yml"]))
        self.assertIsNone(full_scope_reason({}, [CI_FILE]))

    def test_other_workflow_files_select_workflow_lint_only(self):
        for path in (".github/workflows/publish-edge.yaml", ".github/zizmor.yml",
                     ".github/dependabot.yml"):
            with self.subTest(path=path):
                self.assertEqual(ci_jobs(self.root, self.base, self.base, [path]),
                                 (["workflow-lint"], False))

    def test_selection_inputs_still_force_full_scope(self):
        for path in ("hack/ci_scope.py", "hack/test_ci_scope.py", "Makefile", "go.work",
                     "go.work.sum", ".github/actions/go-cache/action.yml",
                     "test/e2e/buckets.sh", "test/e2e/suite-components.json",
                     "hack/check_changed_coverage.py"):
            with self.subTest(path=path):
                self.assertEqual(full_scope_reason({}, [path]), "selection-rule change")


class CiJobsModuleTests(unittest.TestCase):
    """Shared CI config no longer selects every Go module (spec 023 T044)."""

    def setUp(self):
        """Create the shared Go workspace fixture for each test."""
        make_go_workspace(self)

    def test_ci_yaml_and_golangci_select_no_module(self):
        for path in (CI_FILE, ".golangci.yml", ".github/workflows/publish-edge.yaml"):
            with self.subTest(path=path):
                self.assertEqual(affected_modules(self.root, [path]), [])


class WidenTests(unittest.TestCase):
    """An edited job widens its own selection (spec 023 T045)."""

    def test_widen_e2e(self):
        values = {"e2e-run": {"operator": "^(TestA)$"}, "e2e-upgrade": False,
                  "e2e-web-live": False, "bot-matrix": {"test": []}, "bots-any": False}
        full = {"e2e-run": {"operator": "^(TestA|TestB)$", "api-auth": "^(TestC)$"},
                "bot-matrix": {"test": ["TestBot"]}, "bots-any": True}
        widened = widen_e2e(dict(values), full, ["e2e-go-arm64", "e2e-upgrade", "e2e-game-bot"])
        self.assertEqual(widened["e2e-run"], full["e2e-run"])
        self.assertTrue(widened["e2e-upgrade"])
        self.assertFalse(widened["e2e-web-live"])
        self.assertEqual((widened["bot-matrix"], widened["bots-any"]), ({"test": ["TestBot"]}, True))
        self.assertEqual(widen_e2e(dict(values), full, ["web"]), values)

    def test_widen_units(self):
        values = {"go-packages": {"api": ["./internal/a"], "lib": []}, "web-tests": []}
        self.assertEqual(widen_units(dict(values), ["go", "web"]),
                         {"go-packages": {"api": ["./..."], "lib": ["./..."]}, "web-tests": "all"})
        self.assertEqual(widen_units(dict(values), ["helm"]), values)


class EditedJobSummaryTests(unittest.TestCase):
    """The summary marks rows run by an edited ci.yaml job (spec 023 T045)."""

    def setUp(self):
        """Create the shared Go workspace fixture for each test."""
        make_go_workspace(self)

    def test_edited_job_row_runs(self):
        env = {
            "GITHUB_EVENT_NAME": "pull_request",
            "SCOPE_OUTPUTS": json.dumps({}), "FILTER_OUTPUTS": json.dumps({}),
            "GATE_OUTPUTS": json.dumps({}), "BOT_MATRIX": json.dumps({"test": []}),
            "CI_JOBS": json.dumps(["web", "workflow-lint"]),
        }
        with patch("ci_scope.ROOT", self.root):
            rows, _ = summarize(env, [CI_FILE], None)
        rows = {row["suite"]: row for row in rows}
        self.assertEqual(rows["web"]["decision"], "run")
        self.assertEqual(rows["web"]["reason"], "ci.yaml: lines in this job changed")
        self.assertEqual(rows["workflow lint"]["decision"], "run")
        self.assertEqual(rows["web e2e (mock)"]["decision"], "skip")
```

### 4b. Existing assertion change: apply ONLY after valgul's sign-off (card, 2026-10-22)

In `test_shared_inputs_and_dependency_changes_select_every_module`, old:
```python
                     "Makefile", ".golangci.yml", ".github/workflows/ci.yaml",
```
new:
```python
                     "Makefile",
```

## 5. Verification (compile only, rule 8)

```sh
python3 -c "import ast; [ast.parse(open(f).read()) for f in ('hack/ci_scope.py', 'hack/test_ci_scope.py')]"
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yaml'))"
```
