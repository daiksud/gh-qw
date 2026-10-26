"""Offline contract tests for the identical gh-settings/gh-qw release calculator."""

import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("release-version.py")
spec = importlib.util.spec_from_file_location("release_version", SCRIPT)
versioning = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versioning)


class HistoryFixture(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)
        self.git("init", "--quiet")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.baseline("v0.3.0")

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root).decode().strip()

    def baseline(self, tag):
        (self.root / "base").write_text("base")
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", "chore: baseline")
        self.git("tag", tag)
        self.tag = tag
        self.base = self.git("rev-parse", "HEAD")

    def commit(self, message, empty=False):
        if not empty:
            file = self.root / "change"
            file.write_text(file.read_text() + "x" if file.exists() else "x")
            self.git("add", ".")
        self.git("commit", "--quiet", "--allow-empty", "-m", message)
        return self.git("rev-parse", "HEAD")

    def catalog(self, other=None):
        published = {"id": 1, "tag_name": self.tag, "draft": False, "prerelease": False,
                     "published_at": "2026-10-01T00:00:00Z"}
        return [published, *(other or [])]

    def calculate(self, other=None, allow_retry=False):
        source = self.git("rev-parse", "HEAD")
        old = pathlib.Path.cwd()
        os.chdir(self.root)
        try:
            return versioning.calculate(source, self.catalog(other), allow_retry)
        finally:
            os.chdir(old)

    def test_version_tiers(self):
        for message, expected in [
            ("fix: correct behavior", "v0.3.1"),
            ("feat(core): add command", "v0.4.0"),
            ("perf: speed up search", "v0.4.0"),
            ("refactor!: remove API", "v0.4.0"),
            ("docs: explain behavior\n\nDEPRECATED: Old usage", "v0.4.0"),
            ("fix: change API\n\nBREAKING CHANGE: Update usage", "v0.4.0"),
            ("docs: examples\n\n```text\nBREAKING CHANGE: Not a footer\n```", "v0.3.1"),
            ("chore: maintenance", "v0.3.1"),
        ]:
            with self.subTest(message=message):
                case = tempfile.TemporaryDirectory()
                self.addCleanup(case.cleanup)
                # Reset the current fixture to its published boundary.
                self.git("reset", "--hard", self.base)
                self.commit(message)
                self.assertEqual(self.calculate()[0], expected)

    def test_strongest_signal_wins(self):
        self.commit("fix: correction")
        self.commit("feat: feature")
        self.commit("refactor!: breaking change")
        self.assertEqual(self.calculate()[0], "v0.4.0")

    def test_empty_commits_and_unpublished_tags(self):
        self.commit("build(release): preparation", empty=True)
        with self.assertRaisesRegex(versioning.ReleaseError, "no substantive changes"):
            self.calculate()
        self.commit("fix: correction")
        self.git("tag", "v99.0.0")
        self.assertEqual(self.calculate()[0], "v0.3.1")

    def test_previous_published_timestamp_not_highest_tag(self):
        self.git("tag", "v0.2.9")
        self.commit("fix: patch")
        newer = {"id": 2, "tag_name": "v0.2.9", "draft": False,
                 "prerelease": False, "published_at": "2026-10-02T00:00:00Z"}
        self.assertEqual(self.calculate([newer])[:2], ("v0.2.10", "v0.2.9"))

    def test_changed_version_file_counts_as_substantive(self):
        (self.root / "action").mkdir()
        (self.root / "action/cli-version").write_text("v0.4.0\n")
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", "build(release): prepare next version")
        self.assertEqual(self.calculate()[0], "v0.3.1")
        self.commit("fix: actual change")
        self.assertEqual(self.calculate()[0], "v0.3.1")

    def test_retry_with_matching_marker(self):
        sha = self.commit("feat: new feature")
        self.git("tag", "v0.4.0")
        manifest = {"version": "v0.4.0", "commit": sha, "macos_sha256": "a" * 64,
                    "linux_sha256": "b" * 64, "checksums_sha256": "c" * 64}
        released = {"id": 2, "tag_name": "v0.4.0", "draft": False,
                    "prerelease": False, "published_at": "2026-10-02T00:00:00Z",
                    "body": "<!-- gh-settings-release: " + json.dumps(manifest) + " -->\nNotes"}
        self.assertEqual(self.calculate([released], True), ("v0.4.0", "", ""))
        with self.assertRaisesRegex(versioning.ReleaseError, "no substantive changes"):
            self.calculate([released])

    def test_invalid_baseline_and_empty_interval(self):
        with self.assertRaisesRegex(versioning.ReleaseError, "no substantive changes"):
            self.calculate()
        self.commit("fix: a change")
        (self.root / ".git/shallow").write_text(self.base + "\n")
        with self.assertRaisesRegex(versioning.ReleaseError, "complete Git history"):
            self.calculate()


class ParsingTests(unittest.TestCase):
    def test_fences_and_trailer_positions(self):
        for message, breaking, minor in [
            ("docs: mention\nBREAKING CHANGE: text", False, False),
            ("docs: mention\n\n```\nBREAKING CHANGE: fake\n```", False, False),
            ("fix: change\n\nBREAKING-CHANGE: migrate", True, False),
            ("fix: change\n\nRefs #123\nDEPRECATED: leave", False, True),
            ("docs: example\n\n~~~\nDEPRECATED: fake\n~~~", False, False),
        ]:
            with self.subTest(message=message):
                result = versioning.classify(message)
                self.assertEqual(result[2:], (breaking, minor))

    def test_semver_overflow_and_major(self):
        self.assertEqual(versioning.next_version("v1.2.3", 2), "v2.0.0")
        self.assertEqual(versioning.next_version("v0.2.3", 2), "v0.3.0")
        with self.assertRaises(versioning.ReleaseError):
            versioning.next_version("v1.2.18446744073709551615", 0)

    def test_catalog_rejects_incomplete_data(self):
        for release in [{"id": 1, "tag_name": "v0.1.0"},
                        {"id": 1, "tag_name": "v0.1.0", "draft": False,
                         "prerelease": False, "published_at": None}]:
            with self.subTest(release=release):
                with mock.patch.object(versioning, "command", return_value=json.dumps([[release]]).encode()):
                    with self.assertRaisesRegex(versioning.ReleaseError, "incomplete"):
                        versioning.read_releases("owner/repo")


if __name__ == "__main__":
    unittest.main()
