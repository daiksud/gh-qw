#!/usr/bin/env python3
"""Generate release notes from committed messages in a frozen Git interval."""

import os
import re
import subprocess
import sys
from dataclasses import dataclass


SHA = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
HEADER = re.compile(r"^([A-Za-z][A-Za-z0-9-]*)(?:\([^()]+\))?(!)?: (.+)$")
FOOTER = re.compile(r"^([A-Za-z][A-Za-z0-9-]*|BREAKING CHANGE)(: | #)(.*)$")
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
HEADINGS = (
    "💥 BREAKING CHANGE",
    "⚠️ DEPRECATED",
    "✨ New Feature",
    "🐛 Bug Fix",
    "⚡ Performance",
    "🛠️ Maintenance",
)
MARKDOWN_ESCAPES = {
    "\\": "\\\\", "&": "&amp;", "<": "&lt;", ">": "&gt;",
    **{character: "\\" + character for character in "`*_[]#~|"},
}


class ReleaseError(Exception):
    """Invalid input or incomplete release history."""


@dataclass(frozen=True)
class Change:
    sha: str
    subject: str
    kind: str
    major: bool
    deprecated: bool
    breaking: tuple[str, ...]

    def category(self):
        if self.major:
            return 0
        if self.deprecated:
            return 1
        return {"feat": 2, "fix": 3, "perf": 4}.get(self.kind, 5)


def git(*args):
    # Read committed objects without fetching or using local replacements.
    environment = dict(os.environ, GIT_NO_LAZY_FETCH="1")
    result = subprocess.run(
        ["git", "--no-pager", "--no-replace-objects", "-c", "log.showSignature=false", *args],
        capture_output=True, check=False, env=environment,
    )
    if result.returncode:
        detail = result.stderr.decode("utf-8", "replace").strip()
        raise ReleaseError(f"git {args[0]} failed: {detail or result.returncode}")
    return result.stdout.decode("utf-8")


def footers(lines):
    """Read formal compatibility trailers, including multiline migration text."""
    breaking = []
    value = []
    in_footers = collecting = blank = deprecated = False
    fence = ""
    for line in lines:
        match = FENCE.match(line)
        if fence:
            if (match and match.group(1)[0] == fence[0] and
                    len(match.group(1)) >= len(fence) and not line[match.end():].strip()):
                fence = ""
        elif match:
            fence = match.group(1)
        else:
            footer = FOOTER.fullmatch(line)
            if footer and (blank or in_footers):
                if collecting:
                    breaking.append("\n".join(value).strip())
                value = []
                in_footers = True
                collecting = footer.group(2) == ": " and footer.group(1) in (
                    "BREAKING CHANGE", "BREAKING-CHANGE",
                )
                if collecting:
                    value.append(footer.group(3))
                if footer.group(1) == "DEPRECATED" and footer.group(2) == ": ":
                    deprecated = True
                blank = False
                continue
        if collecting:
            value.append(line)
        blank = not line.strip()
    if collecting:
        breaking.append("\n".join(value).strip())
    return tuple(breaking), deprecated


def parse_change(sha, message):
    lines = message.replace("\r\n", "\n").split("\n")
    header = HEADER.fullmatch(lines[0])
    kind = header.group(1).lower() if header else ""
    subject = header.group(3) if header else lines[0]
    breaking, deprecated = footers(lines[1:])
    major = bool((header and header.group(2)) or breaking)
    return Change(sha, subject, kind, major, deprecated, breaking)


def history(previous, source):
    for name, sha in (("PREVIOUS_SHA", previous), ("SOURCE_SHA", source)):
        if not SHA.fullmatch(sha):
            raise ReleaseError(f"{name} must be a full commit SHA")
    if git("rev-parse", "--is-shallow-repository").strip() != "false":
        raise ReleaseError("complete Git history is required")
    for name, sha in (("PREVIOUS_SHA", previous), ("SOURCE_SHA", source)):
        if git("rev-parse", "--verify", f"{sha}^{{commit}}").strip() != sha:
            raise ReleaseError(f"{name} must identify a commit directly")
    try:
        git("merge-base", "--is-ancestor", previous, source)
    except ReleaseError as error:
        raise ReleaseError(f"previous release is not an ancestor of source: {error}") from error

    log = git(
        "log", "--first-parent", "--reverse", "--topo-order", "--no-decorate",
        "--no-notes", "--no-color", "--encoding=UTF-8", "-z", "--format=%H%x00%P%x00%B",
        f"{previous}..{source}", "--",
    )
    if not log:
        raise ReleaseError("no substantive changes since previous release")
    fields = log.removesuffix("\x00").split("\x00")
    if not log.endswith("\x00") or len(fields) % 3:
        raise ReleaseError("invalid Git log record")

    changes = []
    first_parent = previous
    for index in range(0, len(fields), 3):
        sha, parent_text, message = fields[index:index + 3]
        parents = parent_text.split()
        if not SHA.fullmatch(sha) or any(not SHA.fullmatch(parent) for parent in parents):
            raise ReleaseError("invalid commit in Git log")
        if not parents or parents[0] != first_parent:
            raise ReleaseError("previous release is not on source's first-parent history")
        first_parent = sha
        changed_paths = git(
            "diff-tree", "--no-commit-id", "--name-only", "-r", "--no-ext-diff",
            "--no-renames", "--ignore-submodules=none", parents[0], sha, "--",
        )
        if changed_paths:
            changes.append(parse_change(sha, message))
    if first_parent != source:
        raise ReleaseError("incomplete first-parent history")
    if not changes:
        raise ReleaseError("no substantive changes since previous release")
    return changes


def markdown_text(text):
    return "".join(MARKDOWN_ESCAPES.get(character, character) for character in text)


def generate(repository, previous, source):
    if not REPOSITORY.fullmatch(repository):
        raise ReleaseError("invalid repository identity")
    changes = history(previous, source)
    groups = [[] for _ in HEADINGS]
    for change in changes:
        groups[change.category()].append(change)

    def entry(change):
        return f"- [{markdown_text(change.subject)}](https://github.com/{repository}/commit/{change.sha})\n"

    notes = ["## What's Changed\n"]
    for heading, group in zip(HEADINGS, groups):
        if group:
            notes.append(f"\n### {heading}\n\n")
            notes.extend(entry(change) for change in group)

    breaking_changes = [change for change in changes if change.major]
    if breaking_changes:
        notes.append("\n## ⚠️ Migration guidance\n\n")
        for change in breaking_changes:
            notes.append(entry(change))
            if not change.breaking:
                notes.append("\n  > No migration footer was recorded; review the linked change before upgrading.\n")
            for detail in change.breaking:
                notes.append("\n")
                notes.extend(f"  > {markdown_text(line)}\n" for line in detail.split("\n"))
    notes.append(
        f"\n**Frozen comparison**: [{previous} → {source}]"
        f"(https://github.com/{repository}/compare/{previous}...{source})\n"
    )
    return "".join(notes)


def main():
    try:
        if sys.argv[1:]:
            raise ReleaseError("usage: release-notes.py (PREVIOUS_SHA, SOURCE_SHA, GITHUB_REPOSITORY required)")
        notes = generate(
            os.environ.get("GITHUB_REPOSITORY", ""),
            os.environ.get("PREVIOUS_SHA", ""),
            os.environ.get("SOURCE_SHA", ""),
        )
        sys.stdout.buffer.write(notes.encode("utf-8"))
    except (ReleaseError, OSError, UnicodeError) as error:
        print(f"release-notes: {error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
