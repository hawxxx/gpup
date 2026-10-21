import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from ci_changes import JOBS, main, select_jobs


class ChangeSelectionTest(unittest.TestCase):
    def test_components_and_shared_inputs(self):
        cases = [
            (["web/src/main.tsx"], {"web", "packaging"}),
            (["web/package-lock.json"], {"web", "packaging"}),
            (["web/dist/index.html"], {"go", "packaging"}),
            (["web/embed.go"], {"go", "packaging"}),
            (["go.sum"], {"go", "packaging"}),
            (["internal/api/server.go"], {"go", "packaging"}),
            (["benchmarks/fixture/main.go"], {"go", "packaging"}),
            (["deploy/docker/Dockerfile"], {"packaging"}),
            (["deploy/helm/gpup/Chart.yaml"], {"packaging"}),
            (["README.md", "docs/metrics.md", "index.html"], set()),
            ([".github/workflows/ci.yaml"], set(JOBS)),
            (["Makefile"], set(JOBS)),
            (["cmd/gpup/main.go", "web/src/main.tsx"], set(JOBS)),
        ]
        for paths, expected in cases:
            with self.subTest(paths=paths):
                self.assertEqual(select_jobs(paths), expected)

    def test_main_push_always_runs_every_job(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            with patch.dict(os.environ, {"EVENT": "push", "GITHUB_OUTPUT": str(output)}):
                main()
            self.assertEqual(output.read_text().count("=true\n"), len(JOBS))

    def test_failed_diff_runs_every_job(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            with patch.dict(os.environ, {
                "EVENT": "pull_request", "BASE_SHA": "base", "HEAD_SHA": "head",
                "GITHUB_OUTPUT": str(output),
            }), patch("ci_changes.subprocess.check_output", side_effect=subprocess.CalledProcessError(1, "git")):
                main()
            self.assertEqual(output.read_text().count("=true\n"), len(JOBS))

    def test_rename_out_of_go_sources_keeps_go_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", "-C", directory, *args]).decode().strip()
            git("init", "-q")
            git("config", "user.email", "test@example.invalid")
            git("config", "user.name", "CI test")
            (root / "internal").mkdir()
            (root / "internal/a.go").write_text("package example\n")
            git("add", ".")
            git("commit", "-qm", "test: initial")
            base = git("rev-parse", "HEAD")
            (root / "docs").mkdir()
            git("mv", "internal/a.go", "docs/a.go")
            git("commit", "-qm", "test: move")
            output = root / "output"
            with patch.dict(os.environ, {
                "EVENT": "pull_request", "BASE_SHA": base, "HEAD_SHA": git("rev-parse", "HEAD"),
                "GITHUB_OUTPUT": str(output),
            }), patch("ci_changes.subprocess.check_output", wraps=subprocess.check_output) as command:
                previous = os.getcwd()
                try:
                    os.chdir(directory)
                    main()
                finally:
                    os.chdir(previous)
                self.assertIn("--no-renames", command.call_args.args[0])
            self.assertIn("go=true", output.read_text())
            self.assertIn("web=false", output.read_text())


if __name__ == "__main__":
    unittest.main()
