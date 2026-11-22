# Brief: PR 3, unit test selection and changed-lines coverage (T031–T040)

Branch `claude/project-thread-63yei6-pr3`, stacked on PR 2. Facts below were checked
once by the scout; fix agents apply them blind. Every edit is exact: find the quoted
"old" text, replace it with "new". Do not touch any other line, and never edit an
existing test.

Scout facts:

- Tests that read files outside their package: `operator/internal/controller`
  (`config/crd`, `test/crds/snapshot`), `api/internal/handlers` (`operator/config/crd`),
  `gp-module/internal/validator` (`modules/cs2`), `i18n/cmd/i18n-check`
  (`i18n/testdata/defective`), `api/internal/messages` (walks all of `api/internal`).
- `tunnel/frpc/go.mod` is a nested module outside `go.work`; the package index skips any
  sub-directory that holds its own `go.mod`.
- Every `.testcoverage.yml` has `threshold.total`; some have `exclude.paths` regexes
  that match a module-relative file path or a package directory (`^api/v1alpha1$`).
- `web/vitest.config.ts` has `coverage.exclude` (with `**/*.test.{ts,tsx}`) and
  `coverage.thresholds.lines: 92`. Its reporters do not include `json`, so the partial
  run passes `--coverage.reporter=json`. That also means no `coverage-summary.json`, so
  the commit-status step exits 0 on partial runs.
- `make test-ci-scope` runs in the `workflow-lint` job, gated by the `github` filter.

## 1. `hack/ci_scope.py`

### 1a. Constants. Insert after the `KNOWN_DIRS = (...)` line (line 48)

```python
# A change to one of these files in a module root reruns that whole module (research R4).
GO_MODULE_WIDE = {"go.mod", "go.sum", ".testcoverage.yml"}
# Tests that read files outside their package directory. A changed path under a key also
# selects these package directories (research R4, the package form of file_consumers).
FILE_READERS = {
    "operator/config/crd": ("operator/internal/controller", "api/internal/handlers"),
    "operator/test/crds": ("operator/internal/controller",),
    "modules": ("gp-module/internal/validator",),
    "i18n/testdata": ("i18n/cmd/i18n-check",),
    "api/internal": ("api/internal/messages",),  # usage_test walks every api/internal file
}
# Directories that never hold a package the go job tests.
SKIP_PACKAGE_DIRS = {"testdata", "vendor", "node_modules"}
# Web files that change every Vitest run (research R6).
WEB_ALL_FILES = {"web/package.json", "web/package-lock.json", "web/vitest.config.ts",
                 "web/vite.config.ts", "web/eslint.config.js"}
```

### 1b. `FORCE_ALL_FILES`

old:
```python
FORCE_ALL_FILES = {
    ".github/workflows/ci.yaml", "hack/ci_scope.py", "hack/test_ci_scope.py", "Makefile",
    "go.work", "go.work.sum", "test/e2e/buckets.sh", ".golangci.yml",
}
```
new:
```python
FORCE_ALL_FILES = {
    ".github/workflows/ci.yaml", "hack/ci_scope.py", "hack/test_ci_scope.py", "Makefile",
    "go.work", "go.work.sum", "test/e2e/buckets.sh", ".golangci.yml",
    "hack/check_changed_coverage.py", "hack/test_check_changed_coverage.py",
}
```

### 1c. `affected_modules` shared prefixes

old:
```python
        p in shared or p.startswith((".github/", "hack/ci_scope", "hack/test_ci_scope"))
        or Path(p).name in {"go.mod", "go.sum"}
```
new:
```python
        p in shared or p.startswith((".github/", "hack/ci_scope", "hack/test_ci_scope",
                                     "hack/check_changed_coverage",
                                     "hack/test_check_changed_coverage"))
        or Path(p).name in {"go.mod", "go.sum"}
```

### 1d. New functions. Insert right before `def verify_suites(suites, buckets, modules):`

```python
def go_imports(text):
    """Return the import paths named in a Go file's import declarations."""
    found = re.findall(r'^import\s+(?:[\w.]+\s+)?"([^"]+)"', text, re.MULTILINE)
    for block in re.findall(r"^import\s*\((.*?)\)", text, re.MULTILINE | re.DOTALL):
        found += re.findall(r'"([^"]+)"', block)
    return found


def package_index(root, modules):
    """Return {package dir: imported paths} and {import path: package dir} for the workspace."""
    imports, dirs = {}, {}
    for module, name in modules.items():
        base = root / module
        for current, subdirs, files in os.walk(base):
            here = Path(current)
            subdirs[:] = sorted(d for d in subdirs if not d.startswith((".", "_"))
                                and d not in SKIP_PACKAGE_DIRS
                                and not (here / d / "go.mod").exists())
            sources = [f for f in files if f.endswith(".go")]
            if not sources:
                continue
            directory = here.relative_to(root).as_posix()
            inner = here.relative_to(base).as_posix()
            dirs[name if inner == "." else f"{name}/{inner}"] = directory
            imports[directory] = {path for f in sorted(sources)
                                  for path in go_imports((here / f).read_text(encoding="utf-8"))}
    return imports, dirs


def owning_module(path, modules):
    """Return the workspace module directory that contains path, or None."""
    owners = [m for m in modules if under(path, m)]
    return max(owners, key=len) if owners else None


def go_packages(root, paths, reason):
    """Return {module: [go test package patterns]} for the go job (research R4, OD-8).

    A changed file selects its package and, unless it is a _test.go file, every package
    that imports it, across modules. A module's own go.mod, go.sum or .testcoverage.yml,
    or a file in no package, selects ./... for that module.
    """
    modules = workspace_modules(root)
    targets = [m for m in sorted(modules) if m != "test/e2e"]
    if reason or paths is None:
        return {m: ["./..."] for m in targets}
    imports, dirs = package_index(root, modules)
    wide, seeds, direct = set(), set(), set()
    for path in paths:
        if path.endswith(".md"):
            continue
        readers = [d for key, found in FILE_READERS.items() if under(path, key) for d in found]
        direct.update(readers)  # a reader's tests read the files; its importers do not
        module = owning_module(path, modules)
        if module is None:
            continue
        if path in {f"{module}/{name}" for name in GO_MODULE_WIDE}:
            wide.add(module)
            continue
        directory = path.rpartition("/")[0]
        while directory not in imports and directory != module:
            directory = directory.rpartition("/")[0]
        if directory in imports:
            (direct if path.endswith("_test.go") else seeds).add(directory)
        elif not readers:
            wide.add(module)
    importers = {}
    for directory, imported in imports.items():
        for name in imported:
            if name in dirs:
                importers.setdefault(dirs[name], set()).add(directory)
    selected, queue = set(), [d for d in seeds if d in imports]
    while queue:
        directory = queue.pop()
        if directory not in selected:
            selected.add(directory)
            queue.extend(importers.get(directory, ()))
    selected |= {d for d in direct if d in imports}
    result = {}
    for module in targets:
        if module in wide:
            result[module] = ["./..."]
        else:
            result[module] = sorted("." if d == module else "./" + d[len(module) + 1:]
                                    for d in selected if owning_module(d, modules) == module)
    return result


def web_tests(root, paths, reason):
    """Return "all" or the changed web/src files for `vitest related` (research R6, OD-9)."""
    if reason or paths is None:
        return "all"
    files = []
    for path in paths:
        if (path in WEB_ALL_FILES or under(path, "web/src/test") or under(path, "i18n/locales")
                or fnmatch.fnmatch(path, "web/tsconfig*.json")):
            return "all"
        if not under(path, "web") or path.endswith(".md") or under(path, "web/e2e"):
            continue
        if not under(path, "web/src") or not (root / path).exists():
            return "all"  # vitest related cannot follow a deleted file or a non-source input
        files.append(path)
    return sorted(files)


def unit_outputs(root, paths, reason):
    """Return the outputs of mode units: Go package patterns per module and web tests."""
    return {
        "all": bool(reason),
        "reason": reason or "",
        "go-packages": go_packages(root, paths, reason),
        "web-tests": web_tests(root, paths, reason),
    }
```

### 1e. `summarize`: notes on the go and web rows (T039)

old:
```python
    e2e = load_json(env, "E2E_OUTPUTS")
```
new:
```python
    e2e = load_json(env, "E2E_OUTPUTS")
    units = load_json(env, "UNITS_OUTPUTS")
    go_selected = decoded(units, "go-packages", {})
    web_selected = decoded(units, "web-tests", "all")
```

old:
```python
    def add(suite, run, feed=(), run_reason="", skip_reason="not affected"):
        """Append one selection row with a run or skip decision and its reason."""
```
new:
```python
    def go_note(module):
        """Return the package count note for one go row, or "" for a whole-module run."""
        packages = go_selected.get(module, ["./..."]) if units else ["./..."]
        if packages == ["./..."]:
            return ""
        return f"{len(packages)} packages; " if packages else "vet only, no affected package; "

    if not units or web_selected == "all":
        web_note = ""
    elif web_selected:
        web_note = f"{len(web_selected)} files for vitest related; "
    else:
        web_note = "lint and build only; "

    def add(suite, run, feed=(), run_reason="", skip_reason="not affected", note=""):
        """Append one selection row with a run or skip decision and its reason."""
```

old:
```python
        else:
            why = skip_reason
        rows.append({"suite": suite, "decision": "run" if run else "skip", "reason": why})
```
new:
```python
        else:
            why = skip_reason
        if run and not reason:
            why = note + why
        rows.append({"suite": suite, "decision": "run" if run else "skip", "reason": why})
```

old:
```python
            add(f"go ({m}) (amd64, arm64)", gate("go") and m in selected, module_feed(m))
```
new:
```python
            add(f"go ({m}) (amd64, arm64)", gate("go") and m in selected, module_feed(m),
                note=go_note(m))
```

old:
```python
    add("web", gate("web"), filtered("web"))
```
new:
```python
    add("web", gate("web"), filtered("web"), note=web_note)
```

### 1f. `main`: mode `units`

old:
```python
    parser.add_argument("mode", choices=("modules", "images", "dockerfiles", "bots", "summary",
                                         "e2e", "verify-suites"))
```
new:
```python
    parser.add_argument("mode", choices=("modules", "images", "dockerfiles", "bots", "summary",
                                         "e2e", "verify-suites", "units"))
```

Insert right before the line `    # PRs use affected work; every master push, manual run and ci: full-run`:

```python
    if args.mode == "units":
        env = os.environ
        try:
            event_reason = full_scope_reason(env, [])
            paths = None if event_reason else changed_paths(ROOT, args.base, args.head)
            reason = full_scope_reason(env, paths)
            modules = workspace_modules(ROOT)
            unmapped = [p for p in paths or [] if is_unmapped(p, modules)]
            if reason is None and unmapped:
                reason = f"unmapped path {unmapped[0]}"
            values = unit_outputs(ROOT, paths, reason)
        except Exception as exc:  # any doubt runs every unit test (FR-002)
            print(f"::warning::unit selection failed, running every unit test: {exc}")
            values = unit_outputs(ROOT, None, f"error: {exc}")
        emit(values)
        return
```

Also in the summary-mode block, add `UNITS_OUTPUTS` to nothing: no change there.

## 2. New file `hack/check_changed_coverage.py`

```python
"""Changed-lines coverage gate for partial CI runs (spec 023 FR-009, OD-1, OD-5).

Reads added lines from `git diff -U0`, intersects them with a Go cover profile or a
Vitest (Istanbul) coverage-final.json, and fails when the covered share of added
statement lines is below the module's own threshold. Python stdlib only.
"""

import argparse
import fnmatch
import json
import os
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
PROFILE_LINE = re.compile(r"^(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$")
HUNK = re.compile(r"^@@ -\S+ \+(\d+)(?:,(\d+))? @@")


def added_lines(root, base, head, prefix):
    """Return {repo path: set of added line numbers} for files under prefix."""
    diff = subprocess.run(
        ["git", "diff", "-U0", "--no-color", "--no-renames", base, head, "--", prefix],
        cwd=root, check=True, capture_output=True, text=True).stdout
    lines, current = {}, None
    for line in diff.splitlines():
        if line.startswith("+++ "):
            target = line[4:]
            current = target[2:] if target.startswith("b/") else None
        elif current and line.startswith("@@"):
            match = HUNK.match(line)
            if match:
                start, count = int(match[1]), int(match[2] or "1")
                lines.setdefault(current, set()).update(range(start, start + count))
    return lines


def expand(glob):
    """Expand {a,b} alternatives in a glob into plain fnmatch patterns."""
    match = re.search(r"\{([^{}]*)\}", glob)
    if not match:
        return [glob]
    return [pattern for option in match[1].split(",")
            for pattern in expand(glob[:match.start()] + option + glob[match.end():])]


def go_config(module_dir):
    """Return (threshold.total, exclude.paths regexes) from a module's .testcoverage.yml."""
    text = (module_dir / ".testcoverage.yml").read_text(encoding="utf-8")
    total, excludes, section = None, [], None
    for raw in text.splitlines():
        line = raw.split("#", 1)[0].rstrip()
        if not line.strip():
            continue
        if not line.startswith((" ", "-")):
            section = line.split(":", 1)[0].strip()
            continue
        item = line.strip()
        if section == "threshold" and item.startswith("total:"):
            total = float(item.split(":", 1)[1])
        elif section == "exclude" and item.startswith("- "):
            excludes.append(item[2:].strip().strip("'\""))
    if total is None:
        raise ValueError(f"{module_dir.name}/.testcoverage.yml has no threshold.total")
    return total, excludes


def vitest_config(web_dir):
    """Return (coverage.thresholds.lines, expanded coverage.exclude globs) from vitest.config.ts."""
    text = (web_dir / "vitest.config.ts").read_text(encoding="utf-8")
    start = text.find("coverage:")
    coverage = text[start:] if start >= 0 else ""
    lines = re.search(r"thresholds:\s*\{[^}]*?\blines:\s*(\d+(?:\.\d+)?)", coverage)
    if not lines:
        raise ValueError("web/vitest.config.ts has no coverage.thresholds.lines")
    block = re.search(r"\bexclude:\s*\[(.*?)\]", coverage, re.DOTALL)
    globs = re.findall(r'"([^"]+)"', block[1]) if block else []
    return float(lines[1]), [pattern for glob in globs for pattern in expand(glob)]


def mark(lines, first, last, covered):
    """Mark lines first..last; a line is covered when any block covering it ran."""
    for number in range(first, last + 1):
        lines[number] = lines.get(number, False) or covered


def go_statements(profile, module_path, module):
    """Return {repo path: {line: covered}} from a Go cover profile."""
    result = {}
    for line in Path(profile).read_text(encoding="utf-8").splitlines():
        match = PROFILE_LINE.match(line)
        if not match or not match[1].startswith(module_path + "/"):
            continue
        path = module + "/" + match[1][len(module_path) + 1:]
        mark(result.setdefault(path, {}), int(match[2]), int(match[3]), int(match[5]) > 0)
    return result


def vitest_statements(report, root):
    """Return {repo path: {line: covered}} from an Istanbul coverage-final.json."""
    data = json.loads(Path(report).read_text(encoding="utf-8"))
    result = {}
    for name, entry in data.items():
        path = Path(entry.get("path", name))
        if not path.is_absolute():
            path = root / path
        try:
            relative = path.resolve().relative_to(root.resolve()).as_posix()
        except ValueError:
            continue
        lines = result.setdefault(relative, {})
        counts = entry.get("s", {})
        for key, location in entry.get("statementMap", {}).items():
            mark(lines, location["start"]["line"], location["end"]["line"],
                 counts.get(key, 0) > 0)
    return result


def check(added, statements, threshold, excluded):
    """Return (exit code, one-line result, uncovered file:line list)."""
    total, missing = 0, []
    for path in sorted(added):
        if excluded(path):
            continue
        known = statements.get(path, {})
        for number in sorted(added[path]):
            if number in known:
                total += 1
                if not known[number]:
                    missing.append(f"{path}:{number}")
    if total == 0:
        return 0, "changed-lines coverage: no added statement lines, passes", []
    covered = total - len(missing)
    share = 100 * covered / total
    line = (f"changed-lines coverage: {covered}/{total} added statement lines covered "
            f"({share:.1f}%, minimum {threshold:g}%)")
    return (0 if share >= threshold else 1), line, missing


def go_inputs(root, module, profiles, base, head):
    """Return (threshold, added lines, statements, excluded) for one Go module."""
    threshold, patterns = go_config(root / module)
    text = (root / module / "go.mod").read_text(encoding="utf-8")
    match = re.search(r"^module\s+(\S+)", text, re.MULTILINE)
    if not match:
        raise ValueError(f"{module}/go.mod has no module directive")
    statements = {}
    for profile in profiles:
        for path, lines in go_statements(profile, match[1], module).items():
            merged = statements.setdefault(path, {})
            for number, covered in lines.items():
                mark(merged, number, number, covered)

    def excluded(path):
        """Skip tests, non-Go files and the module's own coverage exclusions."""
        relative = path[len(module) + 1:]
        directory = relative.rpartition("/")[0]
        return (not relative.endswith(".go") or relative.endswith("_test.go")
                or any(re.search(p, relative) or re.search(p, directory) for p in patterns))

    return threshold, added_lines(root, base, head, module), statements, excluded


def web_inputs(root, report, base, head):
    """Return (threshold, added lines, statements, excluded) for the dashboard."""
    threshold, globs = vitest_config(root / "web")

    def excluded(path):
        """Skip tests, non-TypeScript files and vitest.config.ts coverage.exclude."""
        relative = path[len("web/"):]
        return (not relative.endswith((".ts", ".tsx"))
                or relative.endswith((".test.ts", ".test.tsx"))
                or any(fnmatch.fnmatch(relative, glob) for glob in globs))

    return (threshold, added_lines(root, base, head, "web/src"),
            vitest_statements(report, root), excluded)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--go-profile", action="append", default=[])
    parser.add_argument("--module")
    parser.add_argument("--vitest-json")
    parser.add_argument("--root", default=str(ROOT))
    args = parser.parse_args(argv)
    root = Path(args.root)
    if not args.vitest_json and not (args.go_profile and args.module):
        parser.error("pass --vitest-json, or --go-profile with --module")
    try:
        if args.vitest_json:
            inputs = web_inputs(root, args.vitest_json, args.base, args.head)
        else:
            inputs = go_inputs(root, args.module.strip("/"), args.go_profile, args.base,
                               args.head)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as exc:
        print(f"::error::changed-lines coverage: {exc}")
        return 1
    threshold, added, statements, excluded = inputs
    code, line, missing = check(added, statements, threshold, excluded)
    print(line)
    for item in missing:
        print(f"uncovered: {item}")
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as output:
            output.write(line + "\n")
    return code


if __name__ == "__main__":
    sys.exit(main())
```

## 3. New file `hack/test_check_changed_coverage.py` (T033)

```python
"""Behavioral coverage for the changed-lines coverage gate (run in CI)."""

import contextlib
import io
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from check_changed_coverage import check, expand, go_config, main, vitest_config


GIT = ["git", "-c", "user.name=test", "-c", "user.email=test@example.com",
       "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"]
COVERAGE_YML = """threshold:
  file: 0
  total: 80

exclude:
  paths:
    - ^cmd/  # wiring
"""
VITEST_CONFIG = """export default defineConfig({
  test: {
    exclude: ["e2e/**"],
    coverage: {
      exclude: ["src/main.tsx", "**/*.test.{ts,tsx}"],
      thresholds: {
        lines: 92,
        functions: 76,
      },
    },
  },
});
"""
GO_FUNC = "package mod\n// added comment\nfunc F() int {\n\treturn 1\n}\n"


class ChangedCoverageTests(unittest.TestCase):
    """Check added-line coverage against Go profiles and Istanbul JSON."""

    def setUp(self):
        """Create a git repository with one Go module and a web tree."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")
        self.write("mod/go.mod", "module example.com/mod\n")
        self.write("mod/.testcoverage.yml", COVERAGE_YML)
        self.write("mod/a.go", "package mod\n")
        self.write("mod/cmd/main.go", "package main\n")
        self.write("web/vitest.config.ts", VITEST_CONFIG)
        self.write("web/src/a.ts", "export const a = 1;\n")
        self.base = self.commit()
        self.head = self.base

    def git(self, *args):
        """Run git in the fixture repository and return its trimmed output."""
        return subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True,
                              text=True).stdout.strip()

    def write(self, name, content):
        """Write a fixture file and return its path."""
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def commit(self):
        """Commit every fixture change and return the new commit id."""
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "change")
        return self.git("rev-parse", "HEAD")

    def run_main(self, *args):
        """Run the gate and return (exit code, stdout, step summary)."""
        summary = self.root / "summary.md"
        out = io.StringIO()
        with patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": str(summary)}), \
                contextlib.redirect_stdout(out):
            code = main(["--root", str(self.root), "--base", self.base, "--head", self.head,
                         *args])
        text = summary.read_text(encoding="utf-8") if summary.exists() else ""
        return code, out.getvalue(), text

    def go_change(self, count):
        """Add a comment and a three-line function block; return a profile path."""
        self.write("mod/a.go", GO_FUNC)
        self.head = self.commit()
        profile = self.write("profile.out",
                             f"mode: atomic\nexample.com/mod/a.go:3.14,5.2 1 {count}\n")
        return str(profile)

    def test_covered_go_lines_pass(self):
        code, out, summary = self.run_main("--go-profile", self.go_change(1), "--module", "mod")
        self.assertEqual(code, 0)
        self.assertIn("3/3 added statement lines covered", out)
        self.assertIn("changed-lines coverage", summary)

    def test_uncovered_go_lines_fail_and_are_listed(self):
        code, out, _ = self.run_main("--go-profile", self.go_change(0), "--module", "mod")
        self.assertEqual(code, 1)
        self.assertIn("0/3 added statement lines covered", out)
        for number in (3, 4, 5):
            self.assertIn(f"uncovered: mod/a.go:{number}", out)

    def test_zero_statement_lines_pass(self):
        self.write("mod/a.go", "package mod\n// only a comment\n")
        self.head = self.commit()
        profile = self.write("profile.out", "mode: atomic\n")
        code, out, _ = self.run_main("--go-profile", str(profile), "--module", "mod")
        self.assertEqual(code, 0)
        self.assertIn("no added statement lines", out)

    def test_go_tests_and_excluded_paths_are_ignored(self):
        self.write("mod/cmd/main.go", "package main\nfunc main() {\n}\n")
        self.write("mod/a_test.go", "package mod\nfunc helper() {\n}\n")
        self.head = self.commit()
        profile = self.write("profile.out", "mode: atomic\n"
                             "example.com/mod/cmd/main.go:2.13,3.2 0 0\n"
                             "example.com/mod/a_test.go:2.15,3.2 0 0\n")
        code, out, _ = self.run_main("--go-profile", str(profile), "--module", "mod")
        self.assertEqual(code, 0)
        self.assertIn("no added statement lines", out)

    def test_istanbul_json_for_web(self):
        self.write("web/src/a.ts", "export const a = 1;\nexport const b = 2;\n")
        self.head = self.commit()
        source = str(self.root / "web/src/a.ts")
        for hits, want in ((0, 1), (1, 0)):
            with self.subTest(hits=hits):
                report = self.write("web/coverage/coverage-final.json", json.dumps({source: {
                    "path": source,
                    "statementMap": {"0": {"start": {"line": 2, "column": 0},
                                           "end": {"line": 2, "column": 20}}},
                    "s": {"0": hits},
                }}))
                code, out, _ = self.run_main("--vitest-json", str(report))
                self.assertEqual(code, want, out)

    def test_threshold_comes_from_testcoverage_yml(self):
        self.assertEqual(go_config(self.root / "mod"), (80.0, ["^cmd/"]))

    def test_threshold_comes_from_vitest_config(self):
        self.assertEqual(vitest_config(self.root / "web"),
                         (92.0, ["src/main.tsx", "**/*.test.ts", "**/*.test.tsx"]))

    def test_missing_threshold_fails(self):
        self.write("mod/.testcoverage.yml", "threshold:\n  file: 0\n")
        with self.assertRaises(ValueError):
            go_config(self.root / "mod")
        code, out, _ = self.run_main("--go-profile", self.go_change(1), "--module", "mod")
        self.assertEqual(code, 1)
        self.assertIn("::error::", out)
        self.write("web/vitest.config.ts", "export default defineConfig({ test: {} });\n")
        with self.assertRaises(ValueError):
            vitest_config(self.root / "web")

    def test_share_at_threshold_passes(self):
        added = {"f.go": {1, 2}}
        statements = {"f.go": {1: True, 2: False}}
        self.assertEqual(check(added, statements, 50, lambda _: False)[0], 0)
        self.assertEqual(check(added, statements, 51, lambda _: False)[0], 1)

    def test_brace_expansion(self):
        self.assertEqual(expand("**/*.test.{ts,tsx}"), ["**/*.test.ts", "**/*.test.tsx"])
        self.assertEqual(expand("src/main.tsx"), ["src/main.tsx"])


if __name__ == "__main__":
    unittest.main()
```

## 4. `hack/test_ci_scope.py`: new test classes (T031, T032). Existing tests untouched.

Import line: add `go_packages, web_tests` to the `from ci_scope import (...)` list (keep
the rest exactly). old:
```python
                      is_unmapped, load_suites, main, publish_paths, select_images, summarize,
                      verify_suites,
                      workspace_modules)
```
new:
```python
                      is_unmapped, load_suites, main, publish_paths, select_images, summarize,
                      verify_suites, go_packages, web_tests,
                      workspace_modules)
```

Insert right before `if __name__ == "__main__":` at the end of the file:

```python
class GoPackageTests(unittest.TestCase):
    """Package-level Go selection from import scanning (spec 023 T031, research R4)."""

    def setUp(self):
        """Create the shared workspace plus three api packages and one lib package."""
        make_go_workspace(self)
        write_file(self.root, "lib/helpers/h.go", "package helpers\n")
        write_file(self.root, "api/internal/a/a.go",
                   'package a\n\nimport "example.com/lib/helpers"\n\nvar _ = helpers.X\n')
        write_file(self.root, "api/internal/a/a_test.go", "package a\n")
        write_file(self.root, "api/internal/b/b.go",
                   'package b\n\nimport (\n\t"fmt"\n\tx "example.com/api/internal/a"\n)\n')
        write_file(self.root, "api/internal/c/c.go", "package c\n")
        write_file(self.root, "api/internal/c/testdata/in.txt", "fixture\n")

    def packages(self, *paths):
        """Return the go-packages selection for the changed paths."""
        return go_packages(self.root, list(paths), None)

    def test_leaf_change_selects_only_its_package(self):
        self.assertEqual(self.packages("api/internal/b/b.go"),
                         {"api": ["./internal/b"], "lib": [], "other": []})

    def test_importer_chain_crosses_modules(self):
        self.assertEqual(self.packages("lib/helpers/h.go"),
                         {"api": ["./internal/a", "./internal/b"], "lib": ["./helpers"],
                          "other": []})

    def test_test_file_change_selects_only_its_package(self):
        self.assertEqual(self.packages("api/internal/a/a_test.go")["api"], ["./internal/a"])

    def test_testdata_and_embedded_files_select_their_package(self):
        self.assertEqual(self.packages("api/internal/c/testdata/in.txt")["api"],
                         ["./internal/c"])
        self.assertEqual(self.packages("api/internal/c/schema.json")["api"], ["./internal/c"])

    def test_module_files_select_the_whole_module(self):
        for name in ("go.mod", "go.sum", ".testcoverage.yml"):
            with self.subTest(name=name):
                self.assertEqual(self.packages(f"api/{name}"),
                                 {"api": ["./..."], "lib": [], "other": []})

    def test_file_outside_any_package_selects_the_whole_module(self):
        self.assertEqual(self.packages("api/config/x.yaml")["api"], ["./..."])

    def test_markdown_selects_nothing(self):
        self.assertEqual(self.packages("api/README.md")["api"], [])

    def test_full_scope_runs_every_module(self):
        self.assertEqual(go_packages(self.root, None, "master push"),
                         {"api": ["./..."], "lib": ["./..."], "other": ["./..."]})


class WebTestsTests(unittest.TestCase):
    """Web unit selection for `vitest related` (spec 023 T032, research R6)."""

    def setUp(self):
        """Create a temporary web tree with one route."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        write_file(self.root, "web/src/routes/servers.tsx", "export {};\n")

    def test_route_file_gives_its_source_path(self):
        self.assertEqual(web_tests(self.root, ["web/src/routes/servers.tsx", "api/x.go"], None),
                         ["web/src/routes/servers.tsx"])

    def test_shared_web_inputs_give_all(self):
        for path in ("web/src/test/setup.ts", "web/package.json", "web/package-lock.json",
                     "web/vitest.config.ts", "web/vite.config.ts", "web/tsconfig.app.json",
                     "web/tsconfig.json", "web/eslint.config.js", "i18n/locales/en/app.json"):
            with self.subTest(path=path):
                self.assertEqual(web_tests(self.root, [path], None), "all")

    def test_deleted_source_gives_all(self):
        self.assertEqual(web_tests(self.root, ["web/src/routes/gone.tsx"], None), "all")

    def test_playwright_and_unrelated_paths_select_nothing(self):
        self.assertEqual(web_tests(self.root, ["web/e2e/servers.spec.ts", "web/README.md",
                                               "docs/a.md"], None), [])

    def test_full_scope_gives_all(self):
        self.assertEqual(web_tests(self.root, None, "master push"), "all")
        self.assertEqual(web_tests(self.root, ["web/src/routes/servers.tsx"], "manual run"),
                         "all")


class UnitSummaryNoteTests(unittest.TestCase):
    """The summary rows name package and file counts from mode units (spec 023 T039)."""

    def setUp(self):
        """Create the shared Go workspace fixture for each test."""
        make_go_workspace(self)

    def rows(self, units):
        """Return the summary rows of a partial run with go and web gates on."""
        env = {
            "GITHUB_EVENT_NAME": "pull_request",
            "SCOPE_OUTPUTS": json.dumps({"modules": json.dumps(["api", "lib"])}),
            "FILTER_OUTPUTS": json.dumps({}),
            "GATE_OUTPUTS": json.dumps({"go": "true", "web": "true"}),
            "BOT_MATRIX": json.dumps({"test": []}),
            "UNITS_OUTPUTS": json.dumps({key: json.dumps(value) for key, value in units.items()}),
        }
        with patch("ci_scope.ROOT", self.root):
            rows, _ = summarize(env, ["api/internal/a/a.go"], None)
        return {row["suite"]: row["reason"] for row in rows}

    def test_notes_name_package_and_file_counts(self):
        rows = self.rows({"go-packages": {"api": ["./internal/a", "./internal/b"], "lib": []},
                          "web-tests": ["web/src/a.tsx"]})
        self.assertTrue(rows["go (api) (amd64, arm64)"].startswith("2 packages; "))
        self.assertTrue(rows["go (lib) (amd64, arm64)"].startswith("vet only, no affected package; "))
        self.assertTrue(rows["web"].startswith("1 files for vitest related; "))

    def test_whole_runs_add_no_note(self):
        rows = self.rows({"go-packages": {"api": ["./..."], "lib": ["./..."]}, "web-tests": "all"})
        self.assertFalse(rows["go (api) (amd64, arm64)"].startswith(("2 packages", "vet only")))
        self.assertFalse(rows["web"].startswith(("1 files", "lint and build")))
        rows = self.rows({"go-packages": {}, "web-tests": []})
        self.assertTrue(rows["web"].startswith("lint and build only; "))
```

## 5. `Makefile` test-ci-scope target

old:
```make
	python3 -m unittest discover -s hack -p 'test_ci_scope.py' -v
```
new:
```make
	python3 -m unittest discover -s hack -p 'test_ci_scope.py' -v
	python3 -m unittest discover -s hack -p 'test_check_changed_coverage.py' -v
```
(Tab-indented, like the line above.)

## 6. `.github/workflows/ci.yaml`

6a. `changes` job outputs. old:
```yaml
      e2e-bots-any: ${{ steps.e2e.outputs.bots-any }}
```
new:
```yaml
      e2e-bots-any: ${{ steps.e2e.outputs.bots-any }}
      go-packages: ${{ steps.units.outputs.go-packages }}
      web-tests: ${{ steps.units.outputs.web-tests }}
```

6b. New step right after the `e2e` step (that is, insert before
`      - uses: dorny/paths-filter@`):
```yaml
      - name: select the unit test packages and web tests the change affects
        id: units
        # Package-level Go and `vitest related` selection (spec 023 US2). Any
        # doubt emits ./... for every module and "all" for web.
        env:
          BASE_SHA: ${{ github.event.pull_request.base.sha || github.event.before }}
          HEAD_SHA: ${{ github.sha }}
          CI_FULL_RUN: "${{ contains(github.event.pull_request.labels.*.name, 'ci: full-run') }}"
        run: python3 hack/ci_scope.py units --base "$BASE_SHA" --head "$HEAD_SHA"
```

6c. Both the `ci:` and the `github:` paths-filter lists contain the line
`              - 'hack/test_ci_scope.py'`. After EACH of the two, add:
```yaml
              - 'hack/check_changed_coverage.py'
              - 'hack/test_check_changed_coverage.py'
```

6d. Summary step env. old:
```yaml
          E2E_OUTPUTS: ${{ toJSON(steps.e2e.outputs) }}
```
new:
```yaml
          E2E_OUTPUTS: ${{ toJSON(steps.e2e.outputs) }}
          UNITS_OUTPUTS: ${{ toJSON(steps.units.outputs) }}
```

6e. `go` job. old:
```yaml
    timeout-minutes: 20
    permissions:
      contents: read
    strategy:
      fail-fast: false
      matrix:
        module: [netguard,
```
new (only the `env:` block is added; the `module:` line continues unchanged):
```yaml
    timeout-minutes: 20
    permissions:
      contents: read
    env:
      # Space-separated package patterns from the changes job (spec 023 US2).
      # Empty means no package is affected and only vet runs; a module the
      # selection does not list runs ./... .
      GO_PACKAGES: ${{ join(fromJSON(needs.changes.outputs.go-packages || '{}')[matrix.module] || fromJSON('["./..."]'), ' ') }}
      BASE_SHA: ${{ github.event.pull_request.base.sha || github.event.before }}
    strategy:
      fail-fast: false
      matrix:
        module: [netguard,
```

6f. Unit test step. old:
```yaml
      - name: unit test + coverage
        working-directory: ${{ matrix.module }}
        run: |
          mkdir -p coverage
          go test -covermode=atomic -coverpkg=./... \
            -coverprofile=coverage/unit.out ./...
```
new:
```yaml
      - name: unit test + coverage
        if: env.GO_PACKAGES != ''
        working-directory: ${{ matrix.module }}
        run: |
          mkdir -p coverage
          read -r -a packages <<<"$GO_PACKAGES"
          go test -covermode=atomic -coverpkg=./... \
            -coverprofile=coverage/unit.out "${packages[@]}"
```

6g. Envtest caches and step. In the three steps named `setup-envtest binary cache`,
`envtest assets cache` and `integration (envtest) test + coverage`, replace
```yaml
        if: matrix.module == 'operator' || matrix.module == 'api'
```
with
```yaml
        if: (matrix.module == 'operator' || matrix.module == 'api') && env.GO_PACKAGES != ''
```
And in the envtest run block, old:
```yaml
          export KUBEBUILDER_ASSETS
          go test -tags=envtest -timeout 10m \
            -covermode=atomic -coverpkg=./... \
            -coverprofile=coverage/envtest.out ./...
```
new:
```yaml
          export KUBEBUILDER_ASSETS
          read -r -a packages <<<"$GO_PACKAGES"
          go test -tags=envtest -timeout 10m \
            -covermode=atomic -coverpkg=./... \
            -coverprofile=coverage/envtest.out "${packages[@]}"
```

6h. Coverage steps. old:
```yaml
      - name: warm coverage tool cache
        # cover-go-merge and cover-go-check `go run` pinned tools, so they hit
```
new:
```yaml
      - name: warm coverage tool cache
        if: env.GO_PACKAGES != ''
        # cover-go-merge and cover-go-check `go run` pinned tools, so they hit
```
old:
```yaml
      - name: merge profiles + threshold gate
        run: make cover-go-merge && make cover-go-check
```
new:
```yaml
      - name: merge profiles + threshold gate
        # A whole-module run keeps the module total gate (spec 023 OD-1).
        if: env.GO_PACKAGES == './...'
        run: make cover-go-merge && make cover-go-check
      - name: merge profiles + changed-lines coverage gate
        # A package-level run gates the added lines at the module's own
        # threshold instead (spec 023 OD-1, OD-5).
        if: env.GO_PACKAGES != '' && env.GO_PACKAGES != './...'
        env:
          MODULE: ${{ matrix.module }}
        run: |
          git fetch --no-tags --depth=1 origin "$BASE_SHA"
          make cover-go-merge
          python3 hack/check_changed_coverage.py --base "$BASE_SHA" --head "$GITHUB_SHA" \
            --go-profile "$MODULE/coverage/merged.out" --module "$MODULE"
```
old:
```yaml
        if: matrix.module == 'api' && matrix.arch == 'amd64'
        run: |
          awk '$1 ~ /\/internal\/handlers\/secrets_managed\.go:/
```
new:
```yaml
        if: matrix.module == 'api' && matrix.arch == 'amd64' && env.GO_PACKAGES != ''
        run: |
          awk '$1 ~ /\/internal\/handlers\/secrets_managed\.go:/
```

6i. `web` job unit step. old:
```yaml
      - name: unit tests + coverage
        id: cover
        run: npm run test:cover
```
new:
```yaml
      - name: unit tests + coverage
        id: cover
        # Partial runs test only what the changed web/src files reach and gate
        # the added lines; "all" keeps the full suite and thresholds (spec 023 OD-9).
        env:
          WEB_TESTS: ${{ needs.changes.outputs.web-tests }}
          BASE_SHA: ${{ github.event.pull_request.base.sha || github.event.before }}
        run: |
          set -euo pipefail
          if [ -z "$WEB_TESTS" ] || [ "$WEB_TESTS" = '"all"' ]; then
            npm run test:cover
            exit 0
          fi
          mapfile -t files < <(jq -r '.[] | sub("^web/"; "")' <<<"$WEB_TESTS")
          if [ "${#files[@]}" -eq 0 ]; then
            echo "No dashboard source changed; Vitest skipped, lint and build ran."
            exit 0
          fi
          npx vitest related --run --passWithNoTests --coverage --coverage.reporter=json \
            --coverage.thresholds.lines=0 --coverage.thresholds.functions=0 \
            --coverage.thresholds.branches=0 --coverage.thresholds.statements=0 \
            "${files[@]}"
          if [ ! -f coverage/coverage-final.json ]; then
            echo "No coverage report from vitest related; running the full suite."
            npm run test:cover
            exit 0
          fi
          git fetch --no-tags --depth=1 origin "$BASE_SHA"
          cd .. && python3 hack/check_changed_coverage.py --base "$BASE_SHA" --head "$GITHUB_SHA" \
            --vitest-json web/coverage/coverage-final.json
```

## 7. Specs

7a. `specs/done_023-change-based-test-selection/contracts/ci-scope-cli.md`, old:
```markdown
| `go-packages` | JSON object: module → list of package patterns | `["./..."]` on full scope or when a module's own `go.mod`/`go.sum` changed |
| `web-tests` | JSON: `"all"` or list of `web/src/...` source paths for `vitest related` | |
```
new:
```markdown
| `go-packages` | JSON object: module → list of package patterns | Emitted by mode `units`. `["./..."]` on full scope or when a module's own `go.mod`/`go.sum`/`.testcoverage.yml` changed |
| `web-tests` | JSON: `"all"` or list of `web/src/...` source paths for `vitest related` | Emitted by mode `units` |
```

7b. Same file, insert before the line `## Mode \`bots\` — changed`:
```markdown
## Mode `units` (new)

Emits package-level Go selection and the web unit selection (research R4, R6). The
`units` step of the `changes` job runs right after `e2e`. Outputs:

| Key | Type | Meaning |
|---|---|---|
| `all` | boolean | True when `reason` is set |
| `reason` | string | A full-scope reason, `unmapped path <p>` or `error: <exception>`, or empty |
| `go-packages` | JSON object: module → list | Package patterns for `go test` (`./...`, `.`, `./internal/x`); an empty list means vet only. Every Go module except `test/e2e` is listed |
| `web-tests` | `"all"` or JSON list | `web/src` files for `vitest related`; an empty list skips Vitest (lint and build still run) |

Rules: a changed file selects its package directory (walking up to the nearest one) and,
unless it is a `_test.go` file, every package that imports it, transitively and across
modules. `FILE_READERS` adds the packages whose tests read the changed directory. A
module's own `go.mod`, `go.sum` or `.testcoverage.yml`, or a file under no package,
gives `["./..."]`. For web, `package.json`, `package-lock.json`, `vitest.config.ts`,
`vite.config.ts`, `tsconfig*.json`, `eslint.config.js`, `src/test/**`, `i18n/locales/**`,
a deleted `web/src` file and any other `web/` file outside `src` and `e2e` give `"all"`.

```

7c. `specs/done_023-change-based-test-selection/contracts/changed-coverage.md`, append at the
end of the `## Behaviour` list (after item 5, before `## Not changed`):
```markdown
6. A profile block marks every line from its start to its end line; a line counts as
   covered when any block over it ran. Istanbul statements are read the same way.
7. Web: when `vitest related` writes no `coverage-final.json`, the job falls back to the
   full `npm run test:cover` run instead of calling this script.
```

7d. `specs/done_023-change-based-test-selection/tasks.md`: change `- [ ] T031` … `- [ ] T040`
(ten lines) to `- [X]`. In the T033 line, replace
`extend the \`test-ci-scope\` target in \`Makefile\` to discover \`test_*.py\` in \`hack/\``
with
`add a second \`unittest discover\` line for \`test_check_changed_coverage.py\` to the \`test-ci-scope\` target in \`Makefile\` (a \`test_*.py\` glob would also run \`test_auto_label.py\`, which has its own workflow)`.

## 8. Verification (compile only, rule 8)

```sh
python3 -m py_compile hack/ci_scope.py hack/check_changed_coverage.py \
  hack/test_ci_scope.py hack/test_check_changed_coverage.py
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yaml'))"
```
No unittest, no make test-ci-scope, no lint.
