# Scout brief: e2e selection, PR 2 (T014–T030)

Written on branch `claude/project-thread-63yei6-pr2` (stacked on PR 1, #752) on
2026-10-22. Fix agents edit from this brief only (CLAUDE.md rule 18). Every code block
below is final text: paste it exactly, do not re-derive it.

## Design decisions in this PR (and why)

1. **New mode `e2e`, not new keys on mode `modules`.** Two existing tests in
   `hack/test_ci_scope.py` assert mode `modules`' exact output dict, so adding keys there
   breaks them. This is the same reason valgul chose option A (a new mode) for the PR 1
   summary. The new step `e2e` runs right after `bots` in the `changes` job.
2. **The e2e-go matrices are not trimmed with `exclude`.** GitHub runs `include` after
   `exclude`, and an `include` entry that matches no remaining combination becomes a new
   one. Every bucket has an `include` entry (its `parallel`/timeouts), so an excluded
   bucket would come back as a leg without `arch`. Instead each leg reads its bucket's
   regex from `e2e-run` into the job env `RUN_REGEX`; when it is empty every step is
   skipped, so the leg finishes in seconds without a cluster. Both arches share the
   `&e2e-go-steps` anchor, so FR-010 holds by construction.
3. **Mode `bots` is unchanged.** Mode `e2e` emits its own `bot-matrix` and `bots-any`;
   `e2e-game-bot` reads those. The summary still lists every `bot-fast` test from mode
   `bots`.
4. **Changed e2e Go files.** A changed `test/e2e/<file>.go` selects its own `Test`
   functions plus every test in a file that uses one of its top-level names (funcs by
   `name(`, types/vars/consts and method receiver types by `\bname\b`), following pure
   helper files transitively but stopping at files that define tests. A deleted file
   selects every e2e test. Any other `test/e2e/` path that no `paths` entry names selects
   every e2e test.
5. **Unmapped paths.** A path that is not `.md`, not in a known no-e2e set, not under a
   `universal` prefix, not named by a `paths` entry and not inside a component forces
   full scope with reason `unmapped path <p>`. `combine` folds `steps.e2e.outputs.all`
   into `CI`, so every job runs. The `e2e bucket coverage` job fails on a non-empty
   `unmapped` list (the "selection-rules" check in quickstart scenario 12).
6. **Kept as is:** `e2e-postgres` (still on the `e2e` gate), `e2e-multicluster` and
   `e2e-upgrade` run their whole bucket when selected, and on PRs `e2e-capture-overhead`
   still needs its spec 007 OD-2 path filter (selection only narrows it further).
7. **Agent is part of every test whose GameServer pod starts** (the agent sidecar runs
   in it, and backups quiesce through it). So an agent-only change also selects
   `upgrade`, `api-mods` and the server-owning tests in `api-roles`, unlike the rough
   expectation in quickstart scenario 1. Correctness wins over the expected skip.

## 1. New file `test/e2e/suite-components.json`

```json
{
  "version": 1,
  "universal": [
    "charts/",
    "containers/",
    "operator/api/v1alpha1/",
    "operator/config/",
    "deploy/kind/",
    "signing/",
    ".dockerignore",
    "test/e2e/env.go",
    "test/e2e/internal/fakeoidc/",
    "test/e2e/go.mod",
    "test/e2e/go.sum",
    "test/e2e/buckets.sh",
    "test/e2e/suite-components.json"
  ],
  "suites": [
    { "tests": "^TestHelmInstall_", "components": ["*"] },
    { "tests": "^TestCRD_Validation_", "components": ["operator"] },
    { "tests": "^TestGameServer_", "components": ["operator", "api", "agent"] },
    { "tests": "^TestGameServer_TunnelCredentialRefusalSurfaced$", "components": ["tunnel"] },
    { "tests": "^TestGameServer_IdleNeverSleepsWithoutAPlayerCount$", "components": ["sentinel"] },
    { "tests": "^(TestGameServer_NetworkCapture|TestNetworkCapture_)",
      "components": ["operator", "api", "agent", "capture-sidecar"] },
    { "tests": "^TestGameTemplate_", "components": ["operator", "api", "agent"] },
    { "tests": "^(TestModule_|TestModuleSource)", "components": ["operator", "api", "modules"] },
    { "tests": "^TestModule_FinalizerBlocksWhileTemplateInUse$", "components": ["agent"] },
    { "tests": "^TestModule_(ScaffoldAndPackage|SteamcmdScaffoldKeepsNonRootSecurity)$",
      "components": ["gp-module"] },
    { "tests": "^TestModuleSource_RejectsSSRFTarget$", "components": ["netguard"] },
    { "tests": "^(TestBackup|TestRestore_|TestE2E_ResticRepoReady$)",
      "components": ["operator", "api", "agent"] },
    { "tests": "^TestAddressPool_", "components": ["operator", "api", "agent"] },
    { "tests": "^TestAPI_", "components": ["operator", "api"] },
    { "tests": "^TestAPI_(Agent|ConsolePTYRoundTrip|LogsTailWS|Lifecycle|ModManifestInstallUpgrade|ModUpload|ModArchiveConfinement_|OwnerCollaboratorAccess|OwnerOnlyServerOperations_|EventStreamAndRoleEdits_)",
      "components": ["agent", "gameaction"] },
    { "tests": "^TestAPI_I18n_", "components": ["i18n"] },
    { "tests": "^TestAPI_I18n_ModuleLocales$", "components": ["modules"] },
    { "tests": "^TestAPI_ModuleUpload_ExtractionStaysWithinBudget$", "components": ["gp-module", "modules"] },
    { "tests": "^TestGameServer_.*(Bot_|_Persistence$|_RCON$|_Workshop$|_Mods$)",
      "components": ["agent", "modules"],
      "paths": ["test/e2e/internal/", "test/e2e/gamebot_helpers_e2e_test.go", "test/e2e/gameprobe_job.go"] },
    { "tests": "^TestGameServer_WakeOnConnect_", "components": ["sentinel", "gameproto"] },
    { "tests": "^TestGameServer_CaptureOverhead_Joined$",
      "components": ["capture-sidecar", "gameproto", "modules"],
      "paths": ["test/e2e/internal/minecraft-java/", "test/e2e/internal/probe/", "test/e2e/gameprobe_job.go"] },
    { "tests": "^TestMultiCluster_", "components": ["operator", "api", "agent", "capture-sidecar"] },
    { "tests": "^TestUpgrade_", "components": ["operator", "api", "agent"] },
    { "tests": "^TestTelemetry", "components": ["operator", "api", "telemetry-receiver", "telemetryschema"],
      "paths": ["test/e2e/telemetry_e2e_helpers_test.go", "test/e2e/telemetry_client_test.go", "test/e2e/telemetry_pow_e2e_test.go"] },
    { "tests": "^TestOptionalComponents_",
      "components": ["operator", "api", "agent", "mcp-server", "audit-syslog-bridge", "tunnel", "capture-sidecar"],
      "paths": ["test/e2e/fixtures/frps.yaml", "test/e2e/fixtures/syslog-sink.yaml"] },
    { "tests": "^TestCISelection_", "components": [],
      "paths": ["hack/ci_scope.py", ".github/workflows/ci.yaml", "test/e2e/buckets.sh", "test/e2e/suite-components.json"] }
  ],
  "unexercised": [
    { "component": "svcutil", "reason": "library only; the suites of every service that imports it run through the module closure" },
    { "component": "test/e2e", "reason": "the e2e suite itself; a changed test file selects its own tests and the tests that use its helpers" },
    { "component": "web", "reason": "dashboard; exercised by e2e web live (Playwright), not by a Go e2e test" }
  ]
}
```

## 2. `hack/ci_scope.py`

### 2a. After the `E2E_BUCKETS = [...]` constant (line 30–31), insert

```python
SUITES_FILE = "test/e2e/suite-components.json"
# Paths no Go e2e test reads. Their own jobs keep their paths-filter gates.
NO_E2E_FILES = {
    "design.pen", "LICENSE", ".claudeignore", ".coderabbit.yaml", ".editorconfig",
    ".gitattributes", ".gitignore", ".gitmodules", ".hadolint.yaml",
    "deploy/kind/up.sh", "deploy/kind/down.sh",
    "test/e2e/joincoverage.sh", "test/e2e/joincoverage_test.sh",
    "test/e2e/lint-gate-verify.sh", "test/e2e/lint-gate-verify_test.sh",
}
NO_E2E_DIRS = ("docs", "specs", "assets", "website", "hack", ".github", ".claude", ".devcontainer",
               ".githooks", ".jules", ".specify", "deploy/standalone", "test/e2e/testdata")
# The dashboard live run drives these components through a real cluster.
WEB_LIVE_COMPONENTS = {"web", "api", "operator", "agent"}
# Component ids beside the go.work modules (data-model.md Component).
EXTRA_COMPONENTS = ("web", "charts", "deploy-kind", "modules", "e2e-harness")
```

### 2b. After `table_cell()` (ends just before `def summarize`), insert

```python
def decoded(values, key, default=None):
    """Return a step output decoded from the JSON that emit() wrote, or default."""
    value = values.get(key)
    if value is None or value == "":
        return default
    if not isinstance(value, str):
        return value
    try:
        return json.loads(value)
    except ValueError:
        return value


def under(path, base):
    """Return True when path equals base or lies beneath it."""
    return path == base or path.startswith(base + "/")


def prefixed(path, prefix):
    """Match a suite-components prefix: a trailing slash means a directory, else one path."""
    return path.startswith(prefix) if prefix.endswith("/") else path == prefix


def load_suites(root):
    """Return the parsed test/e2e/suite-components.json."""
    return json.loads((root / SUITES_FILE).read_text(encoding="utf-8"))


def bucket_lists(root):
    """Return {bucket: [test, ...]} from test/e2e/buckets.sh, in its listed order."""
    def run(*args):
        """Run buckets.sh with args and return its output words."""
        return subprocess.run(["bash", "test/e2e/buckets.sh", *args], cwd=root, check=True,
                              capture_output=True, text=True).stdout.split()
    return {name: run("list", name) for name in run("buckets")}


def go_tests(text):
    """Return the Test functions a Go file defines, without TestMain."""
    return [name for name in re.findall(r"^func (Test[A-Za-z0-9_]*)\(", text, re.MULTILINE)
            if name != "TestMain"]


def helper_pattern(text):
    """Return a regex matching uses of a Go file's top-level names, or None if it has none.

    Functions match as calls; types, vars, consts and the receiver types of methods
    match as words, so a changed method selects the users of its type.
    """
    funcs = set(re.findall(r"^func ([A-Za-z_]\w*)", text, re.MULTILINE))
    funcs -= set(go_tests(text)) | {"TestMain"}
    words = set(re.findall(r"^func \(\s*\w*\s*\*?\s*([A-Za-z_]\w*)", text, re.MULTILINE))
    words |= set(re.findall(r"^(?:type|var|const)\s+([A-Za-z_]\w*)", text, re.MULTILINE))
    for block in re.findall(r"^(?:type|var|const)\s*\((.*?)^\)", text,
                            re.MULTILINE | re.DOTALL):
        words |= set(re.findall(r"^\s+([A-Za-z_]\w*)", block, re.MULTILINE))
    parts = ([re.escape(name) + r"\(" for name in sorted(funcs)]
             + [re.escape(name) + r"\b" for name in sorted(words)])
    return re.compile(r"\b(?:" + "|".join(parts) + ")") if parts else None


def e2e_file_tests(root, path):
    """Return the tests a changed test/e2e/<file>.go can affect, or None for every test.

    That is the file's own tests plus every test in a file that uses one of its
    top-level names, following pure helper files (no tests) transitively. A
    deleted file returns None.
    """
    texts = {source.name: source.read_text(encoding="utf-8")
             for source in sorted((root / "test/e2e").glob("*.go"))}
    start = Path(path).name
    if start not in texts:
        return None
    seen, queue, tests = {start}, [start], set()
    while queue:
        name = queue.pop()
        own = set(go_tests(texts[name]))
        tests |= own
        if own and name != start:
            continue
        pattern = helper_pattern(texts[name])
        if pattern is None:
            continue
        for other, text in texts.items():
            if other not in seen and pattern.search(text):
                seen.add(other)
                queue.append(other)
    return tests


def select_e2e(root, paths, reason, suites, buckets):
    """Select e2e tests for a change (spec 023 US1).

    Returns (selected, unmapped, reason, touched): selected maps each chosen test
    to the changed paths that chose it, reason is set when every test runs because
    of the event or an unmapped path, and touched holds the changed component ids
    ("*" when a universal path changed).
    """
    tests = list(dict.fromkeys(name for names in buckets.values() for name in names))
    if reason is None and paths is None:
        reason = "unknown base"
    if reason:
        return {name: [] for name in tests}, [], reason, {"*"}
    modules = workspace_modules(root)
    entries = suites["suites"]
    picked, components, every, unmapped = {}, {}, [], []

    def matching(entry):
        """Return the tests one suite entry's pattern matches."""
        return [name for name in tests if re.search(entry["tests"], name)]

    def pick(names, path):
        """Record that path selects each test in names."""
        for name in names:
            picked.setdefault(name, []).append(path)

    for path in paths:
        if path.endswith(".md") or path in NO_E2E_FILES:
            continue
        if any(prefixed(path, prefix) for prefix in suites["universal"]):
            every.append(path)
            continue
        named = [entry for entry in entries
                 if any(prefixed(path, prefix) for prefix in entry.get("paths", []))]
        for entry in named:
            pick(matching(entry), path)
        if under(path, "test/e2e"):
            if path.count("/") == 2 and path.endswith(".go"):
                own = e2e_file_tests(root, path)
                if own is None:
                    every.append(path)
                else:
                    pick([name for name in tests if name in own], path)
            elif not named:
                every.append(path)
            continue
        if named or any(under(path, directory) for directory in NO_E2E_DIRS):
            continue
        owner = next((module for module in modules if under(path, module)), None)
        if owner:
            components.setdefault(owner, []).append(path)
        elif under(path, "web"):
            components.setdefault("web", []).append(path)
        elif under(path, "modules"):
            components.setdefault("modules", []).append(path)
        else:
            unmapped.append(path)
    if unmapped:
        return {name: [] for name in tests}, unmapped, f"unmapped path {unmapped[0]}", {"*"}
    if every:
        return {name: list(every) for name in tests}, [], None, {"*"}
    module_paths = [path for component, owned in components.items()
                    if component in modules or component == "modules" for path in owned]
    if module_paths:
        # Library changes reach the binaries that import them (research R3).
        for module in affected_modules(root, module_paths):
            components.setdefault(module, module_paths)
    for entry in entries:
        wanted = set(entry.get("components", []))
        if "*" in wanted:
            hit = [path for owned in components.values() for path in owned]
        else:
            hit = [path for component in sorted(wanted & components.keys())
                   for path in components[component]]
        for path in dict.fromkeys(hit):
            pick(matching(entry), path)
    selected = {name: list(dict.fromkeys(picked[name])) for name in tests if name in picked}
    return selected, [], None, set(components)


def e2e_outputs(root, paths, reason):
    """Return the outputs of mode e2e: per-bucket -run regexes, job switches, bot matrix."""
    buckets = bucket_lists(root)
    selected, unmapped, reason, touched = select_e2e(root, paths, reason, load_suites(root),
                                                     buckets)

    def chosen(bucket):
        """Return the selected tests of one bucket, in buckets.sh order."""
        return [name for name in buckets.get(bucket, []) if name in selected]

    def describe(names, total):
        """Return 'N of M tests' plus the changed paths that chose them."""
        files = list(dict.fromkeys(path for name in names for path in selected[name]))
        text = f"{len(names)} of {total} tests"
        return text + (f"; {cite(files)}" if files else "")

    run, why = {}, {}
    for bucket in E2E_BUCKETS + ["ratelimit", "multicluster", "upgrade", "capture-overhead"]:
        names = chosen(bucket)
        if names:
            why[bucket] = describe(names, len(buckets[bucket]))
            if bucket in E2E_BUCKETS or bucket == "ratelimit":
                run[bucket] = "^(" + "|".join(names) + ")$"
    bots = chosen("bot-fast")
    for name in bots:
        why[name] = describe([name], 1)
    return {
        "all": bool(reason),
        "reason": reason or "",
        "unmapped": unmapped,
        "e2e-run": run,
        "e2e-why": why,
        "e2e-multicluster": bool(chosen("multicluster")),
        "e2e-upgrade": bool(chosen("upgrade")),
        "e2e-capture-overhead": bool(chosen("capture-overhead")),
        "e2e-web-live": bool(reason) or "*" in touched or bool(touched & WEB_LIVE_COMPONENTS),
        "bot-matrix": {"test": bots},
        "bots-any": bool(bots),
    }


def verify_suites(suites, buckets, modules):
    """Return the violations of contracts/suite-components.md rules 1, 2 and 5."""
    errors = []
    known = set(modules) | set(EXTRA_COMPONENTS) | {"*"}
    entries = suites.get("suites", [])
    patterns = []
    for entry in entries:
        try:
            patterns.append(re.compile(entry["tests"]))
        except (KeyError, TypeError, re.error) as exc:
            errors.append(f"entry {entry!r}: bad tests pattern: {exc}")
            continue
        if not entry.get("components") and not entry.get("paths"):
            errors.append(f"entry {entry['tests']}: names no component and no path")
        for component in entry.get("components", []):
            if component not in known:
                errors.append(f"entry {entry['tests']}: unknown component {component}")
    for bucket, names in buckets.items():
        for name in names:
            if not any(pattern.search(name) for pattern in patterns):
                errors.append(f"test {name} (bucket {bucket}) matches no entry")
    named = {component for entry in entries for component in entry.get("components", [])}
    named |= {item.get("component") for item in suites.get("unexercised", [])}
    if any(prefix.startswith("charts/") for prefix in suites.get("universal", [])):
        named.add("charts")
    for component in sorted(set(modules) | {"web", "charts"}):
        if component not in named:
            errors.append(f"component {component} is named by no entry and not listed "
                          "in unexercised")
    for item in suites.get("unexercised", []):
        if item.get("component") not in known or not item.get("reason"):
            errors.append(f"unexercised entry {item!r} needs a known component and a reason")
    return errors
```

### 2c. In `summarize()`

Replace

```python
    bots = load_json(env, "BOT_MATRIX")
```

with

```python
    bots = load_json(env, "BOT_MATRIX")
    e2e = load_json(env, "E2E_OUTPUTS")
    # Without the e2e step's outputs every e2e suite follows the coarse e2e gate.
    narrowed = bool(e2e)
    e2e_run = decoded(e2e, "e2e-run", {})
    e2e_why = decoded(e2e, "e2e-why", {})
    e2e_bots = decoded(e2e, "bot-matrix", {}).get("test", [])
```

Replace the block from `    for bucket in E2E_BUCKETS:` through the end of the
`add("e2e capture overhead", ...)` call (the five lines that end with
`filtered("capture_overhead"))`), currently:

```python
    for bucket in E2E_BUCKETS:
        name = "e2e api-auth + ratelimit tail" if bucket == "api-auth" else f"e2e {bucket}"
        add(f"{name} (amd64, arm64)", gate("e2e"), e2e_feed())
    add("e2e multicluster (amd64, arm64)", gate("e2e"), e2e_feed())
    add("e2e upgrade (amd64, arm64)", gate("e2e"), e2e_feed())
    add("e2e postgres (amd64)", gate("e2e"), e2e_feed())
    for test in jlist(bots, "test"):
        add(f"e2e game bot / {test}", gate("e2e"), e2e_feed())
    add("e2e web live (amd64, arm64)", gate("weblive"), e2e_feed() + filtered("web"))
    add("e2e capture overhead",
        (event == "pull_request" and gate("capture_overhead"))
        or (event in ("push", "workflow_dispatch") and gate("e2e")),
        filtered("capture_overhead"))
```

with

```python
    def e2e_row(suite, gate_on, picked, key, feed):
        """Add an e2e row from the coarse gate and, when present, the e2e step's pick."""
        if narrowed and gate_on and not picked:
            add(suite, False, skip_reason="no test in this suite is affected")
        else:
            add(suite, gate_on, feed, run_reason=e2e_why.get(key, "") if narrowed else "")

    for bucket in E2E_BUCKETS:
        name = "e2e api-auth + ratelimit tail" if bucket == "api-auth" else f"e2e {bucket}"
        picked = bucket in e2e_run or (bucket == "api-auth" and "ratelimit" in e2e_run)
        e2e_row(f"{name} (amd64, arm64)", gate("e2e"), picked, bucket, e2e_feed())
    e2e_row("e2e multicluster (amd64, arm64)", gate("e2e"), flag(e2e, "e2e-multicluster"),
            "multicluster", e2e_feed())
    e2e_row("e2e upgrade (amd64, arm64)", gate("e2e"), flag(e2e, "e2e-upgrade"), "upgrade",
            e2e_feed())
    add("e2e postgres (amd64)", gate("e2e"), e2e_feed())
    for test in jlist(bots, "test"):
        e2e_row(f"e2e game bot / {test}", gate("e2e"), test in e2e_bots, test, e2e_feed())
    e2e_row("e2e web live (amd64, arm64)", gate("weblive"), flag(e2e, "e2e-web-live"), "",
            e2e_feed() + filtered("web"))
    e2e_row("e2e capture overhead",
            (event == "pull_request" and gate("capture_overhead"))
            or (event in ("push", "workflow_dispatch") and gate("e2e")),
            event != "pull_request" or flag(e2e, "e2e-capture-overhead"), "capture-overhead",
            filtered("capture_overhead"))
```

### 2d. In `main()`

Replace

```python
    parser.add_argument("mode", choices=("modules", "images", "dockerfiles", "bots", "summary"))
```

with

```python
    parser.add_argument("mode", choices=("modules", "images", "dockerfiles", "bots", "summary",
                                         "e2e", "verify-suites"))
```

In the `summary` branch, replace

```python
            reason = full_scope_reason(env, paths)
            if reason is None and flag(load_json(env, "SCOPE_OUTPUTS"), "all"):
```

with

```python
            reason = full_scope_reason(env, paths) or decoded(load_json(env, "E2E_OUTPUTS"),
                                                              "reason") or None
            if reason is None and flag(load_json(env, "SCOPE_OUTPUTS"), "all"):
```

Directly before the line `    # PRs use affected work; every master push, manual run and ci: full-run`
insert

```python
    if args.mode == "verify-suites":
        errors = verify_suites(load_suites(ROOT), bucket_lists(ROOT), workspace_modules(ROOT))
        for error in errors:
            print(f"::error::{error}")
        if errors:
            raise SystemExit(1)
        print("suite components OK")
        return
    if args.mode == "e2e":
        env = os.environ
        try:
            event_reason = full_scope_reason(env, [])
            paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
            values = e2e_outputs(ROOT, paths, full_scope_reason(env, paths))
        except Exception as exc:  # any doubt selects every e2e test (FR-002)
            print(f"::warning::e2e selection failed, running every e2e test: {exc}")
            values = e2e_outputs(ROOT, None, f"error: {exc}")
        emit(values)
        return
```

## 3. `test/e2e/buckets.sh`

In `bucket_operator()`, after the line `TestE2E_ResticRepoReady` (line 108) and before
`EOF`, add:

```
TestCISelection_AgentOnlyChange
TestCISelection_SentinelOnlyChange
TestCISelection_ChartChangeRunsEverySuite
TestCISelection_E2ETestFileSelectsItsTests
TestCISelection_UnmappedPathRunsEverything
TestCISelection_FullRunLabel
```

No other `buckets.sh` change.

## 4. New file `test/e2e/ci_selection_e2e_test.go`

```go
//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The TestCISelection_* tests prove that `hack/ci_scope.py e2e` turns a
// one-commit change into the expected e2e selection (spec 023 OD-4, research
// R13). Each one copies this checkout with `git archive HEAD`, commits a base
// and a head, and runs the selector on them. They need git and python3 only,
// create no cluster resources and never log in, so they live in the operator
// bucket.

// ciSelectionBuckets mirrors E2E_BUCKETS in hack/ci_scope.py (the e2e-go matrix).
var ciSelectionBuckets = []string{
	"operator", "api-auth", "api-roles", "api-rbac", "api-agent", "api-mods",
	"telemetry", "optional-components",
}

// ciSelection is the decoded output of one `ci_scope.py e2e` run.
type ciSelection struct {
	Reason string
	Run    map[string]string
	Bots   []string
	Flags  map[string]bool
}

// ciGit runs git in dir with a fixed identity and returns trimmed stdout.
func ciGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.name=ci-selection",
		"-c", "user.email=ci-selection@example.invalid", "-c", "commit.gpgsign=false"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ciSelectionCopy exports HEAD into a fresh repository and commits it as the
// base. Large paths no selection rule reads are left out to keep it fast.
func ciSelectionCopy(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	dir := t.TempDir()
	script := `set -o pipefail; git -C "$1" archive --format=tar HEAD -- . ` +
		`':(exclude)design.pen' ':(exclude)assets' ':(exclude)docs' ':(exclude)specs' ` +
		`':(exclude)web' | tar -x -C "$2"`
	if out, err := exec.Command("bash", "-c", script, "bash", root, dir).CombinedOutput(); err != nil {
		t.Fatalf("git archive HEAD: %v\n%s", err, out)
	}
	ciGit(t, dir, "init", "-q")
	ciGit(t, dir, "add", "-A")
	ciGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

// ciSelectionAppend appends text to a file in the copy, creating it if needed.
func ciSelectionAppend(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

// ciSelectionRun commits edit as the head and returns the selector's output.
func ciSelectionRun(t *testing.T, dir string, fullRunLabel bool, edit func(dir string)) ciSelection {
	t.Helper()
	base := ciGit(t, dir, "rev-parse", "HEAD")
	if edit != nil {
		edit(dir)
	}
	ciGit(t, dir, "add", "-A")
	ciGit(t, dir, "commit", "-q", "--allow-empty", "-m", "head")
	head := ciGit(t, dir, "rev-parse", "HEAD")
	output := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.Command("python3", "hack/ci_scope.py", "e2e", "--base", base, "--head", head)
	cmd.Dir = dir
	// A clean environment: the e2e job's own GITHUB_EVENT_NAME and GITHUB_REF
	// would turn a master push into a full-scope answer here.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"GITHUB_OUTPUT=" + output}
	if fullRunLabel {
		cmd.Env = append(cmd.Env, "CI_FULL_RUN=true")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ci_scope.py e2e: %v\n%s", err, out)
	}
	return ciSelectionParse(t, output)
}

// ciSelectionParse decodes the key=<json> lines ci_scope.py writes to GITHUB_OUTPUT.
func ciSelectionParse(t *testing.T, path string) ciSelection {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw := map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed output line %q", line)
		}
		raw[key] = json.RawMessage(value)
	}
	decode := func(key string, into any) {
		if err := json.Unmarshal(raw[key], into); err != nil {
			t.Fatalf("decode %s=%s: %v", key, raw[key], err)
		}
	}
	sel := ciSelection{Flags: map[string]bool{}}
	decode("reason", &sel.Reason)
	decode("e2e-run", &sel.Run)
	var bots struct {
		Test []string `json:"test"`
	}
	decode("bot-matrix", &bots)
	sel.Bots = bots.Test
	for _, key := range []string{"all", "e2e-multicluster", "e2e-upgrade", "e2e-web-live",
		"e2e-capture-overhead", "bots-any"} {
		var value bool
		decode(key, &value)
		sel.Flags[key] = value
	}
	return sel
}

// ciSelectionBucket runs buckets.sh in this package directory.
func ciSelectionBucket(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("bash", append([]string{"buckets.sh"}, args...)...).Output()
	if err != nil {
		t.Fatalf("buckets.sh %s: %v", strings.Join(args, " "), err)
	}
	return strings.Fields(string(out))
}

// ciSelectionSuiteTests lists the Test functions compiled into this package.
func ciSelectionSuiteTests(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test files: %v", err)
	}
	pattern := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	var names []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			if match[1] != "TestMain" {
				names = append(names, match[1])
			}
		}
	}
	return names
}

// ciSelectionCheckRegexes asserts every emitted -run regex runs at least one
// test of this package and nothing outside its own bucket.
func ciSelectionCheckRegexes(t *testing.T, sel ciSelection) {
	t.Helper()
	suite := ciSelectionSuiteTests(t)
	for bucket, expr := range sel.Run {
		re, err := regexp.Compile(expr)
		if err != nil {
			t.Errorf("bucket %s: bad regex %q: %v", bucket, expr, err)
			continue
		}
		members := map[string]bool{}
		for _, name := range ciSelectionBucket(t, "list", bucket) {
			members[name] = true
		}
		matched := 0
		for _, name := range suite {
			if !re.MatchString(name) {
				continue
			}
			matched++
			if !members[name] {
				t.Errorf("bucket %s regex %q matches %s, which is not in that bucket", bucket, expr, name)
			}
		}
		if matched == 0 {
			t.Errorf("bucket %s regex %q matches no test in this package", bucket, expr)
		}
	}
}

// ciSelectionRequireAll asserts every e2e-go bucket runs its whole buckets.sh regex.
func ciSelectionRequireAll(t *testing.T, sel ciSelection) {
	t.Helper()
	for _, bucket := range ciSelectionBuckets {
		want := strings.Join(ciSelectionBucket(t, "regex", bucket), "")
		if got := sel.Run[bucket]; got != want {
			t.Errorf("bucket %s regex = %q, want the full bucket %q", bucket, got, want)
		}
	}
	for _, key := range []string{"e2e-multicluster", "e2e-upgrade", "e2e-web-live", "bots-any"} {
		if !sel.Flags[key] {
			t.Errorf("%s = false, want true when every suite runs", key)
		}
	}
}

func TestCISelection_AgentOnlyChange(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "agent/internal/console/console.go"), "\n// ci selection probe\n")
	})
	if sel.Reason != "" {
		t.Fatalf("reason = %q, want a partial selection", sel.Reason)
	}
	if !strings.Contains(sel.Run["api-agent"], "TestAPI_ConsolePTYRoundTrip") {
		t.Errorf("api-agent regex %q lacks TestAPI_ConsolePTYRoundTrip", sel.Run["api-agent"])
	}
	for _, bucket := range []string{"api-auth", "ratelimit", "telemetry"} {
		if expr, ok := sel.Run[bucket]; ok {
			t.Errorf("%s selected for an agent-only change: %s", bucket, expr)
		}
	}
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_SentinelOnlyChange(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "sentinel/main.go"), "\n// ci selection probe\n")
	})
	if sel.Reason != "" {
		t.Fatalf("reason = %q, want a partial selection", sel.Reason)
	}
	for _, bucket := range []string{"api-auth", "api-roles", "api-rbac", "api-agent", "telemetry"} {
		if expr, ok := sel.Run[bucket]; ok {
			t.Errorf("%s selected for a sentinel-only change: %s", bucket, expr)
		}
	}
	wake := false
	for _, name := range sel.Bots {
		if name == "TestGameServer_WakeOnConnect_LoginWakes" {
			wake = true
		}
	}
	if !wake {
		t.Errorf("bot matrix %v lacks TestGameServer_WakeOnConnect_LoginWakes", sel.Bots)
	}
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_ChartChangeRunsEverySuite(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "charts/gameplane/values.yaml"), "\n# ci selection probe\n")
	})
	if sel.Reason != "" {
		t.Fatalf("reason = %q, want a partial scope that still selects every suite", sel.Reason)
	}
	ciSelectionRequireAll(t, sel)
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_E2ETestFileSelectsItsTests(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "test/e2e/telemetry_e2e_test.go"), "\n// ci selection probe\n")
	})
	if got, want := sel.Run["telemetry"], "^(TestTelemetryLifecycle)$"; got != want {
		t.Errorf("telemetry regex = %q, want %q", got, want)
	}
	if expr, ok := sel.Run["api-rbac"]; ok {
		t.Errorf("api-rbac selected for a telemetry test edit: %s", expr)
	}
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_UnmappedPathRunsEverything(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "tools/x.sh"), "#!/bin/sh\n")
	})
	if want := "unmapped path tools/x.sh"; sel.Reason != want {
		t.Fatalf("reason = %q, want %q", sel.Reason, want)
	}
	if !sel.Flags["all"] {
		t.Errorf("all = false, want true for an unmapped path")
	}
	ciSelectionRequireAll(t, sel)
}

func TestCISelection_FullRunLabel(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, true, nil)
	if want := "label ci: full-run"; sel.Reason != want {
		t.Fatalf("reason = %q, want %q", sel.Reason, want)
	}
	ciSelectionRequireAll(t, sel)
}
```

## 5. `hack/test_ci_scope.py`

Replace the import lines 1–11 header imports:

```python
import json
import tempfile
```

with

```python
import json
import subprocess
import tempfile
```

and

```python
from ci_scope import (E2E_BUCKETS, FORCE_ALL_FILES, affected_modules, bot_matrix, changed_paths,
                      full_scope_reason, image_inputs, main, publish_paths, select_images, summarize)
```

with

```python
from ci_scope import (E2E_BUCKETS, FORCE_ALL_FILES, ROOT, affected_modules, bot_matrix,
                      bucket_lists, changed_paths, e2e_outputs, full_scope_reason, image_inputs,
                      load_suites, main, publish_paths, select_images, summarize, verify_suites,
                      workspace_modules)
```

Insert these three classes directly before the final `if __name__ == "__main__":` line
(keep two blank lines between classes):

```python
class E2ESelectionTests(unittest.TestCase):
    """Spec 023 US1 acceptance scenarios 1-4 against this repository's suite map (T014)."""

    def select(self, *paths):
        """Return the e2e outputs for a partial-scope change to paths."""
        return e2e_outputs(ROOT, list(paths), None)

    def test_agent_only_change_selects_agent_tests_and_skips_auth_and_telemetry(self):
        values = self.select("agent/internal/console/console.go")
        self.assertEqual(values["reason"], "")
        self.assertIn("TestAPI_ConsolePTYRoundTrip", values["e2e-run"]["api-agent"])
        for bucket in ("api-auth", "ratelimit", "telemetry"):
            self.assertNotIn(bucket, values["e2e-run"])

    def test_sentinel_only_change_skips_api_suites_and_keeps_wake_tests(self):
        values = self.select("sentinel/main.go")
        for bucket in ("api-auth", "api-roles", "api-rbac", "api-agent", "telemetry"):
            self.assertNotIn(bucket, values["e2e-run"])
        self.assertIn("TestGameServer_WakeOnConnect_LoginWakes", values["bot-matrix"]["test"])
        self.assertNotIn("TestGameServer_MinecraftJavaBot_Joined", values["bot-matrix"]["test"])

    def test_chart_change_selects_every_suite(self):
        values = self.select("charts/gameplane/values.yaml")
        buckets = bucket_lists(ROOT)
        for bucket in E2E_BUCKETS:
            self.assertEqual(values["e2e-run"][bucket], "^(" + "|".join(buckets[bucket]) + ")$")
        for key in ("e2e-multicluster", "e2e-upgrade", "e2e-capture-overhead", "e2e-web-live",
                    "bots-any"):
            self.assertTrue(values[key], key)

    def test_changed_e2e_test_file_selects_its_own_tests(self):
        values = self.select("test/e2e/telemetry_e2e_test.go")
        self.assertEqual(values["e2e-run"].get("telemetry"), "^(TestTelemetryLifecycle)$")
        self.assertNotIn("api-rbac", values["e2e-run"])

    def test_unmapped_path_forces_full_scope(self):
        values = self.select("tools/x.sh")
        self.assertEqual(values["reason"], "unmapped path tools/x.sh")
        self.assertEqual(values["unmapped"], ["tools/x.sh"])
        self.assertTrue(values["all"])

    def test_docs_only_change_selects_no_e2e_test(self):
        values = self.select("docs/architecture.md", "specs/done_023-change-based-test-selection/plan.md")
        self.assertEqual(values["e2e-run"], {})
        self.assertFalse(values["bots-any"])


class VerifySuitesTests(unittest.TestCase):
    """verify-suites rules 1, 2 and 5 from contracts/suite-components.md (T015)."""

    MODULES = {"api": "example.com/api", "agent": "example.com/agent"}

    def suites(self, **overrides):
        """Return a valid minimal suite map, with top-level keys replaced by overrides."""
        value = {
            "universal": ["charts/"],
            "suites": [{"tests": "^TestAPI_", "components": ["api"]},
                       {"tests": "^TestAgent_", "components": ["agent"]}],
            "unexercised": [{"component": "web", "reason": "dashboard"}],
        }
        value.update(overrides)
        return value

    def test_valid_map_has_no_errors(self):
        self.assertEqual(verify_suites(self.suites(), {"b": ["TestAPI_X", "TestAgent_Y"]},
                                       self.MODULES), [])

    def test_unmatched_test_is_reported(self):
        errors = verify_suites(self.suites(), {"b": ["TestAPI_X", "TestOther"]}, self.MODULES)
        self.assertTrue(any("TestOther" in error for error in errors), errors)

    def test_unknown_component_is_reported(self):
        suites = self.suites(suites=[{"tests": "^Test", "components": ["api", "agent", "nope"]}])
        errors = verify_suites(suites, {"b": ["TestAPI_X"]}, self.MODULES)
        self.assertTrue(any("unknown component nope" in error for error in errors), errors)

    def test_unnamed_code_component_is_reported_unless_unexercised(self):
        suites = self.suites(suites=[{"tests": "^Test", "components": ["api"]}])
        errors = verify_suites(suites, {"b": ["TestAPI_X"]}, self.MODULES)
        self.assertTrue(any("component agent" in error for error in errors), errors)
        suites["unexercised"].append({"component": "agent", "reason": "no e2e image"})
        self.assertEqual(verify_suites(suites, {"b": ["TestAPI_X"]}, self.MODULES), [])

    def test_repository_suite_map_is_valid(self):
        self.assertEqual(verify_suites(load_suites(ROOT), bucket_lists(ROOT),
                                       workspace_modules(ROOT)), [])


class E2ERunTests(unittest.TestCase):
    """The e2e-run regexes that replace buckets.sh regex in the e2e jobs (T016)."""

    def test_full_scope_regex_equals_buckets_sh_regex(self):
        values = e2e_outputs(ROOT, None, "master push")
        self.assertEqual(values["reason"], "master push")
        for bucket in E2E_BUCKETS + ["ratelimit"]:
            expected = subprocess.run(["bash", "test/e2e/buckets.sh", "regex", bucket], cwd=ROOT,
                                      check=True, capture_output=True, text=True).stdout.strip()
            self.assertEqual(values["e2e-run"][bucket], expected)

    def test_partial_regex_lists_only_selected_tests_of_its_bucket(self):
        values = e2e_outputs(ROOT, ["agent/internal/console/console.go"], None)
        buckets = bucket_lists(ROOT)
        for bucket, regex in values["e2e-run"].items():
            self.assertTrue(regex.startswith("^(") and regex.endswith(")$"), regex)
            self.assertLessEqual(set(regex[2:-2].split("|")), set(buckets[bucket]))
        operator = values["e2e-run"]["operator"][2:-2].split("|")
        self.assertLess(len(operator), len(buckets["operator"]))
        self.assertNotIn("TestCRD_Validation_GameServerWithoutTemplate", operator)

    def test_ratelimit_tail_runs_only_when_its_test_is_selected(self):
        self.assertNotIn("ratelimit",
                         e2e_outputs(ROOT, ["agent/internal/console/console.go"], None)["e2e-run"])
        self.assertIn("ratelimit", e2e_outputs(ROOT, ["api/cmd/main.go"], None)["e2e-run"])
```

## 6. `.github/workflows/ci.yaml`

6a. `changes` job `outputs:` — after the line
`      selection: ${{ steps.summary.outputs.selection }}` add:

```yaml
      e2e-run: ${{ steps.e2e.outputs.e2e-run }}
      e2e-unmapped: ${{ steps.e2e.outputs.unmapped }}
      e2e-multicluster: ${{ steps.e2e.outputs.e2e-multicluster }}
      e2e-upgrade: ${{ steps.e2e.outputs.e2e-upgrade }}
      e2e-web-live: ${{ steps.e2e.outputs.e2e-web-live }}
      e2e-capture-overhead: ${{ steps.e2e.outputs.e2e-capture-overhead }}
      e2e-bot-matrix: ${{ steps.e2e.outputs.bot-matrix }}
      e2e-bots-any: ${{ steps.e2e.outputs.bots-any }}
```

6b. After the `bots` step (the two lines
`        id: bots` / `        run: python3 hack/ci_scope.py bots`) insert:

```yaml
      - name: select the e2e tests the change affects
        id: e2e
        # Per-test e2e selection from test/e2e/suite-components.json (spec 023 US1).
        # An unmapped path sets `all`, which `combine` folds into CI below.
        env:
          BASE_SHA: ${{ github.event.pull_request.base.sha || github.event.before }}
          HEAD_SHA: ${{ github.sha }}
          CI_FULL_RUN: "${{ contains(github.event.pull_request.labels.*.name, 'ci: full-run') }}"
        run: python3 hack/ci_scope.py e2e --base "$BASE_SHA" --head "$HEAD_SHA"
```

6c. In the `combine` step env, replace

```yaml
          CI: ${{ steps.filter.outputs.ci == 'true' || steps.scope.outputs.all == 'true' }}
```

with

```yaml
          CI: ${{ steps.filter.outputs.ci == 'true' || steps.scope.outputs.all == 'true' || steps.e2e.outputs.all == 'true' }}
```

6d. In the `summarize test selection` step env, after
`          BOT_MATRIX: ${{ steps.bots.outputs.matrix }}` add:

```yaml
          E2E_OUTPUTS: ${{ toJSON(steps.e2e.outputs) }}
```

6e. `e2e-buckets` job: after the step
`      - name: verify buckets are disjoint + exhaustive` / `        run: ./test/e2e/buckets.sh verify`
insert:

```yaml
      - name: verify every e2e test declares what it exercises
        run: python3 hack/ci_scope.py verify-suites
      - name: selection rules map every changed path
        # An unmapped path already forced the full suite; this makes the gap visible
        # so a rule gets added to hack/ci_scope.py (spec 023 quickstart scenario 12).
        env:
          UNMAPPED: ${{ needs.changes.outputs.e2e-unmapped }}
        run: |
          if [ -n "$UNMAPPED" ] && [ "$UNMAPPED" != "[]" ]; then
            echo "::error::no selection rule maps these paths: $UNMAPPED"
            exit 1
          fi
```

6f. `e2e-go` job: its `env:` block is

```yaml
    env:
      CLUSTER: gameplane-e2e
      CTX: kind-gameplane-e2e
    steps: &e2e-go-steps
```

Replace it with

```yaml
    env:
      CLUSTER: gameplane-e2e
      CTX: kind-gameplane-e2e
      # Only this bucket's selected tests (spec 023 OD-6); empty skips the leg.
      RUN_REGEX: ${{ fromJSON(needs.changes.outputs.e2e-run)[matrix.bucket] || '' }}
      TAIL_REGEX: ${{ matrix.tail == 'ratelimit' && fromJSON(needs.changes.outputs.e2e-run).ratelimit || '' }}
    steps: &e2e-go-steps
```

In the `e2e-go-arm64` job, replace

```yaml
    env:
      CLUSTER: gameplane-e2e
      CTX: kind-gameplane-e2e
    steps: *e2e-go-steps
```

with

```yaml
    env:
      CLUSTER: gameplane-e2e
      CTX: kind-gameplane-e2e
      # Same selection as e2e-go, so both arches always run the same tests (FR-010).
      RUN_REGEX: ${{ fromJSON(needs.changes.outputs.e2e-run)[matrix.bucket] || '' }}
      TAIL_REGEX: ${{ matrix.tail == 'ratelimit' && fromJSON(needs.changes.outputs.e2e-run).ratelimit || '' }}
    steps: *e2e-go-steps
```

Inside `&e2e-go-steps`, add `if: env.RUN_REGEX != '' || env.TAIL_REGEX != ''` as the
first key of each of these six steps: the `actions/checkout` step, the
`./.github/actions/go-cache` step, the `azure/setup-helm` step, `install kind`,
`load images`, and `bring up cluster + helm install`. For a `- uses:` step put the `if:`
line right after the `- uses:` line at the same indent as `with:`. Example:

```yaml
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        if: env.RUN_REGEX != '' || env.TAIL_REGEX != ''
      - uses: ./.github/actions/go-cache
        if: env.RUN_REGEX != '' || env.TAIL_REGEX != ''
        with:
          key-suffix: e2e
```

Replace the two run steps:

```yaml
      - name: run go e2e (${{ matrix.bucket }})
        run: |
          RUN_REGEX="$(./test/e2e/buckets.sh regex '${{ matrix.bucket }}')"
          cd test/e2e && \
```

with

```yaml
      - name: run go e2e (${{ matrix.bucket }})
        if: env.RUN_REGEX != ''
        run: |
          cd test/e2e && \
```

and

```yaml
        if: matrix.tail == 'ratelimit'
        run: |
          RUN_REGEX="$(./test/e2e/buckets.sh regex ratelimit)"
          cd test/e2e && \
            GAMEPLANE_E2E_REUSE_CLUSTER=1 \
            GAMEPLANE_E2E_CLUSTER="$CLUSTER" \
            go test -tags=e2e -timeout 5m -v -run "$RUN_REGEX" ./...
```

with

```yaml
        if: env.TAIL_REGEX != ''
        run: |
          cd test/e2e && \
            GAMEPLANE_E2E_REUSE_CLUSTER=1 \
            GAMEPLANE_E2E_CLUSTER="$CLUSTER" \
            go test -tags=e2e -timeout 5m -v -run "$TAIL_REGEX" ./...
```

6g. Job gates. Replace the `if:` line of each job:

| Job | New `if:` |
|---|---|
| `e2e-multicluster`, `e2e-multicluster-arm64` | `needs.changes.outputs.e2e == 'true' && needs.changes.outputs.e2e-multicluster == 'true'` |
| `e2e-upgrade`, `e2e-upgrade-arm64` | `needs.changes.outputs.e2e == 'true' && needs.changes.outputs.e2e-upgrade == 'true'` |
| `e2e-web-live`, `e2e-web-live-arm64` | `needs.changes.outputs.weblive == 'true' && needs.changes.outputs.e2e-web-live == 'true'` |
| `e2e-game-bot` | `needs.changes.outputs.e2e == 'true' && needs.changes.outputs.e2e-bots-any == 'true'` |

In `e2e-game-bot` also replace
`      matrix: ${{ fromJSON(needs.changes.outputs.bot-matrix) }}` with
`      matrix: ${{ fromJSON(needs.changes.outputs.e2e-bot-matrix) }}`.

In `e2e-capture-overhead` replace the line
`      (github.event_name == 'pull_request' && needs.changes.outputs.capture_overhead == 'true') ||`
with
`      (github.event_name == 'pull_request' && needs.changes.outputs.capture_overhead == 'true' && needs.changes.outputs.e2e-capture-overhead == 'true') ||`.

## 7. Specs

- `contracts/ci-scope-cli.md`: add a `## Mode e2e — new` section listing the outputs of
  `e2e_outputs()` (`all`, `reason`, `unmapped`, `e2e-run`, `e2e-why`, `e2e-multicluster`,
  `e2e-upgrade`, `e2e-capture-overhead`, `e2e-web-live`, `bot-matrix`, `bots-any`); in
  the "Mode modules — added keys" table mark `e2e-exclude`, `e2e-run`, the four booleans
  and `unmapped` as "emitted by mode `e2e`"; mark `e2e-exclude` as not emitted (decision 2
  above); in "Mode bots — changed" say the selected matrix comes from mode `e2e`.
- `contracts/suite-components.md` rule 3: append "A changed `test/e2e/<file>.go` also
  selects the tests in files that use its top-level names (helpers)." Rule list:
  `components` may be empty when `paths` is set (the `TestCISelection_` entry).
- `tasks.md`: tick T014–T029 `[X]` after the PR opens; reword T020/T022–T025 to say mode
  `e2e`, and T025 to say "skip unselected legs with `RUN_REGEX`" instead of `e2e-exclude`.

## 8. Follow-up fix: an unmapped path must also widen mode `modules`

Mode `e2e` sets `all` for an unmapped path, and `combine` folds it into `CI`, which turns
the `go` and `lint` gates on. But mode `modules` still emits `go-exclude`/`lint-exclude`
from its own (narrow) selection, so for a change like `tools/x.sh` every module is
excluded and the `go` matrix has no legs. Mode `modules` must treat an unmapped path as
an unknown diff (every module), like any other doubt (FR-002).

8a. `hack/ci_scope.py`: after the `EXTRA_COMPONENTS = (...)` line insert

```python
# Top-level areas a selection rule knows; any other path is unmapped and forces full scope.
KNOWN_DIRS = ("charts", "containers", "deploy/kind", "signing", "test/e2e", "web", "modules")


def is_unmapped(path, modules):
    """Return True when no selection rule knows path; such a path forces full scope."""
    if (path.endswith(".md") or path in NO_E2E_FILES or path in FORCE_ALL_FILES
            or path == ".dockerignore"):
        return False
    return not any(path == d or path.startswith(d + "/")
                   for d in NO_E2E_DIRS + KNOWN_DIRS + tuple(modules))
```

8b. In `main()`, mode `modules` branch, replace

```python
        paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
        selected = affected_modules(ROOT, paths)
```

with

```python
        paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
        if paths is not None and any(is_unmapped(p, workspace_modules(ROOT)) for p in paths):
            paths = None  # mode e2e reports the path; every module runs (FR-002)
        selected = affected_modules(ROOT, paths)
```

(Only the occurrence directly followed by `selected = affected_modules(ROOT, paths)`.)

8c. `hack/test_ci_scope.py`: add `is_unmapped` to the `from ci_scope import (...)` list
(alphabetical, after `image_inputs`), and insert this class directly before the final
`if __name__ == "__main__":` line, with two blank lines around it:

```python
class UnmappedPathTests(unittest.TestCase):
    """An unmapped path widens mode modules to every module (spec 023 FR-002)."""

    def setUp(self):
        """Create the shared Go workspace fixture for each test."""
        make_go_workspace(self)

    def test_known_and_unknown_paths(self):
        modules = ["lib", "api", "test/e2e", "other"]
        for path in ("README.md", "api/x.go", "charts/gameplane/values.yaml", "docs/a.txt",
                     "hack/ci_scope.py", ".dockerignore", "web/src/App.tsx", "modules"):
            with self.subTest(path=path):
                self.assertFalse(is_unmapped(path, modules))
        self.assertTrue(is_unmapped("tools/x.sh", modules))

    def test_modules_mode_selects_every_module_for_an_unmapped_path(self):
        output = self.root / "output"
        output.write_text("", encoding="utf-8")
        with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "modules"]), \
                patch.dict("os.environ", {"GITHUB_EVENT_NAME": "pull_request",
                                          "GITHUB_REF": "refs/pull/7/merge", "CI_FULL_RUN": "false",
                                          "GITHUB_OUTPUT": str(output)}), \
                patch("ci_scope.changed_paths", return_value=["tools/x.sh"]):
            main()
        values = {key: json.loads(value) for key, value in
                  (line.split("=", 1) for line in output.read_text(encoding="utf-8").splitlines())}
        self.assertEqual(values, FULL_VALUES)
```
