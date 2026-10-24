"""Behavioral coverage for CI module and image selection (run in CI)."""

import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import ClassVar
from unittest.mock import patch
from subprocess import CompletedProcess

from ci_scope import (E2E_AMD64_ONLY, E2E_BUCKETS, FORCE_ALL_FILES, ROOT, affected_modules, bot_matrix,
                      bucket_lists, changed_paths, e2e_outputs, full_scope_reason, image_inputs,
                      is_unmapped, load_suites, main, publish_paths, select_images, summarize,
                      verify_suites, go_packages, web_tests, CI_FILE, ci_blocks, ci_jobs,
                      widen_e2e, widen_units,
                      workspace_modules)


class ScopeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.write("go.work", "go 1.26.0\nuse (\n ./lib\n ./api\n ./test/e2e\n ./other\n)\n")
        for name in ("lib", "api", "test/e2e", "other"):
            self.write(f"{name}/go.mod", f"module example.com/{name}\ngo 1.26.0\n")
        self.write("api/main.go", 'package api\nimport "example.com/lib/helpers"\n')
        self.write("test/e2e/go.mod", "module example.com/test/e2e\nrequire example.com/api v0.0.0\n")

    def write(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def test_changed_library_selects_transitive_consumers_including_workspace_imports(self):
        self.assertEqual(affected_modules(self.root, ["lib/helpers/file.go"]),
                         ["api", "lib", "test/e2e"])

    def test_deleted_source_still_selects_its_module_and_consumers(self):
        self.assertEqual(affected_modules(self.root, ["lib/deleted.go"]),
                         ["api", "lib", "test/e2e"])

    def test_crd_files_select_api_envtests_and_transitive_consumers(self):
        self.write("go.work", "go 1.26.0\nuse (\n ./lib\n ./api\n ./test/e2e\n ./other\n ./operator\n)\n")
        self.write("operator/go.mod", "module example.com/operator\ngo 1.26.0\n")
        for path in ("operator/config/crd", "operator/config/crd/bases/gameplane.io_gameservers.yaml"):
            with self.subTest(path=path):
                self.assertEqual(affected_modules(self.root, [path]),
                                 ["api", "operator", "test/e2e"])

    def test_module_gitlink_and_files_select_validator_and_transitive_consumers(self):
        self.write("go.work", "go 1.26.0\nuse (\n ./lib\n ./api\n ./test/e2e\n ./other\n ./gp-module\n)\n")
        self.write("gp-module/go.mod", "module example.com/gp-module\ngo 1.26.0\n")
        self.write("api/module_test.go", 'package api\nimport "example.com/gp-module/validator"\n')
        for path in ("modules", "modules/cs2/template.yaml", "modules/cs2/README.md"):
            with self.subTest(path=path):
                self.assertEqual(affected_modules(self.root, [path]),
                                 ["api", "gp-module", "test/e2e"])
        self.assertEqual(affected_modules(self.root, ["modules-other/template.yaml"]), [])

    def test_docs_only_does_not_select_go_jobs(self):
        self.assertEqual(affected_modules(self.root, ["CLAUDE.md", "api/specs.md"]), [])

    def test_unrelated_module_stays_isolated(self):
        self.assertEqual(affected_modules(self.root, ["other/source.go"]), ["other"])

    def test_shared_inputs_and_dependency_changes_select_every_module(self):
        for path in ("go.work", "go.work.sum", "api/go.mod", "api/go.sum",
                     "Makefile",
                     ".github/actions/go-cache/action.yml", "hack/ci_scope.py",
                     "test/e2e/buckets.sh", "web/Dockerfile"):
            with self.subTest(path=path):
                self.assertEqual(affected_modules(self.root, [path]),
                                 ["api", "lib", "other", "test/e2e"])

    def test_unknown_diff_fails_open(self):
        self.assertIsNone(changed_paths(self.root, "missing-base", "HEAD"))
        self.assertEqual(affected_modules(self.root, None), ["api", "lib", "other", "test/e2e"])

    def test_master_push_runs_full_scope_even_for_docs_only_changes(self):
        output = self.root / "output"
        with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "modules"]), \
                patch.dict("os.environ", {"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/master",
                                          "GITHUB_OUTPUT": str(output)}), \
                patch("ci_scope.changed_paths", return_value=["README.md"]):
            main()
        values = {key: json.loads(value) for key, value in
                  (line.split("=", 1) for line in output.read_text(encoding="utf-8").splitlines())}
        self.assertEqual(values, {
            "all": True, "modules": ["api", "lib", "other", "test/e2e"],
            "lint": True, "go": True, "e2e-unit": True,
            "lint-exclude": [], "go-exclude": [],
        })

    def test_non_master_push_events_keep_docs_only_scope_empty(self):
        for event, ref in (("pull_request", "refs/heads/master"),
                           ("push", "refs/heads/feature"), ("push", "refs/tags/master")):
            with self.subTest(event=event, ref=ref):
                output = self.root / "output"
                output.write_text("", encoding="utf-8")
                with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "modules"]), \
                        patch.dict("os.environ", {"GITHUB_EVENT_NAME": event, "GITHUB_REF": ref,
                                                  "GITHUB_OUTPUT": str(output)}), \
                        patch("ci_scope.changed_paths", return_value=["README.md"]):
                    main()
                values = {key: json.loads(value) for key, value in
                          (line.split("=", 1) for line in output.read_text(encoding="utf-8").splitlines())}
                self.assertEqual(values, {
                    "all": False, "modules": [], "lint": False, "go": False, "e2e-unit": False,
                    "lint-exclude": [{"module": "api"}, {"module": "lib"},
                                     {"module": "other"}, {"module": "test/e2e"}],
                    "go-exclude": [{"module": "api"}, {"module": "lib"}, {"module": "other"}],
                })

    def test_docker_copy_sources_select_only_affected_images(self):
        self.write("api/Dockerfile", "FROM golang\nCOPY lib/ ./lib/\nCOPY api/go.mod api/go.sum ./api/\nCOPY --from=build /out/api /api\n")
        self.write("other/Dockerfile", 'FROM golang\nCOPY ["other/", "/src/"]\n')
        images = [{"component": "api"}, {"component": "other"}]
        self.assertEqual(select_images(self.root, images, ["lib/deleted.go"]), [images[0]])
        self.assertEqual(select_images(self.root, images, ["other/new.go"]), [images[1]])
        self.assertEqual(select_images(self.root, images, ["docs/install.md"]), [])

    def test_image_configuration_or_unknown_diff_rebuilds_every_image(self):
        images = [{"component": "api"}, {"component": "other"}]
        for paths in (None, ["go.work"], [".dockerignore"], ["signing/cosign.pub"],
                      [".github/workflows/publish-edge.yaml"], ["hack/ci_scope.py"]):
            with self.subTest(paths=paths):
                self.assertEqual(select_images(self.root, images, paths), images)

    def test_explicit_dockerfile_and_continuations(self):
        self.write("tunnel/Dockerfile.frp", "FROM golang\nCOPY --chown=1:1 lib/go.mod \\\n lib/go.sum /src/\nCOPY tunnel/ /src/\n")
        self.assertEqual(image_inputs(self.root / "tunnel/Dockerfile.frp"), {"lib", "tunnel"})

    def test_unclassifiable_dockerfile_fails_open(self):
        self.write("other/Dockerfile", "FROM golang\nCOPY ${SOURCE} /src/\n")
        images = [{"component": "other"}]
        self.assertEqual(select_images(self.root, images, ["lib/source.go"]), images)

    def test_bot_shards_preserve_every_input_test_exactly_once(self):
        names = ["TestMinecraft", "TestTerraria", "TestNewGame", "TestWake"]
        self.assertEqual(bot_matrix(names), {"test": names})
        for invalid in ([], ["TestMinecraft", "TestMinecraft"], ["Test.*"], ["TestA\nTestB"]):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                bot_matrix(invalid)

    def test_publish_uses_successful_baseline_and_includes_reverted_intermediate_changes(self):
        base, head = "a" * 40, "b" * 40
        replies = [CompletedProcess([], 0, '{"workflow_runs":[{"head_sha":"' + base + '"}]}'),
                   CompletedProcess([], 0, b""),
                   CompletedProcess([], 0, b"api/main.go\0api/main.go\0web/src/App.tsx\0")]
        with patch("ci_scope.subprocess.run", side_effect=replies) as run:
            paths = publish_paths(self.root, "owner/repo", "master", "push", head)
        self.write("api/Dockerfile", "FROM golang\nCOPY api/ /src/\n")
        self.write("web/Dockerfile", "FROM node\nCOPY web/ /src/\n")
        images = [{"component": "api"}, {"component": "web"}]
        self.assertEqual(select_images(self.root, images, paths), images)
        # These arguments are the external API contract: cancelled runs cannot
        # be a baseline and net-zero reverts cannot hide partially moved tags.
        self.assertIn("status=success", run.call_args_list[0].args[0])
        self.assertIn("log", run.call_args_list[-1].args[0])
        self.assertIn(f"{base}..{head}", run.call_args_list[-1].args[0])

    def test_manual_publish_and_missing_successful_baseline_build_everything(self):
        self.assertIsNone(publish_paths(self.root, "owner/repo", "master", "workflow_dispatch", "a" * 40))
        with patch("ci_scope.subprocess.run", return_value=CompletedProcess([], 0, '{"workflow_runs":[]}')):
            self.assertIsNone(publish_paths(self.root, "owner/repo", "master", "push", "a" * 40))


def make_go_workspace(test_case):
    """Build a temporary go.work workspace with four modules and a test/e2e module."""
    test_case.temp = tempfile.TemporaryDirectory()
    test_case.addCleanup(test_case.temp.cleanup)
    test_case.root = Path(test_case.temp.name)
    write_file(test_case.root, "go.work", "go 1.26.0\nuse (\n ./lib\n ./api\n ./test/e2e\n ./other\n)\n")
    for name in ("lib", "api", "test/e2e", "other"):
        write_file(test_case.root, f"{name}/go.mod", f"module example.com/{name}\ngo 1.26.0\n")
    write_file(test_case.root, "test/e2e/go.mod", "module example.com/test/e2e\nrequire example.com/api v0.0.0\n")


def write_file(root, name, content):
    """Write content to a path under root, creating parent directories first."""
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


ALL_MODULES = ["api", "lib", "other", "test/e2e"]
FULL_VALUES = {
    "all": True, "modules": ALL_MODULES, "lint": True, "go": True, "e2e-unit": True,
    "lint-exclude": [], "go-exclude": [],
}
EMPTY_INPUTS = {"SCOPE_OUTPUTS": "", "FILTER_OUTPUTS": "", "GATE_OUTPUTS": "", "BOT_MATRIX": ""}
GATE_KEYS = ("go", "web", "charts", "standalone", "e2e", "images", "weblive", "github", "specs",
             "docs", "capture_overhead", "submodules", "codegen", "kindsmoke")
SUMMARY_BOTS = ("TestMinecraft", "TestTerraria", "TestNewGame")
SUMMARY_WEB_SCOPE = {"all": "false", "modules": "[]", "lint": "false", "go": "false", "e2e-unit": "false"}
SUMMARY_WEB_GATES = {"web": "true", "images": "true", "weblive": "true"}
SUMMARY_WEB_FILTERS = {"web": "true", "web_files": json.dumps(["web/src/App.tsx"])}


class FullScopeTests(unittest.TestCase):
    """Check full-scope reasons, module selection fallbacks, and summary scope values."""
    def setUp(self):
        """Create the shared Go workspace fixture for each test."""
        make_go_workspace(self)

    def read_output(self, output):
        """Parse KEY=JSON lines from a GITHUB_OUTPUT file into a dictionary."""
        return {key: json.loads(value) for key, value in
                (line.split("=", 1) for line in output.read_text(encoding="utf-8").splitlines())}

    def run_modules(self, env, changed=None):
        """Run modules mode with patched inputs and return outputs and the changed mock."""
        output = self.root / "output"
        output.write_text("", encoding="utf-8")
        with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "modules"]), \
                patch.dict("os.environ", {**env, "GITHUB_OUTPUT": str(output)}), \
                patch("ci_scope.changed_paths", return_value=changed) as changed_mock:
            main()
        return self.read_output(output), changed_mock

    def test_full_scope_reason_values(self):
        """Check the reason returned for each event, label, and base condition."""
        self.assertEqual(full_scope_reason({"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/master"},
                                           ["README.md"]), "master push")
        self.assertIsNone(full_scope_reason({"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/feature"},
                                            ["docs/install.md"]))
        self.assertEqual(full_scope_reason({"GITHUB_EVENT_NAME": "workflow_dispatch"}, ["README.md"]),
                         "manual run")
        self.assertEqual(full_scope_reason({"CI_FULL_RUN": "true"}, ["README.md"]), "label ci: full-run")
        self.assertEqual(full_scope_reason({}, None), "unknown base")
        self.assertIsNone(full_scope_reason({}, ["README.md"]))

    def test_selection_rule_changes_force_full_scope(self):
        """Check that selection-rule file changes force full scope with that reason."""
        for path in (*sorted(FORCE_ALL_FILES), ".github/actions/go-cache/action.yml"):
            with self.subTest(path=path):
                self.assertEqual(full_scope_reason({}, [path]), "selection-rule change")

    def test_event_reasons_take_precedence(self):
        """Check that event reasons win over the full-run label and an unknown base."""
        master = {"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/master", "CI_FULL_RUN": "true"}
        self.assertEqual(full_scope_reason(master, None), "master push")
        manual = {"GITHUB_EVENT_NAME": "workflow_dispatch", "CI_FULL_RUN": "true"}
        self.assertEqual(full_scope_reason(manual, None), "manual run")
        self.assertEqual(full_scope_reason({"CI_FULL_RUN": "true"}, None), "label ci: full-run")

    def test_modules_label_runs_full_scope_without_diff(self):
        """Check that the full-run label selects every module without reading a diff."""
        values, changed = self.run_modules(
            {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/pull/7/merge", "CI_FULL_RUN": "true"},
            changed=["README.md"])
        changed.assert_not_called()
        self.assertEqual(values, FULL_VALUES)

    def test_modules_manual_run_runs_full_scope_without_diff(self):
        """Check that a manual run selects every module without reading a diff."""
        values, changed = self.run_modules(
            {"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/feature"},
            changed=["README.md"])
        changed.assert_not_called()
        self.assertEqual(values, FULL_VALUES)

    def test_selection_exception_falls_back_to_every_module(self):
        """Check that a selection exception falls back to every module."""
        output = self.root / "output"
        output.write_text("", encoding="utf-8")
        with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "modules"]), \
                patch.dict("os.environ", {"GITHUB_EVENT_NAME": "pull_request",
                                          "GITHUB_REF": "refs/pull/7/merge", "CI_FULL_RUN": "false",
                                          "GITHUB_OUTPUT": str(output)}), \
                patch("ci_scope.changed_paths", return_value=["web/src/App.tsx"]), \
                patch("ci_scope.affected_modules", side_effect=RuntimeError("boom")):
            main()
        self.assertEqual(self.read_output(output), FULL_VALUES)

    def test_summary_on_master_push_is_full_scope(self):
        """Check a master push summary reports full scope and the master push reason."""
        output = self.root / "output"
        summary = self.root / "summary.md"
        output.write_text("", encoding="utf-8")
        summary.write_text("", encoding="utf-8")
        with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "summary"]), \
                patch.dict("os.environ", {**EMPTY_INPUTS, "GITHUB_EVENT_NAME": "push",
                                          "GITHUB_REF": "refs/heads/master",
                                          "GITHUB_OUTPUT": str(output),
                                          "GITHUB_STEP_SUMMARY": str(summary)}), \
                patch("ci_scope.changed_paths") as changed:
            main()
        changed.assert_not_called()
        values = self.read_output(output)
        self.assertEqual(values["scope"], "full")
        self.assertEqual(values["reason"], "master push")
        self.assertIn("full: master push", summary.read_text(encoding="utf-8"))

    def test_summary_on_pull_request_is_partial_scope(self):
        """Check a pull request summary reports partial scope with an empty reason."""
        output = self.root / "output"
        output.write_text("", encoding="utf-8")
        with patch("ci_scope.ROOT", self.root), patch("sys.argv", ["ci_scope.py", "summary"]), \
                patch.dict("os.environ", {**EMPTY_INPUTS, "GITHUB_EVENT_NAME": "pull_request",
                                          "GITHUB_REF": "refs/pull/7/merge", "CI_FULL_RUN": "false",
                                          "GITHUB_OUTPUT": str(output),
                                          "GITHUB_STEP_SUMMARY": str(self.root / "summary.md")}), \
                patch("ci_scope.changed_paths", return_value=["web/src/App.tsx"]):
            main()
        values = self.read_output(output)
        self.assertEqual(values["scope"], "partial")
        self.assertEqual(values["reason"], "")


class SelectionSummaryTests(unittest.TestCase):
    """Check selection summary rows and Markdown rendering for web and full runs."""
    def setUp(self):
        """Create the shared Go workspace fixture for each test."""
        make_go_workspace(self)

    def summary(self, paths=None, reason=None, event="pull_request", scope=None, filters=None, gates=None):
        """Run summarize with the given inputs and return its rows and Markdown."""
        env = {
            "GITHUB_EVENT_NAME": event,
            "SCOPE_OUTPUTS": json.dumps(scope or {}),
            "FILTER_OUTPUTS": json.dumps(filters or {}),
            "GATE_OUTPUTS": json.dumps(gates or {}),
            "BOT_MATRIX": json.dumps({"test": list(SUMMARY_BOTS)}),
        }
        with patch("ci_scope.ROOT", self.root):
            return summarize(env, paths, reason)

    def web_only(self):
        """Return the summary of a web-only pull request change with web gates enabled."""
        return self.summary(paths=["web/src/App.tsx"], scope=SUMMARY_WEB_SCOPE,
                            filters=SUMMARY_WEB_FILTERS, gates=SUMMARY_WEB_GATES)

    def test_rows_have_required_keys_and_unique_suites(self):
        """Check each row has suite, decision, and reason keys and unique suite names."""
        rows, _ = self.web_only()
        for row in rows:
            with self.subTest(row=row):
                self.assertEqual(set(row), {"suite", "decision", "reason"})
                self.assertIn(row["decision"], {"run", "skip"})
        suites = [row["suite"] for row in rows]
        self.assertEqual(len(suites), len(set(suites)))

    def test_every_e2e_bucket_and_bot_test_has_a_row(self):
        """Check every e2e bucket and bot test has a selection row."""
        rows, _ = self.web_only()
        suites = {row["suite"] for row in rows}
        for bucket in E2E_BUCKETS:
            name = "e2e api-auth + ratelimit tail" if bucket == "api-auth" else f"e2e {bucket}"
            arches = "amd64" if bucket in E2E_AMD64_ONLY else "amd64, arm64"
            self.assertIn(f"{name} ({arches})", suites)
        for test in SUMMARY_BOTS:
            self.assertIn(f"e2e game bot / {test}", suites)

    def test_web_only_change_runs_web_and_skips_go_e2e(self):
        """Check a web-only change runs the web suite and skips the operator e2e suite."""
        rows, _ = self.web_only()
        by_suite = {row["suite"]: row for row in rows}
        self.assertEqual(by_suite["web"],
                         {"suite": "web", "decision": "run", "reason": "changed: web/src/App.tsx"})
        self.assertEqual(by_suite["e2e operator (amd64, arm64)"],
                         {"suite": "e2e operator (amd64, arm64)", "decision": "skip",
                          "reason": "not affected"})

    def test_citation_lists_five_files_then_remainder(self):
        """Check the web reason cites five changed files and the remaining count."""
        files = [f"web/src/f{i}.tsx" for i in range(7)]
        rows, _ = self.summary(paths=files, scope=SUMMARY_WEB_SCOPE, gates=SUMMARY_WEB_GATES,
                               filters={"web": "true", "web_files": json.dumps(files)})
        by_suite = {row["suite"]: row for row in rows}
        self.assertEqual(by_suite["web"]["reason"], "changed: " + ", ".join(files[:5]) + " (+2)")

    def test_partial_markdown_has_header_and_one_line_per_row(self):
        """Check partial Markdown has its header and one line per selection row."""
        rows, md = self.web_only()
        lines = md.splitlines()
        self.assertEqual(lines[0], "### Test selection (partial)")
        self.assertEqual(lines[1], "")
        self.assertEqual(lines[2], "| Suite | Decision | Why |")
        self.assertEqual(lines[3], "|---|---|---|")
        self.assertEqual(len(lines) - 4, len(rows))
        self.assertTrue(md.endswith("\n"))
        self.assertNotIn("full: ", md)

    def test_full_scope_marks_run_rows_and_header(self):
        """Check full-scope Markdown header and that only dependency review is skipped."""
        scope = {"all": "true", "modules": json.dumps(ALL_MODULES), "lint": "true",
                 "go": "true", "e2e-unit": "true"}
        gates = {key: "true" for key in GATE_KEYS}
        rows, md = self.summary(paths=None, reason="master push", event="push", scope=scope,
                                gates=gates, filters={})
        lines = md.splitlines()
        self.assertEqual(lines[0], "### Test selection (full)")
        self.assertIn("full: master push", lines)
        for row in rows:
            if row["decision"] == "run":
                self.assertTrue(row["reason"].startswith("full: "), row)
        skipped = {row["suite"] for row in rows if row["decision"] == "skip"}
        self.assertEqual(skipped, {"dependency review"})


class E2ESelectionTests(unittest.TestCase):
    """Spec 023 US1 acceptance scenarios 1-4 against this repository's suite map (T014)."""

    def select(self, *paths):
        """Return the e2e outputs for a partial-scope change to paths."""
        return e2e_outputs(ROOT, list(paths), None)

    def test_audit_network_isolation_has_a_run_pattern(self):
        """Affected components and the owning test file select the audit bucket."""
        for path in ("operator/internal/controller/gameserver_controller.go",
                     "api/cmd/main.go",
                     "agent/cmd/main.go",
                     "test/e2e/network_isolation_e2e_test.go"):
            with self.subTest(path=path):
                values = self.select(path)
                self.assertIn("TestNetworkIsolation_EnforcedAndUpgrade",
                              values["e2e-run"].get("network-isolation", ""))

    def test_audit_monitoring_has_a_run_pattern(self):
        """Metric producers and the owning test file select the real scrape fixture."""
        for path in ("operator/internal/controller/gameserver_controller.go",
                     "api/cmd/main.go", "agent/internal/status/performance.go",
                     "tunnel/relay_health.go", "test/e2e/monitoring_e2e_test.go"):
            with self.subTest(path=path):
                values = self.select(path)
                self.assertIn("TestMonitoring_DiscoveryPerformanceAndPolicy",
                              values["e2e-run"].get("monitoring", ""))

    def test_audit_management_placement_has_a_run_pattern(self):
        """Affected components and the owning test file select the audit bucket."""
        for path in ("operator/cmd/main.go",
                     "api/cmd/main.go",
                     "agent/cmd/main.go",
                     "test/e2e/management_placement_e2e_test.go"):
            with self.subTest(path=path):
                values = self.select(path)
                self.assertIn("TestManagementPlacementAndDisruption",
                              values["e2e-run"].get("management-placement", ""))

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
        values = self.select("docs/architecture.md", "specs/023-change-based-test-selection/plan.md")
        self.assertEqual(values["e2e-run"], {})
        self.assertFalse(values["bots-any"])

    def test_universal_path_change_selects_every_suite_and_turns_on_the_gate(self):
        values = self.select(".dockerignore")
        buckets = bucket_lists(ROOT)
        self.assertFalse(values["all"])
        self.assertTrue(values["universal"])
        for bucket in E2E_BUCKETS:
            self.assertEqual(values["e2e-run"][bucket], "^(" + "|".join(buckets[bucket]) + ")$")

    def test_component_change_leaves_the_universal_gate_off(self):
        self.assertFalse(self.select("agent/internal/console/console.go")["universal"])
        self.assertFalse(self.select("tools/x.sh")["universal"])


class VerifySuitesTests(unittest.TestCase):
    """verify-suites rules 1, 2 and 5 from contracts/suite-components.md (T015)."""

    MODULES: ClassVar[dict[str, str]] = {"api": "example.com/api", "agent": "example.com/agent"}

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

    def test_bucket_registry_matches_workflow_matrices(self):
        """Every matrix bucket emits a run pattern and reports its real architectures."""
        text = (ROOT / CI_FILE).read_text(encoding="utf-8")
        owner, _ = ci_blocks(text)
        matrices = {}
        for number, line in enumerate(text.splitlines(), 1):
            job = owner[number]
            if job in {"e2e-go", "e2e-go-arm64"} and line.strip().startswith("bucket: ["):
                self.assertNotIn(job, matrices)
                matrices[job] = {name.strip() for name in line.split("[", 1)[1].split("]", 1)[0].split(",")}
        self.assertEqual(set(matrices), {"e2e-go", "e2e-go-arm64"})
        self.assertEqual(set(E2E_BUCKETS), matrices["e2e-go"] | matrices["e2e-go-arm64"])
        self.assertEqual(E2E_AMD64_ONLY, matrices["e2e-go"] - matrices["e2e-go-arm64"])

    def test_full_scope_regex_equals_buckets_sh_regex(self):
        values = e2e_outputs(ROOT, None, "master push")
        self.assertEqual(values["reason"], "master push")
        for bucket in [*E2E_BUCKETS, "ratelimit"]:
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

    def test_dependency_manifest_selects_importers_across_modules(self):
        for name in ("go.mod", "go.sum"):
            with self.subTest(name=name):
                self.assertEqual(self.packages(f"lib/{name}"),
                                 {"api": ["./internal/a", "./internal/b"], "lib": ["./..."],
                                  "other": []})
        self.assertEqual(self.packages("lib/.testcoverage.yml"),
                         {"api": [], "lib": ["./..."], "other": []})

    def test_deleted_package_selects_its_former_importers(self):
        (self.root / "lib/helpers/h.go").unlink()
        self.assertEqual(self.packages("lib/helpers/h.go"),
                         {"api": ["./internal/a", "./internal/b"], "lib": ["./..."],
                          "other": []})

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


GIT = ["git", "-c", "user.name=test", "-c", "user.email=test@example.com",
       "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
       # background gc could still be writing .git when the temp dir is removed
       "-c", "gc.auto=0", "-c", "maintenance.auto=false"]
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


CI_ARCH_FIXTURE = """name: ci
jobs:
  changes:
    steps:
      - run: echo changes
  build-images:
    needs: [changes]
    steps:
      - run: echo build
  build-images-arm64:
    needs: [changes]
    steps:
      - run: echo build arm64
  e2e-go:
    needs: [changes, build-images]
    steps:
      - run: echo e2e
  e2e-go-arm64:
    needs: [changes, build-images-arm64]
    steps:
      - run: echo e2e arm64
"""


class CiJobsArchitectureTests(unittest.TestCase):
    """An edited e2e job brings its other-architecture twin (spec 023 FR-010)."""

    def setUp(self):
        """Create a git repository holding a workflow with paired e2e jobs."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")
        write_file(self.root, CI_FILE, CI_ARCH_FIXTURE)
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

    def test_either_architecture_edit_selects_both(self):
        both = (["build-images", "build-images-arm64", "e2e-go", "e2e-go-arm64",
                 "workflow-lint"], False)
        self.assertEqual(self.edit("echo e2e\n", "echo e2e now\n"), both)
        self.assertEqual(self.edit("echo e2e arm64\n", "echo e2e arm64 now\n"), both)

    def test_job_without_twin_stays_alone(self):
        self.assertEqual(self.edit("echo build\n", "echo build now\n"),
                         (["build-images", "workflow-lint"], False))


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


if __name__ == "__main__":
    unittest.main()
