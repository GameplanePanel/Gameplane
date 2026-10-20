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
    shared = {"Makefile", "go.work", "go.work.sum", ".golangci.yml", "test/e2e/buckets.sh"}
    if paths is None or any(
        p in shared or p.startswith((".github/", "hack/ci_scope", "hack/test_ci_scope"))
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
    parser.add_argument("mode", choices=("modules", "images", "dockerfiles", "bots"))
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
    # PRs use affected work; every master push is the full-suite backstop.
    full = (os.environ.get("GITHUB_EVENT_NAME") == "push"
            and os.environ.get("GITHUB_REF") == "refs/heads/master")
    paths = None if full else changed_paths(ROOT, args.base, args.head)
    selected = affected_modules(ROOT, paths)
    modules = sorted(workspace_modules(ROOT))
    emit({
        "all": paths is None,
        "modules": selected,
        "lint": bool(selected),
        "go": bool(set(selected) - {"test/e2e"}),
        "e2e-unit": "test/e2e" in selected,
        "lint-exclude": [{"module": m} for m in modules if m not in selected],
        "go-exclude": [{"module": m} for m in modules if m not in selected and m != "test/e2e"],
    })


if __name__ == "__main__":
    main()
