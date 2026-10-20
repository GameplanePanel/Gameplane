"""Behavioral coverage for CI module and image selection (run in CI)."""

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
from subprocess import CompletedProcess

from ci_scope import affected_modules, bot_matrix, changed_paths, image_inputs, main, publish_paths, select_images


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
                     "Makefile", ".golangci.yml", ".github/workflows/ci.yaml",
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


if __name__ == "__main__":
    unittest.main()
