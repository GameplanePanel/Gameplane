"""Select CI work conservatively from repository inputs, without installing Go."""

import argparse
import fnmatch
import json
import os
import re
import shlex
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
EDGE_IMAGES = [
    {"component": component} for component in (
        "operator", "api", "web", "agent", "audit-syslog-bridge",
        "telemetry-receiver", "sentinel", "capture-sidecar", "mcp-server",
    )
] + [
    {"component": f"tunnel-{name}", "dockerfile": f"tunnel/Dockerfile.{name}"}
    for name in ("frp", "tailscale", "playit")
]
# Mirrors the paths-filter `ci` filter in .github/workflows/ci.yaml; keep both in sync.
FORCE_ALL_FILES = {
    "hack/ci_scope.py", "hack/test_ci_scope.py", "Makefile", "go.work", "go.work.sum",
    "test/e2e/buckets.sh", "test/e2e/suite-components.json",
    "hack/check_changed_coverage.py", "hack/test_check_changed_coverage.py",
}
FORCE_ALL_PREFIXES = (".github/actions/",)
# Mirrors the e2e-go matrix buckets in .github/workflows/ci.yaml.
E2E_BUCKETS = ["operator", "api-auth", "api-roles", "api-rbac", "api-agent", "api-mods",
               "telemetry", "optional-components"]
SUITES_FILE = "test/e2e/suite-components.json"
# Paths no Go e2e test reads. Their own jobs keep their paths-filter gates.
NO_E2E_FILES = {
    "design.pen", "LICENSE", ".claudeignore", ".coderabbit.yaml", ".editorconfig", ".golangci.yml",
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
# Top-level areas a selection rule knows; any other path is unmapped and forces full scope.
KNOWN_DIRS = ("charts", "containers", "deploy/kind", "signing", "test/e2e", "web", "modules")
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


def is_unmapped(path, modules):
    """Return True when no selection rule knows path; such a path forces full scope."""
    if (path.endswith(".md") or path in NO_E2E_FILES or path in FORCE_ALL_FILES
            or path == ".dockerignore"):
        return False
    return not any(path == d or path.startswith(d + "/")
                   for d in NO_E2E_DIRS + KNOWN_DIRS + tuple(modules))


def workspace_modules(root):
    """Return module directories and import paths from the checked-out workspace."""
    work = (root / "go.work").read_text(encoding="utf-8")
    paths = re.findall(r"^\s*(?:use\s+)?\./([^\s)]+)", work, re.MULTILINE)
    if not paths or len(paths) != len(set(paths)):
        raise ValueError("go.work must contain distinct local module paths")
    modules = {}
    for path in paths:
        mod = (root / path / "go.mod").read_text(encoding="utf-8")
        match = re.search(r"^module\s+(\S+)", mod, re.MULTILINE)
        if not match:
            raise ValueError(f"missing module directive: {path}/go.mod")
        modules[path] = match[1]
    return modules


def changed_paths(root, base, head, *, history=False):
    """A missing/non-ancestor base means full coverage; renames include both paths."""
    if not re.fullmatch(r"[0-9a-fA-F]{40,64}", base or ""):
        return None
    try:
        subprocess.run(["git", "merge-base", "--is-ancestor", base, head],
                       cwd=root, check=True, capture_output=True)
        command = (["git", "log", "--format=", "--name-only", "--no-renames", "-z",
                    "--first-parent", "--diff-merges=first-parent", f"{base}..{head}", "--"]
                   if history else
                   ["git", "diff", "--name-only", "--no-renames", "-z", base, head, "--"])
        result = subprocess.run(
            command,
            cwd=root, check=True, capture_output=True,
        )
    except subprocess.CalledProcessError:
        return None
    return [path for path in result.stdout.decode("utf-8").split("\0") if path]


def affected_modules(root, paths):
    """Select changed modules and the reverse dependency closure, including tests."""
    modules = workspace_modules(root)
    # ci.yaml and .golangci.yml map to their own jobs through ci_jobs() (OD-3).
    shared = {"Makefile", "go.work", "go.work.sum", "test/e2e/buckets.sh"}
    if paths is None or any(
        p in shared or p.startswith((".github/actions/", "hack/ci_scope", "hack/test_ci_scope",
                                     "hack/check_changed_coverage",
                                     "hack/test_check_changed_coverage"))
        or Path(p).name in {"go.mod", "go.sum"}
        or ("Dockerfile" in Path(p).name and not any(p.startswith(m + "/") for m in modules))
        for p in paths
    ):
        return sorted(modules)

    selected = {m for m in modules for p in paths
                if p.startswith(m + "/") and not p.endswith(".md")}
    # File reads in tests do not appear in the Go import graph. Seed their
    # consumers before the empty-selection return and dependency closure.
    file_consumers = {
        "operator/config/crd": {"api"},  # API envtest CRDDirectoryPaths
        "modules": {"gp-module"},  # validator TestValidate_RealModuleCS2
    }
    for directory, consumers in file_consumers.items():
        if any(p == directory or p.startswith(directory + "/") for p in paths):
            selected.update(consumers & modules.keys())
    if not selected:
        return []
    dependencies = {}
    for module in modules:
        directory = root / module
        text = (directory / "go.mod").read_text(encoding="utf-8")
        # Workspace imports need not have a require in go.mod. Scan Go source,
        # including build-tagged tests, so those consumers are also selected.
        literals = set()
        for source in directory.rglob("*.go"):
            literals.update(re.findall(r'"([^"\r\n]+)"', source.read_text(encoding="utf-8")))
        dependencies[module] = {
            dep for dep, name in modules.items() if dep != module and (
                re.search(r"(?<![\w/])" + re.escape(name) + r"(?=[\s\"/]|$)", text)
                or any(value == name or value.startswith(name + "/") for value in literals)
            )
        }
    while True:
        consumers = {m for m, deps in dependencies.items() if deps & selected}
        if consumers <= selected:
            return sorted(selected)
        selected |= consumers


def image_inputs(dockerfile):
    """Top-level COPY/ADD inputs; None means a form we cannot safely classify."""
    text = dockerfile.read_text(encoding="utf-8").replace("\\\n", " ")
    inputs = set()
    for line in text.splitlines():
        match = re.match(r"^\s*(?:COPY|ADD)\s+(.+)", line, re.IGNORECASE)
        if not match:
            continue
        value = match[1]
        if re.match(r"--from=", value):
            continue
        value = re.sub(r"^(?:--[\w-]+=\S+\s+)+", "", value)
        words = json.loads(value) if value.startswith("[") else shlex.split(value)
        if len(words) < 2:
            return None
        for word in words[:-1]:
            if "$" in word or word in {".", "./", "/"} or word.startswith("--"):
                return None
            inputs.add(word.removeprefix("./").split("/")[0])
    return inputs


def select_images(root, images, paths):
    # Signing/configuration changes must rebuild all images even without COPYs.
    if paths is None or any(
        p in {"go.work", "go.work.sum", ".dockerignore", "signing/cosign.pub"}
        or p.startswith((".github/", "hack/ci_scope", "hack/test_ci_scope"))
        for p in paths
    ):
        return images
    selected = []
    for image in images:
        dockerfile = image.get("dockerfile", image["component"] + "/Dockerfile")
        inputs = image_inputs(root / dockerfile)
        if inputs is None or any(
            p == dockerfile or p == dockerfile + ".dockerignore"
            or any(fnmatch.fnmatchcase(p.split("/")[0], source) for source in inputs)
            for p in paths
        ):
            selected.append(image)
    return selected


def publish_paths(root, repository, branch, event, head):
    """Include unfinished earlier pushes; never advance past a failed publish."""
    if event != "push":
        return None
    try:
        result = subprocess.run([
            "gh", "api", "--method", "GET",
            f"repos/{repository}/actions/workflows/publish-edge.yaml/runs",
            "-f", f"branch={branch}", "-f", "status=success", "-f", "per_page=20",
        ], cwd=root, check=True, capture_output=True, text=True)
        runs = json.loads(result.stdout)["workflow_runs"]
        for run in runs:
            # A rerun of this same SHA must still publish, not produce an empty
            # matrix. Non-ancestors (force pushes) cannot establish a baseline.
            if run["head_sha"] == head:
                continue
            # Include intermediate changes too: an interrupted run may already
            # have moved one :edge tag before a later commit reverts its input.
            paths = changed_paths(root, run["head_sha"], head, history=True)
            if paths is not None:
                return paths
    except (subprocess.CalledProcessError, ValueError, KeyError):
        pass
    return None


def full_scope_reason(env, paths):
    """Return why every suite must run, or None when change-based selection applies."""
    if env.get("GITHUB_EVENT_NAME") == "push" and env.get("GITHUB_REF") == "refs/heads/master":
        return "master push"
    if env.get("GITHUB_EVENT_NAME") == "workflow_dispatch":
        return "manual run"
    if env.get("CI_FULL_RUN") == "true":
        return "label ci: full-run"
    if paths is None:
        return "unknown base"
    if any(p in FORCE_ALL_FILES or p.startswith(FORCE_ALL_PREFIXES) for p in paths):
        return "selection-rule change"
    return None


def flag(values, key):
    """Return True when a step-output value equals the string 'true', ignoring case."""
    return str(values.get(key, "")).lower() == "true"


def jlist(values, key):
    """Return the list stored under key, decoding JSON strings and defaulting to empty."""
    value = values.get(key) or "[]"
    if isinstance(value, list):  # BOT_MATRIX carries a real list, step outputs a JSON string
        return value
    try:
        value = json.loads(value)
    except (TypeError, ValueError):
        return []
    return value if isinstance(value, list) else []


def load_json(env, key):
    """Return the JSON object stored under key, or raise ValueError if it is not one."""
    value = json.loads(env.get(key) or "{}")
    if not isinstance(value, dict):
        raise ValueError(f"{key} must be a JSON object")
    return value


def cite(paths):
    """Format up to five changed paths, adding a count of any remainder."""
    shown = ", ".join(paths[:5])
    extra = len(paths) - 5
    return f"changed: {shown}" + (f" (+{extra})" if extra > 0 else "")


def table_cell(value):
    """Escape a value so it renders safely inside one Markdown table cell."""
    return str(value).replace("\n", " ").replace("|", "\\|")


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
        "universal": not reason and "*" in touched,
    }


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
            needs[collecting].update(re.findall(r"[A-Za-z0-9_][A-Za-z0-9_-]*",
                                                line.split("#", 1)[0]))
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


def summarize(env, paths, reason):
    """Return (rows, markdown) describing which CI suites run and why."""
    scope = load_json(env, "SCOPE_OUTPUTS")
    filters = load_json(env, "FILTER_OUTPUTS")
    gates = load_json(env, "GATE_OUTPUTS")
    bots = load_json(env, "BOT_MATRIX")
    e2e = load_json(env, "E2E_OUTPUTS")
    units = load_json(env, "UNITS_OUTPUTS")
    edited_jobs = set(jlist(env, "CI_JOBS"))
    go_selected = decoded(units, "go-packages", {})
    web_selected = decoded(units, "web-tests", "all")
    # Without the e2e step's outputs every e2e suite follows the coarse e2e gate.
    narrowed = bool(e2e)
    e2e_run = decoded(e2e, "e2e-run", {})
    e2e_why = decoded(e2e, "e2e-why", {})
    e2e_bots = decoded(e2e, "bot-matrix", {}).get("test", [])
    event = env.get("GITHUB_EVENT_NAME", "")
    modules = sorted(workspace_modules(ROOT))
    selected = set(jlist(scope, "modules"))
    changed = list(paths or [])

    def gate(key):
        """Return whether the gate output for key is true."""
        return flag(gates, key)

    def under(path, base):
        """Return True when path equals base or lies beneath it."""
        return path == base or path.startswith(base + "/")

    def filtered(*keys):
        """Return the changed files recorded in the filter outputs for each key."""
        return [name for key in keys for name in jlist(filters, f"{key}_files")]

    def go_feed():
        """Return changed paths that can affect Go builds, CRDs, or module sources."""
        return [p for p in changed if under(p, "operator/config/crd") or under(p, "modules")
                or any(under(p, m) for m in modules)]

    def module_feed(module):
        """Return changed paths for one module, or the whole Go feed when none match."""
        own = [p for p in go_feed() if under(p, module)]
        return own or go_feed()

    def e2e_feed():
        """Return the Go feed plus chart, e2e tree, and game coverage changes."""
        return go_feed() + filtered("charts", "e2etree", "gamecoverage")

    rows = []

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
        jobs = next((found for prefix, found in ROW_JOBS if suite.startswith(prefix)), ())
        if not run and edited_jobs & set(jobs):
            run, run_reason = True, "ci.yaml: lines in this job changed"
        if run and reason:
            why = f"full: {reason}"
        elif run and run_reason:
            why = run_reason
        elif run:
            files = list(dict.fromkeys(feed))
            if files:
                why = cite(files)
            else:
                why = "push event" if event == "push" else "changed: (see filters)"
        else:
            why = skip_reason
        if run and not reason:
            why = note + why
        rows.append({"suite": suite, "decision": "run" if run else "skip", "reason": why})

    for m in modules:
        add(f"lint ({m})", m in selected, module_feed(m))
    for m in modules:
        if m != "test/e2e":
            add(f"go ({m}) (amd64, arm64)", gate("go") and m in selected, module_feed(m),
                note=go_note(m))
    add("go (test/e2e)", flag(scope, "e2e-unit"), module_feed("test/e2e"))
    add("api (postgres)", "api" in selected, module_feed("api"))
    add("web", gate("web"), filtered("web"), note=web_note)
    add("web e2e (mock)", gate("web"), filtered("web"))
    add("helm lint", gate("charts"), filtered("charts"))
    add("chart render", gate("charts"), filtered("charts"))
    add("standalone Compose smoke", gate("standalone"), filtered("standalone"))
    add("workflow lint", gate("github"), filtered("github"))
    add("docs and repository checks",
        flag(scope, "lint") or gate("specs") or gate("docs"),
        filtered("docs", "specs") + go_feed())
    add("codegen drift", gate("codegen") or event == "push", filtered("codegen"))
    add("submodule freshness", gate("submodules") or event == "push", filtered("submodules"))
    add("vuln (go)", gate("go") or event == "push", go_feed())
    add("capture-sidecar CAP_NET_RAW survival", gate("go"), go_feed())
    add("dependency review", event == "pull_request",
        run_reason="pull request", skip_reason="not a pull request")
    add("telemetry default gate", True, run_reason="always runs")
    images_feed = e2e_feed() + filtered("web")
    add("build e2e images (amd64, arm64)", gate("images"), images_feed)
    add("vuln (images)", gate("images"), images_feed)
    add("e2e bucket coverage", gate("e2e"), e2e_feed())
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
    add("ingress-nginx smoke (kind)", gate("kindsmoke"), filtered("kindsmoke"))

    kind = "full" if reason else "partial"
    lines = [f"### Test selection ({kind})", ""]
    if reason:
        lines += [f"full: {reason}", ""]
    lines += ["| Suite | Decision | Why |", "|---|---|---|"]
    lines += [f"| {table_cell(row['suite'])} | {table_cell(row['decision'])} | "
              f"{table_cell(row['reason'])} |" for row in rows]
    return rows, "\n".join(lines) + "\n"


def emit(values):
    lines = "".join(f"{key}={json.dumps(value, separators=(',', ':'))}\n"
                    for key, value in values.items())
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(lines)


def bot_matrix(names):
    if not names or len(names) != len(set(names)) or any(
        not re.fullmatch(r"Test[A-Za-z0-9_]+", name) for name in names
    ):
        raise ValueError("bot-fast must contain unique, exact Go test names")
    return {"test": names}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("modules", "images", "dockerfiles", "bots", "summary",
                                         "e2e", "verify-suites", "units", "jobs"))
    parser.add_argument("--base", default="")
    parser.add_argument("--head", default="HEAD")
    args = parser.parse_args()
    if args.mode == "bots":
        result = subprocess.run(["bash", "test/e2e/buckets.sh", "list", "bot-fast"],
                                cwd=ROOT, check=True, capture_output=True, text=True)
        emit({"matrix": bot_matrix(result.stdout.splitlines())})
        return
    if args.mode == "dockerfiles":
        for image in EDGE_IMAGES:
            print(image.get("dockerfile", image["component"] + "/Dockerfile"))
        return
    if args.mode == "images":
        paths = publish_paths(ROOT, os.environ["GITHUB_REPOSITORY"],
                              os.environ["GITHUB_REF_NAME"], os.environ["GITHUB_EVENT_NAME"], args.head)
        images = select_images(ROOT, EDGE_IMAGES, paths)
        emit({"matrix": {"include": images}, "images": bool(images)})
        return
    if args.mode == "summary":
        env = os.environ
        try:
            paths = None if full_scope_reason(env, []) else changed_paths(ROOT, args.base, args.head)
            reason = full_scope_reason(env, paths) or decoded(load_json(env, "E2E_OUTPUTS"),
                                                              "reason") or None
            if reason is None and flag(load_json(env, "SCOPE_OUTPUTS"), "all"):
                reason = "selection fallback (see detect changes log)"
            rows, markdown = summarize(env, paths, reason)
        except Exception as exc:  # the summary never blocks CI (continue-on-error in ci.yaml)
            reason = f"error: {exc}"
            rows = []
            markdown = f"### Test selection (full)\n\nfull: error: {exc}\n"
        emit({"scope": "full" if reason else "partial", "reason": reason or "", "selection": rows})
        if env.get("GITHUB_STEP_SUMMARY"):
            with open(env["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as summary:
                summary.write(markdown)
        return
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
            paths, reason, jobs = scoped(env, args.base, args.head)
            values = e2e_outputs(ROOT, paths, reason)
            if jobs:
                values = widen_e2e(values, e2e_outputs(ROOT, None, "ci.yaml job edit"), jobs)
        except Exception as exc:  # any doubt selects every e2e test (FR-002)
            print(f"::warning::e2e selection failed, running every e2e test: {exc}")
            values = e2e_outputs(ROOT, None, f"error: {exc}")
        emit(values)
        return
    if args.mode == "jobs":
        try:
            _, reason, jobs = scoped(os.environ, args.base, args.head)
        except Exception as exc:  # any doubt selects the full suite (FR-002)
            print(f"::warning::ci.yaml job mapping failed, running everything: {exc}")
            reason, jobs = f"error: {exc}", []
        emit({"all": bool(reason), "reason": reason or "", "ci-jobs": jobs})
        return
    if args.mode == "units":
        env = os.environ
        try:
            paths, reason, jobs = scoped(env, args.base, args.head)
            modules = workspace_modules(ROOT)
            unmapped = [p for p in paths or [] if is_unmapped(p, modules)]
            if reason is None and unmapped:
                reason = f"unmapped path {unmapped[0]}"
            values = widen_units(unit_outputs(ROOT, paths, reason), jobs)
        except Exception as exc:  # any doubt runs every unit test (FR-002)
            print(f"::warning::unit selection failed, running every unit test: {exc}")
            values = unit_outputs(ROOT, None, f"error: {exc}")
        emit(values)
        return
    # PRs use affected work; every master push, manual run and ci: full-run
    # label is the full-suite backstop.
    env = os.environ
    try:
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
    modules = sorted(workspace_modules(ROOT))
    emit({
        "all": paths is None,
        "modules": selected,
        "lint": bool(selected),
        "go": bool(set(selected) - {"test/e2e"}),
        "e2e-unit": "test/e2e" in selected,
        # An edit inside the lint or go job of ci.yaml runs that job on every module.
        "lint-exclude": [{"module": m} for m in modules
                         if m not in selected and "lint" not in jobs],
        "go-exclude": [{"module": m} for m in modules
                       if m not in selected and m != "test/e2e" and "go" not in jobs],
    })


if __name__ == "__main__":
    main()

# Scenario 10 check.
