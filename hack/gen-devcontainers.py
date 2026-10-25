#!/usr/bin/env python3
"""Generate the modular dev container profiles (spec 026).

Hand-written inputs:
  .devcontainer/versions.toml           pinned tool versions (one key per tool)
  .devcontainer/profiles.toml           profile catalogue (baseline, profiles, full)
  <folder>/system-deps.toml             system dependencies of each folder
  .devcontainer/devcontainer-lock.json  source feature lock (used by the full profile)

Generated outputs (never edit by hand):
  .devcontainer/devcontainer.json, .devcontainer/full/{setup.env,ready.txt}
  .devcontainer/<id>/{devcontainer.json,devcontainer-lock.json,setup.env,ready.txt}
  .devcontainer/tool-index.tsv
  --submodules: <submodule>/.devcontainer/* for the website and modules profiles

Contracts: specs/026-modular-dev-containers/contracts/. Standard library only.
"""

import argparse
import difflib
import json
import os
import re
import sys
import tomllib
from dataclasses import dataclass
from pathlib import Path

DEVC = ".devcontainer"
SYSTEM_DEPS = "system-deps.toml"
GO_PREFIX = "github.com/GameplanePanel/gameplane/"
GO_BUILD_TOKEN = "{go_build}"
GO_NO_BUILD = "test/e2e"
SIZES = ("component", "part", "full")
SIZE_RANK = {"component": 0, "part": 1, "full": 2}
DEP_KINDS = frozenset({"toolchain", "cli", "os-package", "service", "submodule"})
AUTO_FORWARD = frozenset({"notify", "openBrowser", "openBrowserOnce", "openPreview", "silent", "ignore"})
DEP_KEYS = frozenset({"name", "kind", "version", "reason", "optional"})
SYSTEM_DEPS_KEYS = frozenset({"component", "none", "dep"})
PROFILE_KEYS = frozenset({"size", "title", "dirs", "includes", "extras", "submodules",
                          "ports", "extensions", "checks", "sub_checks", "env", "notes"})
PORT_KEYS = frozenset({"port", "label", "onAutoForward"})
BASELINE_KEYS = frozenset({"image", "remoteUser", "runArgs", "extensions", "env", "settings", "deps"})
FULL_KEYS = frozenset({"personal_extensions", "personal_settings", "omit_ports", "omit_extensions"})
PRUNE_DIRS = frozenset({"node_modules", "bin", "vendor", "dist"})
LITERAL_VERSION = re.compile(r"^v?\d+(\.\d+)+")
GLOBAL_TRIGGERS = frozenset({
    ".devcontainer/versions.toml",
    ".devcontainer/profiles.toml",
    ".devcontainer/setup.sh",
    ".devcontainer/devcontainer-lock.json",
    "hack/gen-devcontainers.py",
    "go.work",
    "Makefile",
})

FEAT_COMMON = "ghcr.io/devcontainers/features/common-utils:2"
FEAT_GH = "ghcr.io/devcontainers/features/github-cli:1"
FEAT_GO = "ghcr.io/devcontainers/features/go:1"
FEAT_NODE = "ghcr.io/devcontainers/features/node:1"
FEAT_DOCKER = "ghcr.io/devcontainers/features/docker-in-docker:2"
FEAT_KUBE = "ghcr.io/devcontainers/features/kubectl-helm-minikube:1"
FEATURE_FOR_DEP = {
    "common-utils": FEAT_COMMON,
    "gh": FEAT_GH,
    "go": FEAT_GO,
    "node": FEAT_NODE,
    "docker": FEAT_DOCKER,
    "kubectl": FEAT_KUBE,
    "helm": FEAT_KUBE,
}
# dep name -> (setup.sh step, {<VAR>_VERSION env var: versions.toml key})
STEP_FOR_DEP = {
    "kind": ("kind", {"KIND_VERSION": "kind"}),
    "oras": ("oras", {"ORAS_VERSION": "oras"}),
    "golangci-lint": ("golangci_lint", {"GOLANGCI_LINT_VERSION": "golangci_lint"}),
    "envtest": ("envtest", {"SETUP_ENVTEST_VERSION": "setup_envtest",
                            "ENVTEST_K8S_VERSION": "envtest_k8s"}),
    "uv": ("uv", {}),
    "specify": ("specify", {"SPECIFY_VERSION": "specify"}),
    "ai-clis": ("ai_clis", {}),
    "python3": ("python3", {}),
    "pyyaml": ("pyyaml", {}),
}
FIXED_STEP_ORDER = ("submodules", "python3", "pyyaml", "kind", "oras", "golangci_lint", "envtest",
                    "go_mod_download", "npm_ci_web", "npm_ci_website", "uv", "specify", "ai_clis")
# versions.toml keys a dep consumes besides its own `version` key.
EXTRA_KEYS = {"docker": ("docker_compose",), "envtest": ("setup_envtest",)}
REGISTRY_KEYS = ("go", "node", "docker", "docker_compose", "kubectl", "helm", "kind", "oras",
                 "golangci_lint", "setup_envtest", "envtest_k8s", "specify")
# make targets listed under "Works here:". A target is listed when the profile has every
# tool in its tools set, every folder in its folders set is covered, and the profile's
# checks do not already name it. (make target, tools, folders)
MAKE_TARGETS = (
    ("make build-go", frozenset({"go"}), frozenset()),
    ("make build-web", frozenset({"node"}), frozenset({"web"})),
    ("make web-dev", frozenset({"node"}), frozenset({"web"})),
    ("make generate", frozenset({"go"}), frozenset({"operator"})),
    ("make manifests", frozenset({"go"}), frozenset({"operator"})),
    ("make module-validate", frozenset({"go"}), frozenset()),
    ("make modules-push", frozenset({"oras"}), frozenset({"modules"})),
    ("make dev-load", frozenset({"docker", "kind"}), frozenset({"deploy/kind"})),
    ("make e2e-up", frozenset({"docker", "kind", "kubectl", "helm"}), frozenset()),
    ("make dev-up", frozenset({"docker", "kind", "kubectl", "helm", "oras"}),
     frozenset({"deploy/kind"})),
)


@dataclass
class Profile:
    """One [profile.<id>] table from profiles.toml, normalised."""

    pid: str
    index: int
    size: str
    title: str
    dirs: list[str]
    includes: list[str]
    extras: list[dict]
    submodules: list[str]
    ports: list[dict]
    extensions: list[str]
    checks: list[str]
    sub_checks: list[str]
    env: dict
    notes: list[str]


@dataclass
class Model:
    """Validated inputs. Built once per run by build_model."""

    root: Path
    tools: dict
    baseline: dict
    baseline_deps: list[dict]
    profiles: list[Profile]
    personal_extensions: list[str]
    personal_settings: dict
    folder_deps: dict
    go_modules: list[str]
    go_refs: dict
    source_lock: dict
    full_omit_ports: set
    full_omit_extensions: set


@dataclass
class Resolved:
    """A profile after composition: what its generated files are built from."""

    pid: str
    index: int
    size: str
    title: str
    covered: list[str]
    go_modules: list[str]
    deps: list[dict]
    submodules: list[str]
    ports: list[dict]
    extensions: list[str]
    checks: list[str]
    sub_checks: list[str]
    env: dict
    notes: list[str]
    settings: dict
    sub_folder: str | None


@dataclass
class Context:
    """Cross-profile lookups for ready.txt and tool-index.tsv."""

    all_tools: set
    tool_profiles: dict
    declaring: dict


# ---------------------------------------------------------------- loading and validation

def load_toml(path: Path, label: str, errors: list[str]) -> dict:
    try:
        with path.open("rb") as fh:
            return tomllib.load(fh)
    except FileNotFoundError:
        errors.append(f"{label}: file not found")
    except (tomllib.TOMLDecodeError, OSError) as exc:
        errors.append(f"{label}: cannot read TOML: {exc}")
    return {}


def load_versions(root: Path, errors: list[str]) -> dict:
    label = f"{DEVC}/versions.toml"
    doc = load_toml(root / DEVC / "versions.toml", label, errors)
    for key in sorted(set(doc) - {"tools"}):
        errors.append(f"{label}: unknown table {key!r}")
    tools = doc.get("tools", {})
    for key, value in tools.items():
        if not isinstance(value, str):
            errors.append(f"{label}: [tools].{key} must be a string")
    return {key: str(value) for key, value in tools.items()}


def check_dep(dep: dict, label: str, tools: dict, errors: list[str]) -> None:
    """Shape and registry rules shared by system-deps.toml, extras and the baseline."""
    name = dep.get("name")
    if not isinstance(name, str) or not name:
        errors.append(f"{label}: a dep has no name")
        return
    for key in sorted(set(dep) - DEP_KEYS):
        errors.append(f"{label}: dep {name!r} has unknown key {key!r}")
    kind = dep.get("kind")
    if kind not in DEP_KINDS:
        errors.append(f"{label}: dep {name!r} kind {kind!r} is not one of {sorted(DEP_KINDS)}")
    reason = dep.get("reason")
    if not isinstance(reason, str) or not reason.strip():
        errors.append(f"{label}: dep {name!r} needs a non-empty reason")
    version = dep.get("version")
    if version is not None and version not in tools:
        errors.append(f"{label}: dep {name!r} version {version!r} is not a versions.toml key "
                      "(literal versions are not allowed, FR-007)")
    if "optional" in dep and not isinstance(dep["optional"], bool):
        errors.append(f"{label}: dep {name!r} optional must be true or false")
    registered = name in FEATURE_FOR_DEP or name in STEP_FOR_DEP
    if not registered and (kind != "os-package" or version is not None):
        errors.append(f"{label}: dep {name!r} is not in the generator's dep registry; "
                      "an unregistered dep must be kind 'os-package' with no version")


def load_folder_deps(root: Path, folder: str, tools: dict, errors: list[str]) -> list[dict]:
    label = f"{folder}/{SYSTEM_DEPS}"
    doc = load_toml(root / folder / SYSTEM_DEPS, label, errors)
    for key in sorted(set(doc) - SYSTEM_DEPS_KEYS):
        errors.append(f"{label}: unknown key {key!r} (rule 6)")
    if doc.get("component") != folder:
        errors.append(f"{label}: component must equal {folder!r} (rule 1)")
    none = doc.get("none", False)
    if not isinstance(none, bool):
        errors.append(f"{label}: none must be true or false")
    deps = doc.get("dep", [])
    if none is True and deps:
        errors.append(f"{label}: none = true excludes [[dep]] entries (rule 5)")
    seen: set = set()
    for dep in deps:
        check_dep(dep, label, tools, errors)
        name = dep.get("name")
        if name in seen:
            errors.append(f"{label}: duplicate dep {name!r}")
        seen.add(name)
    return list(deps)


def discover_folders(root: Path) -> list[str]:
    found = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if not d.startswith(".") and d not in PRUNE_DIRS]
        current = Path(dirpath)
        if current != root and SYSTEM_DEPS in filenames:
            found.append(current.relative_to(root).as_posix())
    return sorted(found)


def load_go_work(root: Path, errors: list[str]) -> list[str]:
    try:
        text = (root / "go.work").read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"go.work: cannot read ({exc.strerror})")
        return []
    match = re.search(r"^use \((.*?)^\)", text, re.S | re.M)
    if match is None:
        errors.append("go.work: no use (...) block found")
        return []
    modules = []
    for line in match.group(1).splitlines():
        entry = line.strip()
        if entry and not entry.startswith("//"):
            modules.append(entry.removeprefix("./"))
    return modules


def match_go_module(token: str, go_modules: list[str]) -> str | None:
    best = None
    for module in go_modules:
        prefix = GO_PREFIX + module
        if (token == prefix or token.startswith(prefix + "/")) \
                and (best is None or len(module) > len(best)):
            best = module
    return best


def load_go_refs(root: Path, go_modules: list[str], errors: list[str]) -> dict:
    refs: dict = {}
    for module in go_modules:
        refs[module] = []
        try:
            text = (root / module / "go.mod").read_text(encoding="utf-8")
        except OSError as exc:
            errors.append(f"{module}/go.mod: cannot read ({exc.strerror})")
            continue
        for token in re.findall(r"github\.com/GameplanePanel/gameplane/[\w./-]+", text):
            target = match_go_module(token, go_modules)
            if target and target != module and target not in refs[module]:
                refs[module].append(target)
    return refs


def load_source_lock(root: Path, errors: list[str]) -> dict:
    path = root / DEVC / "devcontainer-lock.json"
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"{DEVC}/devcontainer-lock.json: cannot read ({exc})")
        return {"features": {}}
    return data


def check_makefile_pins(root: Path, tools: dict, errors: list[str]) -> None:
    try:
        text = (root / "Makefile").read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"Makefile: cannot read ({exc.strerror})")
        return
    pin = re.search(r"^ENVTEST_K8S_VERSION\s*\?=\s*(\S+)", text, re.M)
    if pin is None:
        errors.append("Makefile: ENVTEST_K8S_VERSION ?= pin not found (OD-5)")
    elif pin.group(1) != tools.get("envtest_k8s"):
        errors.append(f"Makefile: ENVTEST_K8S_VERSION {pin.group(1)} differs from "
                      f"versions.toml envtest_k8s {tools.get('envtest_k8s')} (OD-5)")
    refs = re.findall(r"setup-envtest@(\S+)", text)
    if not refs:
        errors.append("Makefile: no setup-envtest@ ref found (OD-5)")
    for ref in refs:
        if ref != tools.get("setup_envtest"):
            errors.append(f"Makefile: setup-envtest@{ref} differs from "
                          f"versions.toml setup_envtest {tools.get('setup_envtest')} (OD-5)")


def scan_literals(node, trail: list[str], tools: dict, errors: list[str]) -> None:
    """Reject literal versions anywhere in profiles.toml (FR-007)."""
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "version" and isinstance(value, str) and value not in tools:
                errors.append(f"{DEVC}/profiles.toml {'.'.join(trail) or '(root)'}: version {value!r} "
                              "is not a versions.toml key (FR-007)")
            scan_literals(value, trail + [str(key)], tools, errors)
    elif isinstance(node, list):
        for item in node:
            scan_literals(item, trail, tools, errors)
    elif isinstance(node, str) and LITERAL_VERSION.match(node):
        errors.append(f"{DEVC}/profiles.toml {'.'.join(trail)}: literal version {node!r} "
                      "(FR-007); name a versions.toml key instead")


def str_list(table: dict, key: str, label: str, errors: list[str]) -> list[str]:
    value = table.get(key, [])
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        errors.append(f"{label}: {key} must be a list of strings")
        return []
    return list(value)


def build_baseline(table: dict, tools: dict, errors: list[str]) -> tuple[dict, list[dict]]:
    label = f"{DEVC}/profiles.toml [baseline]"
    for key in sorted(set(table) - BASELINE_KEYS):
        errors.append(f"{label}: unknown key {key!r}")
    for key in ("image", "remoteUser"):
        if not isinstance(table.get(key), str):
            errors.append(f"{label}: {key} must be a string")
    baseline = {
        "image": table.get("image", ""),
        "remoteUser": table.get("remoteUser", ""),
        "runArgs": str_list(table, "runArgs", label, errors),
        "extensions": str_list(table, "extensions", label, errors),
        "env": dict(table.get("env", {})),
        "settings": dict(table.get("settings", {})),
    }
    deps = list(table.get("deps", []))
    for dep in deps:
        check_dep(dep, f"{label} deps", tools, errors)
    return baseline, deps


def build_profile(pid: str, index: int, table: dict, tools: dict, errors: list[str]) -> Profile:
    label = f"{DEVC}/profiles.toml [profile.{pid}]"
    for key in sorted(set(table) - PROFILE_KEYS):
        errors.append(f"{label}: unknown key {key!r}")
    size = table.get("size")
    if size not in SIZES:
        errors.append(f"{label}: size must be one of {list(SIZES)}")
    if not isinstance(table.get("title"), str):
        errors.append(f"{label}: title must be a string")
    sub_checks = str_list(table, "sub_checks", label, errors)
    for check in sub_checks:
        if GO_BUILD_TOKEN in check:
            errors.append(f"{label}: sub_checks takes literal commands, not {GO_BUILD_TOKEN}")
    extras = list(table.get("extras", []))
    for dep in extras:
        check_dep(dep, f"{label} extras", tools, errors)
    ports = list(table.get("ports", []))
    for port in ports:
        if set(port) != PORT_KEYS:
            errors.append(f"{label}: port entries need exactly {sorted(PORT_KEYS)}")
        if not isinstance(port.get("port"), int):
            errors.append(f"{label}: port must be an integer")
        if port.get("onAutoForward") not in AUTO_FORWARD:
            errors.append(f"{label}: onAutoForward {port.get('onAutoForward')!r} is not valid")
    return Profile(
        pid=pid, index=index, size=size or "", title=str(table.get("title", pid)),
        dirs=str_list(table, "dirs", label, errors),
        includes=str_list(table, "includes", label, errors),
        extras=extras,
        submodules=str_list(table, "submodules", label, errors),
        ports=ports,
        extensions=str_list(table, "extensions", label, errors),
        checks=str_list(table, "checks", label, errors),
        sub_checks=sub_checks,
        env=dict(table.get("env", {})),
        notes=str_list(table, "notes", label, errors),
    )


def build_profiles(table: dict, tools: dict, errors: list[str]) -> list[Profile]:
    profiles = [build_profile(pid, index, body, tools, errors)
                for index, (pid, body) in enumerate(table.items())]
    fulls = [p for p in profiles if p.size == "full"]
    components = {p.pid for p in profiles if p.size == "component"}
    if len(fulls) != 1:
        errors.append(f"{DEVC}/profiles.toml: exactly one profile must have size = \"full\"")
    for prof in profiles:
        label = f"{DEVC}/profiles.toml [profile.{prof.pid}]"
        if prof.size == "component" and (prof.includes or not prof.dirs):
            errors.append(f"{label}: a component lists dirs and no includes")
        if prof.size == "part":
            if not prof.includes:
                errors.append(f"{label}: a part must list includes")
            for inc in prof.includes:
                if inc not in components:
                    errors.append(f"{label}: includes {inc!r} is not a component profile")
        if prof.size == "full" and (prof.dirs or prof.includes):
            errors.append(f"{label}: the full profile has no dirs or includes")
    return profiles


def build_full_personal(doc: dict, errors: list[str]) -> tuple[list[str], dict]:
    table = doc.get("full", {})
    for key in sorted(set(table) - FULL_KEYS):
        errors.append(f"{DEVC}/profiles.toml [full]: unknown key {key!r}")
    exts = table.get("personal_extensions", [])
    if not isinstance(exts, list) or not all(isinstance(e, str) for e in exts):
        errors.append(f"{DEVC}/profiles.toml [full]: personal_extensions must be a list of strings")
        exts = []
    return list(exts), dict(table.get("personal_settings", {}))


def build_full_omits(doc: dict, errors: list[str]) -> tuple[set, set]:
    """[full] omit_ports / omit_extensions: what the full profile leaves out of the union."""
    table = doc.get("full", {})
    ports = table.get("omit_ports", [])
    exts = table.get("omit_extensions", [])
    if not isinstance(ports, list) or not all(isinstance(p, int) for p in ports):
        errors.append(f"{DEVC}/profiles.toml [full]: omit_ports must be a list of integers")
        ports = []
    if not isinstance(exts, list) or not all(isinstance(e, str) for e in exts):
        errors.append(f"{DEVC}/profiles.toml [full]: omit_extensions must be a list of strings")
        exts = []
    return set(ports), set(exts)


def build_model(root: Path) -> tuple[Model | None, list[str]]:
    errors: list[str] = []
    tools = load_versions(root, errors)
    for key in REGISTRY_KEYS:
        if key not in tools:
            errors.append(f"versions.toml: key {key!r} is required by the generator registry")
    doc = load_toml(root / DEVC / "profiles.toml", f"{DEVC}/profiles.toml", errors)
    scan_literals(doc, [], tools, errors)
    baseline, baseline_deps = build_baseline(doc.get("baseline", {}), tools, errors)
    profiles = build_profiles(doc.get("profile", {}), tools, errors)
    personal_ext, personal_settings = build_full_personal(doc, errors)
    omit_ports, omit_extensions = build_full_omits(doc, errors)
    folders = discover_folders(root)
    folder_deps = {folder: load_folder_deps(root, folder, tools, errors) for folder in folders}
    go_modules = load_go_work(root, errors)
    go_refs = load_go_refs(root, go_modules, errors)
    check_makefile_pins(root, tools, errors)
    model = Model(root=root, tools=tools, baseline=baseline, baseline_deps=baseline_deps,
                  profiles=profiles, personal_extensions=personal_ext,
                  personal_settings=personal_settings, folder_deps=folder_deps,
                  go_modules=go_modules, go_refs=go_refs,
                  source_lock=load_source_lock(root, errors),
                  full_omit_ports=omit_ports, full_omit_extensions=omit_extensions)
    return model, errors


# ---------------------------------------------------------------- composition

def closure(base: list[str], model: Model) -> list[str]:
    """Add the in-repo Go modules each covered Go module references (transitively)."""
    ordered: list[str] = []
    for folder in base:
        if folder not in ordered:
            ordered.append(folder)
    members = set(ordered)
    todo = [folder for folder in ordered if folder in model.go_refs]
    while todo:
        module = todo.pop()
        for ref in model.go_refs.get(module, []):
            if ref not in members:
                members.add(ref)
                ordered.append(ref)
                todo.append(ref)
    return ordered


def own_go_modules(model: Model, prof: Profile) -> list[str]:
    covered = set(closure(prof.dirs + prof.submodules, model))
    return [m for m in model.go_modules if m in covered]


def expand_checks(checks: list[str], go_modules: list[str]) -> list[str]:
    out: list[str] = []
    for check in checks:
        if check == GO_BUILD_TOKEN:
            out.extend(f"cd {m} && go build ./..." for m in go_modules if m != GO_NO_BUILD)
        else:
            out.append(check)
    return out


def union(lists) -> list:
    out: list = []
    for values in lists:
        for value in values:
            if value not in out:
                out.append(value)
    return out


def merge_deps(deps: list[dict]) -> list[dict]:
    merged: dict = {}
    for dep in deps:
        merged.setdefault(dep["name"], dep)
    return list(merged.values())


def merge_ports(profiles: list[Profile]) -> list[dict]:
    out: list[dict] = []
    seen: set = set()
    for prof in profiles:
        for port in prof.ports:
            if port["port"] not in seen:
                seen.add(port["port"])
                out.append(dict(port))
    return out


def resolve_profiles(model: Model) -> dict[str, Resolved]:
    resolved: dict[str, Resolved] = {}
    for prof in model.profiles:
        if prof.size == "full":
            scope = list(model.profiles)
            base = list(model.folder_deps) + [f for sp in scope for f in sp.dirs + sp.submodules]
            drop_ports = model.full_omit_ports
            drop_extensions = model.full_omit_extensions
            extensions_extra = list(model.personal_extensions)
            settings = {**model.baseline["settings"], **model.personal_settings}
        else:
            wanted = set(prof.includes) | {prof.pid}
            scope = [sp for sp in model.profiles if sp.pid in wanted]
            base = [f for sp in scope for f in sp.dirs + sp.submodules]
            drop_ports = set()
            drop_extensions = set()
            extensions_extra = []
            settings = dict(model.baseline["settings"])
        covered = closure(base, model)
        extras = [d for sp in scope for d in sp.extras]
        deps = merge_deps(model.baseline_deps
                          + [d for f in covered for d in model.folder_deps.get(f, [])]
                          + extras)
        submodules = union([sp.submodules for sp in scope]
                           + [[d["name"] for d in deps if d["kind"] == "submodule"]])
        checks = union([expand_checks(sp.checks, own_go_modules(model, sp)) for sp in scope])
        env: dict = {}
        for sp in scope:
            for key, value in sp.env.items():
                env.setdefault(key, value)
        sub_folder = None
        if prof.size == "component" and len(prof.dirs) == 1 and prof.submodules == prof.dirs:
            sub_folder = prof.dirs[0]
        resolved[prof.pid] = Resolved(
            pid=prof.pid, index=prof.index, size=prof.size, title=prof.title,
            covered=covered,
            go_modules=[m for m in model.go_modules if m in covered],
            deps=deps,
            submodules=submodules,
            ports=[p for p in merge_ports(scope) if p["port"] not in drop_ports],
            extensions=[e for e in union([sp.extensions for sp in scope] + [extensions_extra])
                        if e not in drop_extensions],
            checks=checks,
            sub_checks=list(prof.sub_checks),
            env=env,
            notes=union([sp.notes for sp in scope]),
            settings=settings,
            sub_folder=sub_folder,
        )
    return resolved


def feature_options(res: Resolved, tools: dict) -> dict:
    names = {d["name"] for d in res.deps}
    feats: dict = {}
    if "common-utils" in names:
        feats[FEAT_COMMON] = {"installZsh": "true", "configureZshAsDefaultShell": "false",
                              "username": "vscode"}
    if "gh" in names:
        feats[FEAT_GH] = {}
    if "go" in names:
        feats[FEAT_GO] = {"version": tools["go"]}
    if "node" in names:
        feats[FEAT_NODE] = {"version": tools["node"]}
    if "docker" in names:
        feats[FEAT_DOCKER] = {"version": tools["docker"], "moby": True,
                              "dockerDashComposeVersion": tools["docker_compose"]}
    if names & {"kubectl", "helm"}:
        feats[FEAT_KUBE] = {
            "version": tools["kubectl"] if "kubectl" in names else "none",
            "helm": tools["helm"] if "helm" in names else "none",
            "minikube": "none",
        }
    return feats


def steps_for(res: Resolved, sub_variant: bool) -> tuple[list[str], list[str]]:
    names = [d["name"] for d in res.deps]
    present = {STEP_FOR_DEP[n][0] for n in names if n in STEP_FOR_DEP}
    if not sub_variant and res.submodules:
        present.add("submodules")
    if res.go_modules:
        present.add("go_mod_download")
    if "web" in res.covered:
        present.add("npm_ci_web")
    if "website" in res.covered:
        present.add("npm_ci_website")
    steps = [s for s in FIXED_STEP_ORDER if s in present]
    optional = {STEP_FOR_DEP[d["name"]][0] for d in res.deps
                if d.get("optional") and d["name"] in STEP_FOR_DEP}
    return steps, [s for s in FIXED_STEP_ORDER if s in optional]


def version_vars(res: Resolved, tools: dict) -> dict:
    out: dict = {}
    for dep in res.deps:
        for var, key in STEP_FOR_DEP.get(dep["name"], ("", {}))[1].items():
            out[var] = tools[key]
    return out


def ready_path(res: Resolved) -> str:
    if res.size == "full":
        return f"{DEVC}/full/ready.txt"
    return f"{DEVC}/{res.pid}/ready.txt"


def setup_env_bytes(res: Resolved, tools: dict, sub_variant: bool) -> bytes:
    steps, optional = steps_for(res, sub_variant)
    values = {
        "PROFILE": res.pid,
        "STEPS": " ".join(steps),
        "OPTIONAL_STEPS": " ".join(optional),
        **version_vars(res, tools),
        "GO_MODULES": " ".join(res.go_modules),
        "SUBMODULES": "" if sub_variant else " ".join(res.submodules),
        "WEBSITE_DIR": "." if sub_variant else "website",
        "MODULES_DIR": "." if sub_variant else "modules",
        "READY": f"{DEVC}/ready.txt" if sub_variant else ready_path(res),
    }
    for key, value in values.items():
        if any(ch in value for ch in '"$`\\\n'):
            raise ValueError(f"setup.env value for {key} needs quoting: {value!r}")
    text = "".join(f'{key}="{values[key]}"\n' for key in sorted(values))
    return text.encode("utf-8")


def feature_ids(res: Resolved, tools: dict) -> list[str]:
    return sorted(feature_options(res, tools))


def lock_bytes(model: Model, res: Resolved) -> bytes:
    features = model.source_lock.get("features", {})
    subset = {fid: features[fid] for fid in feature_ids(res, model.tools) if fid in features}
    return json_bytes({"features": subset})


def json_bytes(obj) -> bytes:
    return (json.dumps(obj, indent="\t", sort_keys=True) + "\n").encode("utf-8")


def devcontainer_bytes(model: Model, res: Resolved, post_command: str) -> bytes:
    base = model.baseline
    extensions = sorted(set(base["extensions"]) | set(res.extensions))
    obj: dict = {
        "name": f"Gameplane · {res.title}",
        "image": base["image"],
        "features": feature_options(res, model.tools),
        "runArgs": list(base["runArgs"]),
        "containerEnv": {**base["env"], **res.env},
        "postCreateCommand": post_command,
        "remoteUser": base["remoteUser"],
        "customizations": {"vscode": {"extensions": extensions,
                                      "settings": dict(res.settings)}},
    }
    if res.ports:
        obj["forwardPorts"] = [p["port"] for p in res.ports]
        obj["portsAttributes"] = {
            str(p["port"]): {"label": p["label"], "onAutoForward": p["onAutoForward"]}
            for p in res.ports
        }
    return json_bytes(obj)


def build_context(model: Model, resolved: dict[str, Resolved]) -> Context:
    order = sorted(resolved.values(), key=lambda r: (SIZE_RANK[r.size], r.index))
    tool_profiles: dict = {}
    for res in order:
        for dep in res.deps:
            tool_profiles.setdefault(dep["name"], [])
            if res.pid not in tool_profiles[dep["name"]]:
                tool_profiles[dep["name"]].append(res.pid)
    declaring: dict = {}
    for tool in tool_profiles:
        declaring[tool] = declaring_dir(model, tool)
    return Context(all_tools=set(tool_profiles), tool_profiles=tool_profiles, declaring=declaring)


def declaring_dir(model: Model, tool: str) -> str:
    if any(d["name"] == tool for d in model.baseline_deps):
        return "baseline"
    folders = sorted(f for f, deps in model.folder_deps.items()
                     if any(d["name"] == tool for d in deps))
    if folders:
        return folders[0]
    for prof in model.profiles:
        if any(d["name"] == tool for d in prof.extras):
            return f"extras:{prof.pid}"
    return "baseline"


def need_lines(res: Resolved, ctx: Context) -> list[str]:
    names = {d["name"] for d in res.deps}
    owners: dict = {}
    for tool in sorted(ctx.all_tools - names):
        owners.setdefault(ctx.tool_profiles[tool][0], []).append(tool)
    return [f'Need something else? {", ".join(tools)} → profile "{owner}"'
            for owner, tools in owners.items()]


def ready_bytes(res: Resolved, ctx: Context, sub_folder: str | None) -> bytes:
    names = {d["name"] for d in res.deps}
    lines = [f"[devcontainer] ready: Gameplane · {res.title}", "", "Works here:"]
    # A submodule copy runs inside its own repo, so it lists only sub_checks (no main-repo
    # make targets or cd-prefixed commands).
    for command in (res.sub_checks if sub_folder else res.checks):
        lines.append("  " + command)
    if sub_folder is None:
        for target, tools, folders in MAKE_TARGETS:
            if tools <= names and folders <= set(res.covered) \
                    and not any(target in check for check in res.checks):
                lines.append(f"  {target}")
    lines.append("")
    if res.ports:
        lines.append("Ports: " + ", ".join(f"{p['port']} {p['label']}" for p in res.ports))
    else:
        lines.append("Ports: none")
    lines.extend(res.notes)
    lines.append("Tests and lint run in CI (CLAUDE.md rule 8).")
    if sub_folder is None:
        lines.extend(need_lines(res, ctx))
    return ("\n".join(lines) + "\n").encode("utf-8")


def tool_index_bytes(ctx: Context) -> bytes:
    rows = [f"{tool}\t{','.join(ctx.tool_profiles[tool])}\t{ctx.declaring[tool]}"
            for tool in sorted(ctx.all_tools)]
    return ("\n".join(rows) + "\n").encode("utf-8")


# ---------------------------------------------------------------- rendering

def render_main(model: Model, resolved: dict[str, Resolved], ctx: Context) -> dict[str, bytes]:
    out: dict[str, bytes] = {}
    for res in resolved.values():
        if res.size == "full":
            out[f"{DEVC}/devcontainer.json"] = devcontainer_bytes(
                model, res, "bash .devcontainer/setup.sh .devcontainer/full/setup.env")
            base = f"{DEVC}/full"
        else:
            base = f"{DEVC}/{res.pid}"
            out[f"{base}/devcontainer.json"] = devcontainer_bytes(
                model, res, f"bash .devcontainer/setup.sh {base}/setup.env")
            out[f"{base}/devcontainer-lock.json"] = lock_bytes(model, res)
        out[f"{base}/setup.env"] = setup_env_bytes(res, model.tools, sub_variant=False)
        out[f"{base}/ready.txt"] = ready_bytes(res, ctx, None)
    out[f"{DEVC}/tool-index.tsv"] = tool_index_bytes(ctx)
    return out


def render_submodule_copies(model: Model, resolved: dict[str, Resolved], ctx: Context,
                            folders: set[str] | None = None) -> dict[str, bytes]:
    """website/.devcontainer and modules/.devcontainer copies. `folders` limits them."""
    out: dict[str, bytes] = {}
    setup_src = (model.root / DEVC / "setup.sh").read_bytes()
    for res in resolved.values():
        if res.sub_folder is None or (folders is not None and res.sub_folder not in folders):
            continue
        base = f"{res.sub_folder}/{DEVC}"
        out[f"{base}/devcontainer.json"] = devcontainer_bytes(
            model, res, "bash .devcontainer/setup.sh .devcontainer/setup.env")
        out[f"{base}/devcontainer-lock.json"] = lock_bytes(model, res)
        out[f"{base}/setup.sh"] = setup_src
        out[f"{base}/setup.env"] = setup_env_bytes(res, model.tools, sub_variant=True)
        out[f"{base}/ready.txt"] = ready_bytes(res, ctx, res.sub_folder)
    return out


# ---------------------------------------------------------------- validation

def validate_resolved(model: Model, resolved: dict[str, Resolved]) -> list[str]:
    errors: list[str] = []
    for res in resolved.values():
        if res.size == "full":
            continue
        for folder in res.covered:
            if folder not in model.folder_deps:
                errors.append(f"{DEVC}/profiles.toml [profile.{res.pid}]: covers {folder}, which has "
                              f"no {folder}/{SYSTEM_DEPS} (FR-004a)")
        for feature in feature_ids(res, model.tools):
            if feature not in model.source_lock.get("features", {}):
                errors.append(f"{DEVC}/profiles.toml [profile.{res.pid}]: feature {feature} is not in "
                              f"{DEVC}/devcontainer-lock.json")
        if res.sub_folder and not res.sub_checks:
            errors.append(f"{DEVC}/profiles.toml [profile.{res.pid}]: a submodule copy needs sub_checks")
        if res.sub_checks and not res.sub_folder:
            errors.append(f"{DEVC}/profiles.toml [profile.{res.pid}]: sub_checks is read only for a "
                          f"component whose dirs are its submodule")
    non_full = [r for r in resolved.values() if r.size != "full"]
    for module in model.go_modules:
        if not any(module in r.covered for r in non_full):
            errors.append(f"go.work: module {module} is covered by no profile (FR-014)")
    used: set = set()
    for dep in resolved["full"].deps if "full" in resolved else []:
        if dep.get("version"):
            used.add(dep["version"])
        used.update(EXTRA_KEYS.get(dep["name"], ()))
    for key in sorted(set(model.tools) - used):
        errors.append(f"{DEVC}/versions.toml: key {key!r} is used by no dependency")
    return errors


def validate_lock_all(model: Model, resolved: dict[str, Resolved]) -> list[str]:
    errors: list[str] = []
    for res in resolved.values():
        for feature in feature_ids(res, model.tools):
            if feature not in model.source_lock.get("features", {}):
                errors.append(f"{res.pid}: feature {feature} missing from {DEVC}/devcontainer-lock.json")
    return errors


def affected_ids(model: Model, resolved: dict[str, Resolved], paths: list[str]) -> list[str]:
    all_ids = [p.pid for p in model.profiles]
    hit: set = {"full"}
    for raw in paths:
        rel = Path(raw)
        if rel.is_absolute():
            try:
                rel = rel.relative_to(model.root)
            except ValueError:
                continue  # outside the repository: no profile covers it
        text = rel.as_posix().removeprefix("./")
        if text in GLOBAL_TRIGGERS:
            return all_ids
        for pid, res in resolved.items():
            if any(text == f or text.startswith(f + "/") for f in res.covered):
                hit.add(pid)
    return [pid for pid in all_ids if pid in hit]


# ---------------------------------------------------------------- output

def check_outputs(root: Path, outputs: dict[str, bytes]) -> int:
    stale = 0
    for rel in sorted(outputs):
        want = outputs[rel]
        path = root / rel
        have = path.read_bytes() if path.is_file() else None
        if have == want:
            continue
        stale += 1
        print(f"stale: {rel}")
        if have is None:
            print(f"  missing on disk: {rel}")
            continue
        diff = difflib.unified_diff(
            have.decode("utf-8", "replace").splitlines(keepends=True),
            want.decode("utf-8", "replace").splitlines(keepends=True),
            fromfile=f"{rel} (disk)", tofile=f"{rel} (generated)")
        sys.stdout.writelines(diff)
    return 1 if stale else 0


def write_outputs(root: Path, outputs: dict[str, bytes]) -> None:
    for rel, data in outputs.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)


def parse_args(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate the modular dev container profiles.")
    parser.add_argument("--check", action="store_true",
                        help="diff generated files against disk; exit 1 on drift or invalid input")
    parser.add_argument("--submodules", action="store_true",
                        help="also write website/.devcontainer and modules/.devcontainer")
    parser.add_argument("--affected", nargs="+", metavar="PATH",
                        help="print the profile ids affected by these changed paths")
    parser.add_argument("--checks", metavar="PROFILE",
                        help="print the profile's checks joined with ' && ' (CI runCmd)")
    parser.add_argument("--root", default=str(Path(__file__).resolve().parent.parent),
                        help="repository root (default: parent of hack/)")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    root = Path(args.root).resolve()
    model, errors = build_model(root)
    resolved: dict[str, Resolved] = {}
    if model is not None and not errors:
        resolved = resolve_profiles(model)
        errors.extend(validate_resolved(model, resolved))
        errors.extend(validate_lock_all(model, resolved))
    if errors or model is None:
        for message in errors:
            print(f"error: {message}", file=sys.stderr)
        return 1
    if args.affected is not None:
        for pid in affected_ids(model, resolved, args.affected):
            print(pid)
        return 0
    if args.checks is not None:
        if args.checks not in resolved:
            print(f"error: unknown profile {args.checks!r}", file=sys.stderr)
            return 1
        checks = resolved[args.checks].checks
        if not checks:
            print(f"error: profile {args.checks!r} declares no checks", file=sys.stderr)
            return 1
        # Each check is parenthesised so a `cd` in one check does not leak into the next.
        print(" && ".join(f"({check})" for check in checks))
        return 0
    ctx = build_context(model, resolved)
    outputs = render_main(model, resolved, ctx)
    if args.check:
        present = {r.sub_folder for r in resolved.values()
                   if r.sub_folder and (root / r.sub_folder / DEVC).is_dir()}
        outputs.update(render_submodule_copies(model, resolved, ctx, present))
        return check_outputs(root, outputs)
    write_outputs(root, outputs)
    if args.submodules:
        checked_out = {r.sub_folder for r in resolved.values()
                       if r.sub_folder and (root / r.sub_folder).is_dir()
                       and any((root / r.sub_folder).iterdir())}
        for res in resolved.values():
            if res.sub_folder and res.sub_folder not in checked_out:
                print(f"skipped {res.sub_folder}/{DEVC}: submodule has no checkout", file=sys.stderr)
        write_outputs(root, render_submodule_copies(model, resolved, ctx, checked_out))
    print(f"wrote {len(outputs)} generated files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
