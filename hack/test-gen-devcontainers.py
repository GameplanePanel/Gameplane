#!/usr/bin/env python3
"""Fixture tests for hack/gen-devcontainers.py (spec 026, T008).

Each test builds a small repository tree in a temporary directory and runs the
generator in-process with --root pointing at that tree. Run only in CI
(CLAUDE.md rule 8); never run locally.
"""

import contextlib
import importlib.util
import io
import json
import tempfile
import unittest
from pathlib import Path


spec = importlib.util.spec_from_file_location(
    "gen_devcontainers", Path(__file__).with_name("gen-devcontainers.py")
)
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)


GO_WORK = "go 1.26\n\nuse (\n\t./api\n\t./gp-module\n\t./operator\n)\n"

MAKEFILE = (
    "ENVTEST_K8S_VERSION ?= @PIN@\n"
    "\n"
    "envtest:\n"
    "\tsetup-envtest@@REF@ use\n"
)

VERSIONS = """[tools]
go = "1.26"
node = "24"
docker = "latest"
docker_compose = "v2"
kubectl = "1.36"
helm = "4.3.1"
kind = "v0.32.0"
oras = "1.2.0"
golangci_lint = "v2.12.2"
setup_envtest = "release-0.24"
envtest_k8s = "1.36.2"
specify = "v1.0.3"
"""

PROFILES = """[baseline]
image = "mcr.microsoft.com/devcontainers/base:ubuntu-24.04"
remoteUser = "vscode"
runArgs = ["--init"]
extensions = ["anthropic.claude-code"]
env = { GOTOOLCHAIN = "local", CI = "1" }
settings = { "editor.formatOnSave" = true }

[[baseline.deps]]
name = "common-utils"
kind = "os-package"
reason = "shell and base utilities"

[profile.api]
size = "component"
title = "API"
dirs = ["api"]
extensions = ["golang.go"]
checks = ["{go_build}"]
ports = [{ port = 8080, label = "API", onAutoForward = "silent" }]

[profile.operator]
size = "component"
title = "Operator"
dirs = ["operator"]
extensions = ["golang.go"]
checks = ["{go_build}"]
ports = [{ port = 9443, label = "Webhook", onAutoForward = "silent" }]

[profile.gp-module]
size = "component"
title = "Module CLI"
dirs = ["gp-module"]
checks = ["{go_build}"]

[profile.dashboard]
size = "part"
title = "Dashboard"
includes = ["api", "operator"]

[profile.full]
size = "full"
title = "Full"
"""

GP_MODULE_SECTION = (
    '[profile.gp-module]\n'
    'size = "component"\n'
    'title = "Module CLI"\n'
    'dirs = ["gp-module"]\n'
    'checks = ["{go_build}"]\n'
    '\n'
)

WEB_PROFILE = (
    '\n[profile.web]\n'
    'size = "component"\n'
    'title = "Web"\n'
    'dirs = ["web"]\n'
)

API_DEPS = """component = "api"

[[dep]]
name = "go"
kind = "toolchain"
version = "go"
reason = "builds the API module"

[[dep]]
name = "gh"
kind = "cli"
reason = "GitHub CLI for pull request work"

[[dep]]
name = "docker"
kind = "service"
version = "docker"
reason = "container runtime for kind"

[[dep]]
name = "kind"
kind = "cli"
version = "kind"
reason = "local clusters"

[[dep]]
name = "oras"
kind = "cli"
version = "oras"
reason = "OCI module pushes"
"""

OPERATOR_DEPS = """component = "operator"

[[dep]]
name = "gh"
kind = "cli"
reason = "GitHub CLI for pull request work"

[[dep]]
name = "kubectl"
kind = "cli"
version = "kubectl"
reason = "cluster inspection"

[[dep]]
name = "helm"
kind = "cli"
version = "helm"
reason = "chart installs"

[[dep]]
name = "envtest"
kind = "cli"
version = "envtest_k8s"
reason = "envtest control-plane binaries"
"""

GP_MODULE_DEPS = """component = "gp-module"

[[dep]]
name = "node"
kind = "toolchain"
version = "node"
reason = "scaffold preview renders web bundles"

[[dep]]
name = "golangci-lint"
kind = "cli"
version = "golangci_lint"
reason = "lint inside the container"

[[dep]]
name = "specify"
kind = "cli"
version = "specify"
reason = "spec tooling"
"""

LOCK = json.dumps({"features": {
    gen.FEAT_COMMON: {}, gen.FEAT_GH: {}, gen.FEAT_GO: {}, gen.FEAT_NODE: {},
    gen.FEAT_DOCKER: {}, gen.FEAT_KUBE: {},
}}, indent=2) + "\n"


def go_mod(module: str) -> str:
    return f"module github.com/GameplanePanel/gameplane/{module}\n\ngo 1.26\n"


def base_files() -> dict:
    return {
        "go.work": GO_WORK,
        "Makefile": MAKEFILE.replace("@PIN@", "1.36.2").replace("@REF@", "release-0.24"),
        ".devcontainer/versions.toml": VERSIONS,
        ".devcontainer/profiles.toml": PROFILES,
        ".devcontainer/devcontainer-lock.json": LOCK,
        "api/go.mod": go_mod("api"),
        "operator/go.mod": go_mod("operator"),
        "gp-module/go.mod": go_mod("gp-module"),
        "api/system-deps.toml": API_DEPS,
        "operator/system-deps.toml": OPERATOR_DEPS,
        "gp-module/system-deps.toml": GP_MODULE_DEPS,
    }


def run_gen(root: Path, *argv: str) -> tuple:
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        code = gen.main(["--root", str(root), *argv])
    return code, out.getvalue(), err.getvalue()


class GenDevcontainersTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)

    def write(self, overrides: dict | None = None) -> None:
        """Write the valid base tree; an override with value None drops that file."""
        files = base_files()
        for rel, text in (overrides or {}).items():
            if text is None:
                files.pop(rel, None)
            else:
                files[rel] = text
        for rel, text in files.items():
            path = self.root / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding="utf-8")

    def read_json(self, rel: str):
        return json.loads((self.root / rel).read_text(encoding="utf-8"))

    def test_valid_fixture_generates_and_checks_clean(self):
        self.write()
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 0, err)
        code, out, err = run_gen(self.root, "--check")
        self.assertEqual(code, 0, out + err)

    def test_literal_dep_version_rejected(self):
        self.write({"api/system-deps.toml": API_DEPS.replace('version = "go"', 'version = "1.26"')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("api/system-deps.toml", err)
        self.assertIn("literal versions are not allowed", err)

    def test_literal_profile_value_rejected(self):
        self.write({".devcontainer/profiles.toml": PROFILES.replace(
            'CI = "1" }', 'CI = "1", GO_VERSION = "1.26.1" }')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn(".devcontainer/profiles.toml", err)
        self.assertIn("literal version '1.26.1'", err)

    def test_unknown_kind_rejected(self):
        self.write({"api/system-deps.toml": API_DEPS.replace('kind = "toolchain"', 'kind = "bogus"')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("api/system-deps.toml", err)
        self.assertIn("kind 'bogus' is not one of", err)

    def test_empty_reason_rejected(self):
        self.write({"api/system-deps.toml": API_DEPS.replace(
            'reason = "builds the API module"', 'reason = "   "')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("dep 'go' needs a non-empty reason", err)

    def test_none_with_deps_rejected(self):
        self.write({"api/system-deps.toml": API_DEPS.replace(
            'component = "api"\n', 'component = "api"\nnone = true\n', 1)})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("none = true excludes [[dep]] entries (rule 5)", err)

    def test_unknown_dep_name_rejected(self):
        self.write({"api/system-deps.toml": API_DEPS + (
            '\n[[dep]]\nname = "mystery"\nkind = "cli"\nreason = "not registered"\n')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("dep 'mystery' is not in the generator's dep registry", err)

    def test_duplicate_dep_within_one_file_rejected(self):
        self.write({"operator/system-deps.toml": OPERATOR_DEPS + (
            '\n[[dep]]\nname = "gh"\nkind = "cli"\nreason = "again"\n')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("operator/system-deps.toml: duplicate dep 'gh'", err)

    def test_missing_system_deps_rejected(self):
        # web/ is covered by a profile but has no system-deps.toml (FR-004a).
        self.write({
            ".devcontainer/profiles.toml": PROFILES + WEB_PROFILE,
            "web/package.json": "{}\n",
        })
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("profiles.toml [profile.web]: covers web, which has no web/system-deps.toml", err)

    def test_go_module_in_no_profile_rejected(self):
        profiles = PROFILES.replace(GP_MODULE_SECTION, "")
        self.assertNotEqual(profiles, PROFILES)
        self.write({".devcontainer/profiles.toml": profiles})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("go.work: module gp-module is covered by no profile (FR-014)", err)

    def test_unused_version_key_rejected(self):
        self.write({".devcontainer/versions.toml": VERSIONS + 'spare = "x"\n'})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("versions.toml: key 'spare' is used by no dependency", err)

    def test_part_takes_union_of_included_components(self):
        self.write()
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 0, err)
        part = self.read_json(".devcontainer/dashboard/devcontainer.json")
        self.assertEqual(part["forwardPorts"], [8080, 9443])
        setup = (self.root / ".devcontainer/dashboard/setup.env").read_text(encoding="utf-8")
        self.assertIn('GO_MODULES="api operator"\n', setup)
        for changed in ("api/main.go", "operator/main.go"):
            with self.subTest(path=changed):
                code, out, _ = run_gen(self.root, "--affected", changed)
                self.assertEqual(code, 0)
                self.assertIn("dashboard", out.split())

    def test_duplicate_dep_collapses_across_folders(self):
        # gh is declared by api and operator; the output names it once, under the first folder.
        self.write()
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 0, err)
        rows = (self.root / ".devcontainer/tool-index.tsv").read_text(encoding="utf-8").splitlines()
        gh_rows = [row for row in rows if row.split("\t", 1)[0] == "gh"]
        self.assertEqual(gh_rows, ["gh\tapi,operator,dashboard,full\tapi"])
        full = self.read_json(".devcontainer/devcontainer.json")
        self.assertIn(gen.FEAT_GH, full["features"])

    def test_hand_edited_output_detected_by_check(self):
        self.write()
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 0, err)
        path = self.root / ".devcontainer/api/setup.env"
        path.write_text(path.read_text(encoding="utf-8") + 'HAND_EDITED="1"\n', encoding="utf-8")
        code, out, _ = run_gen(self.root, "--check")
        self.assertEqual(code, 1)
        self.assertIn("stale: .devcontainer/api/setup.env", out)

    def test_affected_system_deps_file_lists_covering_profiles(self):
        self.write()
        code, out, err = run_gen(self.root, "--affected", "api/system-deps.toml")
        self.assertEqual(code, 0, err)
        self.assertEqual(out.split(), ["api", "dashboard", "full"])

    def test_affected_versions_toml_lists_every_profile(self):
        self.write()
        code, out, err = run_gen(self.root, "--affected", ".devcontainer/versions.toml")
        self.assertEqual(code, 0, err)
        self.assertEqual(out.split(), ["api", "operator", "gp-module", "dashboard", "full"])

    def test_affected_uncovered_path_lists_only_full(self):
        self.write()
        code, out, err = run_gen(self.root, "--affected", "docs/README.md")
        self.assertEqual(code, 0, err)
        self.assertEqual(out.split(), ["full"])

    def test_makefile_envtest_k8s_pin_mismatch_rejected(self):
        self.write({"Makefile": MAKEFILE.replace("@PIN@", "1.35.0").replace("@REF@", "release-0.24")})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("Makefile: ENVTEST_K8S_VERSION 1.35.0 differs from versions.toml envtest_k8s 1.36.2", err)

    def test_makefile_setup_envtest_ref_mismatch_rejected(self):
        self.write({"Makefile": MAKEFILE.replace("@PIN@", "1.36.2").replace("@REF@", "release-0.23")})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("Makefile: setup-envtest@release-0.23 differs from versions.toml setup_envtest "
                      "release-0.24", err)

    def test_submodule_copy_lists_only_sub_checks(self):
        # A website-style component whose dirs are its own submodule: the submodule copy's
        # ready.txt lists sub_checks (run from the submodule root), not the main-repo checks.
        website_section = (
            '\n[profile.website]\n'
            'size = "component"\n'
            'title = "Website"\n'
            'dirs = ["website"]\n'
            'submodules = ["website"]\n'
            'checks = ["cd website && npm ci && npm run check"]\n'
            'sub_checks = ["npm ci && npm run check"]\n'
        )
        self.write({
            ".devcontainer/profiles.toml": PROFILES.replace(
                "\n[profile.full]", website_section + "\n[profile.full]"),
            "website/system-deps.toml": 'component = "website"\nnone = true\n',
            "website/package.json": "{}\n",
            ".devcontainer/setup.sh": "#!/usr/bin/env bash\n",
        })
        code, _, err = run_gen(self.root, "--submodules")
        self.assertEqual(code, 0, err)
        ready = (self.root / "website/.devcontainer/ready.txt").read_text(encoding="utf-8")
        self.assertIn("  npm ci && npm run check\n", ready)
        self.assertNotIn("cd website", ready)
        self.assertNotIn("make ", ready)
        setup = (self.root / "website/.devcontainer/setup.env").read_text(encoding="utf-8")
        self.assertIn('SUBMODULES=""\n', setup)
        self.assertIn('WEBSITE_DIR="."\n', setup)
        self.assertIn('READY=".devcontainer/ready.txt"\n', setup)
        main_ready = (self.root / ".devcontainer/website/ready.txt").read_text(encoding="utf-8")
        self.assertIn("  cd website && npm ci && npm run check\n", main_ready)
        code, out, err = run_gen(self.root, "--check")
        self.assertEqual(code, 0, out + err)

    def test_sub_checks_on_non_submodule_profile_rejected(self):
        self.write({".devcontainer/profiles.toml": PROFILES.replace(
            'dirs = ["gp-module"]\n', 'dirs = ["gp-module"]\nsub_checks = ["go test ./..."]\n')})
        code, _, err = run_gen(self.root)
        self.assertEqual(code, 1)
        self.assertIn("sub_checks is read only for a component whose dirs are its submodule", err)


if __name__ == "__main__":
    unittest.main()
