"""Offline release-note and publication contracts using actual Git histories."""

import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("release-notes.py").resolve()
PUBLISH_ACTION = SCRIPT.parent.parent / "actions/release-publish/action.yml"
REPOSITORY = "example/gh-qw"
HEADINGS = (
    "💥 BREAKING CHANGE",
    "⚠️ DEPRECATED",
    "✨ New Feature",
    "🐛 Bug Fix",
    "⚡ Performance",
    "🛠️ Maintenance",
)


class HistoryFixture(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="release-notes-test ")
        self.addCleanup(temporary.cleanup)
        self.root = pathlib.Path(temporary.name)
        self.repo = self.root / "repository"
        self.repo.mkdir()
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith("GIT_")}
        self.env.update(
            GIT_CONFIG_GLOBAL=os.devnull,
            GIT_CONFIG_NOSYSTEM="1",
            GIT_TERMINAL_PROMPT="0",
        )
        self.git("init", "--quiet", "--initial-branch=main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "core.hooksPath", os.devnull)
        self.sequence = 0
        self.base = self.commit("chore: recorded baseline")

    def git(self, *arguments, cwd=None, input=None):
        return subprocess.run(
            ["git", *arguments], cwd=cwd or self.repo, env=self.env,
            input=input, text=True, capture_output=True, check=True,
        ).stdout.strip()

    def commit(self, message, empty=False):
        self.sequence += 1
        if not empty:
            path = self.repo / f"change-{self.sequence}.txt"
            path.write_text(f"change {self.sequence}\n", encoding="utf-8")
            self.git("add", path.name)
        self.git("commit", "--quiet", "--allow-empty", "--cleanup=verbatim",
                 "--file=-", input=message + "\n")
        return self.git("rev-parse", "HEAD")

    def generate(self, previous=None, source=None, cwd=None, overrides=None):
        env = {
            **self.env,
            "PREVIOUS_SHA": previous if previous is not None else self.base,
            "SOURCE_SHA": source if source is not None else self.git("rev-parse", "HEAD"),
            "GITHUB_REPOSITORY": REPOSITORY,
        }
        for key, value in (overrides or {}).items():
            if value is None:
                env.pop(key, None)
            else:
                env[key] = value
        return subprocess.run(
            [sys.executable, str(SCRIPT)], cwd=cwd or self.repo, env=env,
            text=True, capture_output=True,
        )

    def successful_notes(self, **arguments):
        result = self.generate(**arguments)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(result.stdout.startswith("## What's Changed\n"), result.stdout)
        return result.stdout

    def assert_failed_before_output(self, result):
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertTrue(result.stderr.strip(), "failure must explain its cause")

    def assert_category(self, notes, sha, expected):
        sections = {heading: [] for heading in HEADINGS}
        current = None
        for line in notes.splitlines():
            if line.startswith("### "):
                current = line[4:] if line[4:] in sections else None
            elif line.startswith("## "):
                current = None
            elif current is not None:
                sections[current].append(line)
        link = f"https://github.com/{REPOSITORY}/commit/{sha}"
        occurrences = [heading for heading, lines in sections.items()
                       for line in lines if link in line]
        self.assertEqual(occurrences, [expected], notes)

    def gh_stub(self):
        directory = self.root / "bin"
        directory.mkdir(exist_ok=True)
        log = self.root / "gh-calls.jsonl"
        stub = directory / "gh"
        stub.write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, pathlib, sys\n"
            "args = sys.argv[1:]\n"
            "entry = {'args': args}\n"
            "if '--notes-file' in args:\n"
            "    path = pathlib.Path(args[args.index('--notes-file') + 1])\n"
            "    entry['notes'] = path.read_text(encoding='utf-8')\n"
            "with pathlib.Path(os.environ['GH_STUB_LOG']).open('a', encoding='utf-8') as output:\n"
            "    output.write(json.dumps(entry) + '\\n')\n",
            encoding="utf-8",
        )
        stub.chmod(0o755)
        return {
            "PATH": str(directory) + os.pathsep + self.env["PATH"],
            "GH_STUB_LOG": str(log),
        }, log


class GenerationTests(HistoryFixture):
    def test_category_priority_and_legacy_maintenance(self):
        cases = [
            ("feat!: replace layout\n\nDEPRECATED: old layout", HEADINGS[0]),
            ("feat: replace interface\n\nDEPRECATED: old interface\n"
             "BREAKING CHANGE: use the new interface", HEADINGS[0]),
            ("fix: replace protocol\n\nBREAKING-CHANGE: update clients", HEADINGS[0]),
            ("feat: retire old behavior\n\nDEPRECATED: use another command", HEADINGS[1]),
            ("fix: retire old option\n\nDEPRECATED: use another option", HEADINGS[1]),
            ("perf: retire old lookup\n\nDEPRECATED: use direct lookup", HEADINGS[1]),
            ("feat(worktree): select an existing directory", HEADINGS[2]),
            ("fix(remove): preserve an unselected directory", HEADINGS[3]),
            ("perf(registry): reduce repeated reads", HEADINGS[4]),
            *[(f"{kind}: maintain the repository", HEADINGS[5])
              for kind in ("docs", "refactor", "style", "test", "build", "ci",
                           "chore", "revert")],
            ("Previously merged maintenance change", HEADINGS[5]),
            ("feat(scope):missing required separator", HEADINGS[5]),
        ]
        commits = [(self.commit(message), heading) for message, heading in cases]
        notes = self.successful_notes()
        self.assertEqual(re.findall(r"^### (.+)$", notes, re.MULTILINE), list(HEADINGS))
        for sha, expected in commits:
            with self.subTest(sha=sha):
                self.assert_category(notes, sha, expected)

    def test_compatibility_words_in_prose_and_examples_are_not_footers(self):
        bodies = [
            "docs: no blank boundary\nBREAKING CHANGE: example only",
            "docs: no deprecation boundary\nDEPRECATED: example only",
            "docs: prose\n\nAn example says BREAKING CHANGE: update usage.",
            "docs: heading\n\n## BREAKING CHANGE: example only",
            "docs: quotation\n\n> BREAKING CHANGE: example only\n> DEPRECATED: old",
            "docs: indentation\n\n    BREAKING CHANGE: example only",
            "docs: backtick fence\n\n```text\nBREAKING CHANGE: example only\n"
            "DEPRECATED: old\n```",
            "docs: tilde fence\n\n~~~text\nBREAKING-CHANGE: example only\n"
            "DEPRECATED: old\n~~~",
        ]
        commits = [self.commit(message) for message in bodies]
        notes = self.successful_notes()
        for sha in commits:
            self.assert_category(notes, sha, HEADINGS[5])
        self.assertEqual(re.findall(r"^### (.+)$", notes, re.MULTILINE), [HEADINGS[5]])
        self.assertNotIn("## ⚠️ Migration guidance", notes)

    def test_formal_footer_after_example_and_other_trailer_is_recognized(self):
        sha = self.commit(
            "docs: describe a removed command\n\n"
            "```text\nDEPRECATED: an example\n```\n\n"
            "Refs #123\nBREAKING-CHANGE: use the replacement command"
        )
        notes = self.successful_notes()
        self.assert_category(notes, sha, HEADINGS[0])
        self.assertIn("  > use the replacement command", notes)
        self.assertNotIn("an example", notes)

    def test_frozen_range_excludes_baseline_and_later_history(self):
        source = self.commit("feat: keep this frozen change")
        expected = self.successful_notes(source=source)
        later = self.commit("fix: exclude this later change")
        self.git("tag", "moving-reference", self.base)
        self.git("tag", "--force", "moving-reference", later)
        actual = self.successful_notes(source=source)
        self.assertEqual(actual, expected)
        self.assertNotIn("recorded baseline", actual)
        self.assertNotIn("exclude this later change", actual)
        self.assertIn(
            f"https://github.com/{REPOSITORY}/compare/{self.base}...{source}", actual
        )

    def test_only_substantive_first_parent_commits_become_entries(self):
        main = self.commit("feat: main change")
        self.git("checkout", "--quiet", "-b", "topic", self.base)
        side = self.commit("fix: side branch detail")
        self.git("checkout", "--quiet", "main")
        self.git("merge", "--quiet", "--no-ff", "topic", "-m",
                 "chore: integrate prepared work")
        merge = self.git("rev-parse", "HEAD")
        empty = self.commit("feat!: tree-identical change", empty=True)
        self.assertEqual(self.git("rev-parse", f"{merge}^{{tree}}"),
                         self.git("rev-parse", f"{empty}^{{tree}}"))
        notes = self.successful_notes(source=empty)
        self.assert_category(notes, main, HEADINGS[2])
        self.assert_category(notes, merge, HEADINGS[5])
        self.assertNotIn(f"/commit/{side}", notes)
        self.assertNotIn(f"/commit/{empty}", notes)
        self.assertNotIn("tree-identical change", notes)

    def test_escaped_subject_and_multiline_migration_keep_exact_commit_links(self):
        breaking = self.commit(
            "feat!: replace <old> [API] & `mode` (#12)\n\n"
            "BREAKING CHANGE: Migrate <old> to [new](destination).\n\n"
            "Preserve *all* data.\n\nRefs #12"
        )
        header_only = self.commit("refactor!: remove the old path")
        deprecated = self.commit(
            "feat: retire the old spelling\n\nDEPRECATED: supplemental detail"
        )
        notes = self.successful_notes()
        self.assert_category(notes, breaking, HEADINGS[0])
        self.assert_category(notes, header_only, HEADINGS[0])
        self.assert_category(notes, deprecated, HEADINGS[1])
        self.assertIn(
            "- [replace &lt;old&gt; \\[API\\] &amp; \\`mode\\` (\\#12)]"
            f"(https://github.com/{REPOSITORY}/commit/{breaking})", notes
        )
        self.assertIn("## ⚠️ Migration guidance", notes)
        self.assertIn("  > Migrate &lt;old&gt; to \\[new\\](destination).", notes)
        self.assertIn("  > Preserve \\*all\\* data.", notes)
        self.assertIn("No migration footer was recorded; review the linked change", notes)
        self.assertNotIn("Refs #12", notes)
        self.assertNotIn("supplemental detail", notes)
        self.assertNotIn("<old>", notes)

    def test_invalid_or_missing_sha_inputs_fail_without_partial_notes(self):
        source = self.commit("fix: valid change")
        for variable in ("PREVIOUS_SHA", "SOURCE_SHA"):
            for invalid in (None, "", "HEAD", source[:8], "g" * 40, "0" * 40):
                with self.subTest(variable=variable, invalid=invalid):
                    result = self.generate(overrides={variable: invalid})
                    self.assert_failed_before_output(result)

    def test_full_noncommit_object_ids_are_rejected(self):
        self.commit("fix: valid change")
        tree = self.git("rev-parse", "HEAD^{tree}")
        for variable in ("PREVIOUS_SHA", "SOURCE_SHA"):
            with self.subTest(variable=variable):
                self.assert_failed_before_output(
                    self.generate(overrides={variable: tree})
                )

    def test_unrelated_and_second_parent_baselines_are_rejected(self):
        source = self.commit("feat: main change")
        unrelated = self.git("commit-tree", f"{source}^{{tree}}", "-m",
                             "chore: unrelated root")
        self.assert_failed_before_output(self.generate(previous=unrelated))
        self.git("checkout", "--quiet", "-b", "topic", self.base)
        side = self.commit("fix: side change")
        self.git("checkout", "--quiet", "main")
        self.git("merge", "--quiet", "--no-ff", "topic", "-m", "chore: merge topic")
        self.git("merge-base", "--is-ancestor", side, "HEAD")
        self.assert_failed_before_output(self.generate(previous=side))

    def test_shallow_history_is_rejected_even_when_both_boundaries_exist(self):
        previous = self.commit("chore: later baseline")
        source = self.commit("fix: source change")
        shallow = self.root / "shallow"
        self.git("clone", "--quiet", "--depth=2", "--no-local",
                 self.repo.as_uri(), str(shallow))
        self.assertEqual(self.git("rev-parse", "--is-shallow-repository", cwd=shallow),
                         "true")
        self.git("cat-file", "-e", f"{previous}^{{commit}}", cwd=shallow)
        self.assert_failed_before_output(
            self.generate(previous=previous, source=source, cwd=shallow)
        )

    def test_empty_interval_and_tree_identical_history_are_rejected(self):
        self.assert_failed_before_output(self.generate(source=self.base))
        empty = self.commit("feat: metadata-only commit", empty=True)
        self.assert_failed_before_output(self.generate(source=empty))

    def test_generator_does_not_call_gh(self):
        self.commit("fix: local history is sufficient")
        overrides, log = self.gh_stub()
        self.successful_notes(overrides=overrides)
        self.assertFalse(log.exists(), "generation unexpectedly invoked gh")


class PublicationTests(HistoryFixture):
    def setUp(self):
        super().setUp()
        self.source = self.commit("feat: accepted frozen change")
        destination = self.repo / ".github/scripts/release-notes.py"
        destination.parent.mkdir(parents=True)
        shutil.copyfile(SCRIPT, destination)
        self.runner = self.root / "runner temp"
        self.artifacts = self.runner / "gh-qw-release-artifacts"
        self.artifacts.mkdir(parents=True)
        self.assets = [
            str(self.artifacts / name) for name in
            ("gh-qw-darwin-arm64", "gh-qw-linux-amd64", "gh-qw-windows-amd64.exe")
        ]
        for asset in self.assets:
            pathlib.Path(asset).write_text("accepted fixture binary\n", encoding="utf-8")
        self.stub_env, self.log = self.gh_stub()

    def run_publish_step(self, previous=None):
        # Read the actual simple composite step, including its env bindings,
        # without adding a YAML dependency or copying the publication commands.
        lines = PUBLISH_ACTION.read_text(encoding="utf-8").splitlines()
        start = lines.index("    - name: Publish exact accepted binaries") + 1
        end = next((index for index in range(start, len(lines))
                    if lines[index].startswith("    - ")), len(lines))
        step = lines[start:end]
        env_start = step.index("      env:") + 1
        run_start = step.index("      run: |") + 1
        values = {
            "runner.temp": str(self.runner),
            "inputs.source-sha": self.source,
            "inputs.version": "v0.9.0",
            "inputs.previous-tag": "v0.8.0",
            "inputs.previous-sha": previous if previous is not None else self.base,
        }
        inherited = {key: value for key, value in self.env.items()
                     if key not in ("ARTIFACT_DIR", "SOURCE_SHA", "VERSION",
                                    "PREVIOUS_TAG", "PREVIOUS_SHA")}
        env = {**inherited, **self.stub_env, "RUNNER_TEMP": str(self.runner),
               "GITHUB_REPOSITORY": REPOSITORY, "GH_TOKEN": "test-only"}
        for line in step[env_start:run_start - 1]:
            match = re.fullmatch(r"        ([A-Z_]+): (.+)", line)
            self.assertIsNotNone(match, f"unsupported publication env binding: {line}")
            env[match[1]] = re.sub(
                r"\$\{\{\s*([^{}]+?)\s*\}\}",
                lambda expression: values[expression[1]], match[2],
            )
        body = "\n".join(line[8:] if line.startswith("        ") else line
                         for line in step[run_start:]) + "\n"
        return subprocess.run(
            ["bash", "-e", "-o", "pipefail"], cwd=self.repo, env=env,
            input=body, text=True, capture_output=True,
        )

    def test_publisher_passes_generated_notes_with_exact_accepted_assets(self):
        expected = self.successful_notes(source=self.source)
        self.commit("fix: later work must not enter this release")
        notes_path = self.runner / "release-notes.md"
        notes_path.write_text("stale content\n", encoding="utf-8")
        result = self.run_publish_step()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(len(calls), 1, calls)
        arguments = calls[0]["args"]
        self.assertEqual(arguments[:3], ["release", "create", "v0.9.0"])
        for option, value in (
            ("--repo", REPOSITORY), ("--target", self.source), ("--title", "v0.9.0"),
            ("--notes-file", str(notes_path)),
        ):
            self.assertIn(option, arguments)
            self.assertEqual(arguments[arguments.index(option) + 1], value)
        self.assertEqual(arguments[-3:], self.assets)
        self.assertNotIn("--generate-notes", arguments)
        self.assertNotIn("--notes-start-tag", arguments)
        self.assertEqual(calls[0]["notes"], expected)
        self.assertEqual(notes_path.read_text(encoding="utf-8"), expected)
        self.assertNotEqual(notes_path.parent, self.artifacts)

    def test_generation_failure_prevents_any_publication(self):
        result = self.run_publish_step(previous="not-a-full-sha")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(result.stderr.strip())
        self.assertFalse(self.log.exists(), "gh was invoked after generation failed")


if __name__ == "__main__":
    unittest.main()
