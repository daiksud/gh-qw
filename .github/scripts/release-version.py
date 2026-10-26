#!/usr/bin/env python3
"""Select a release version from a frozen Git commit and published GitHub releases.

This file is identical in gh-settings and gh-qw. Repository-specific release
matrices, artifacts, publication, and verification remain in their own workflows.
"""

import json
import os
import re
import subprocess
import sys
from datetime import datetime

VERSION = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
SHA = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
HEADER = re.compile(r"^([A-Za-z][A-Za-z0-9-]*)(?:\(([^()]+)\))?(!)?: .+$")
FOOTER = re.compile(r"^([A-Za-z][A-Za-z0-9-]*|BREAKING CHANGE)(: | #)(.*)$")
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
HASH = re.compile(r"^[0-9a-f]{64}$")
MAX_COMPONENT = (1 << 64) - 1


class ReleaseError(Exception):
    """Invalid history, published metadata, or release candidate."""


def command(*args):
    result = subprocess.run(args, capture_output=True, check=False)
    if result.returncode:
        raise ReleaseError(f"{args[0]} {args[1]} failed: {result.stderr.decode('utf-8', 'replace').strip()}")
    return result.stdout


def git(*args):
    return command("git", "-c", "log.showSignature=false", *args).decode("utf-8")


def read_releases(repository):
    response = command("gh", "api", f"repos/{repository}/releases?per_page=100", "--paginate", "--slurp")
    try:
        pages = json.loads(response)
    except (ValueError, UnicodeError) as exc:
        raise ReleaseError("invalid GitHub release catalog") from exc
    if not isinstance(pages, list) or not pages or any(not isinstance(page, list) for page in pages):
        raise ReleaseError("missing or invalid GitHub release catalog")
    releases = []
    seen = set()
    for page in pages:
        for release in page:
            if not isinstance(release, dict):
                raise ReleaseError("invalid release record")
            rid = release.get("id")
            tag = release.get("tag_name")
            draft = release.get("draft")
            prerelease = release.get("prerelease")
            published = release.get("published_at")
            if (type(rid) is not int or rid <= 0 or rid in seen or
                    not isinstance(tag, str) or not tag or
                    type(draft) is not bool or type(prerelease) is not bool or
                    (not draft and not isinstance(published, str))):
                raise ReleaseError("incomplete or duplicate GitHub release metadata")
            seen.add(rid)
            if not draft:
                try:
                    datetime.fromisoformat(published.replace("Z", "+00:00"))
                except ValueError as exc:
                    raise ReleaseError("invalid publication timestamp") from exc
            releases.append(release)
    return releases


def resolve_tag(tag):
    return git("rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}").strip()


def existing_retry(source, releases):
    """Match an immutable gh-settings release, leaving asset checks to its publisher."""
    matches = []
    for release in releases:
        tag = release["tag_name"]
        if not VERSION.fullmatch(tag) or release["prerelease"]:
            continue
        body = release.get("body")
        if not isinstance(body, str) or not body.startswith("<!-- gh-settings-release: "):
            continue
        marker = body.split("\n", 1)[0]
        if not marker.endswith(" -->"):
            continue
        try:
            identity = json.loads(marker[len("<!-- gh-settings-release: "):-len(" -->")])
        except ValueError:
            continue
        if not isinstance(identity, dict) or identity.get("version") != tag or identity.get("commit") != source:
            continue
        if not all(isinstance(identity.get(key), str) and HASH.fullmatch(identity[key]) for key in
                   ("macos_sha256", "linux_sha256", "checksums_sha256")):
            continue
        if resolve_tag(tag) != source:
            raise ReleaseError("existing release tag does not match its source")
        matches.append(tag)
    if len(matches) > 1:
        raise ReleaseError("multiple existing releases identify the same source")
    return matches[0] if matches else None


def classify(message):
    lines = message.replace("\r\n", "\n").split("\n")
    header = HEADER.fullmatch(lines[0])
    kind = header.group(1).lower() if header else ""
    scope = header.group(2) if header else ""
    breaking = bool(header and header.group(3))
    minor = kind in ("feat", "perf")
    # A trailer must start after a blank line or another trailer, not inside
    # prose or fenced examples. This mirrors the reviewed gh-settings parser.
    in_trailers = False
    blank = False
    fence = ""
    for line in lines[1:]:
        match = FENCE.match(line)
        if fence:
            if (match and match.group(1)[0] == fence[0] and
                    len(match.group(1)) >= len(fence) and not line[match.end():].strip()):
                fence = ""
        elif match:
            fence = match.group(1)
        else:
            footer = FOOTER.fullmatch(line)
            if footer and (blank or in_trailers):
                in_trailers = True
                if footer.group(2) == ": ":
                    breaking |= footer.group(1) in ("BREAKING CHANGE", "BREAKING-CHANGE")
                    minor |= footer.group(1) == "DEPRECATED"
                blank = False
                continue
        blank = not line.strip()
    return kind, scope, breaking, minor


def next_version(version, tier):
    if not VERSION.fullmatch(version):
        raise ReleaseError("invalid published stable version")
    numbers = [int(number) for number in version[1:].split(".")]
    if any(number > MAX_COMPONENT for number in numbers):
        raise ReleaseError("version component exceeds supported range")
    if tier == 2 and numbers[0] == 0:
        tier = 1  # v1.0.0 requires a deliberate project decision.
    index = 2 - tier
    if numbers[index] == MAX_COMPONENT:
        raise ReleaseError("version component would overflow")
    numbers[index] += 1
    numbers[index + 1:] = [0] * (2 - index)
    return "v" + ".".join(map(str, numbers))


def calculate(source, releases, allow_retry=False):
    if not SHA.fullmatch(source):
        raise ReleaseError("source must be an immutable full commit SHA")
    if git("rev-parse", "--is-shallow-repository").strip() != "false":
        raise ReleaseError("complete Git history is required")
    if git("rev-parse", "--verify", f"{source}^{{commit}}").strip() != source:
        raise ReleaseError("source must identify a commit directly")
    if allow_retry:
        version = existing_retry(source, releases)
        if version:
            return version, "", ""

    candidates = [release for release in releases if
                  not release["draft"] and not release["prerelease"] and
                  VERSION.fullmatch(release["tag_name"])]
    if not candidates:
        raise ReleaseError("initial release requires a separately chosen baseline")
    latest = max(candidates, key=lambda release: (release["published_at"], release["id"]))
    previous_tag = latest["tag_name"]
    previous_sha = resolve_tag(previous_tag)
    if not SHA.fullmatch(previous_sha):
        raise ReleaseError("previous release does not resolve to a commit")
    if subprocess.run(["git", "merge-base", "--is-ancestor", previous_sha, source], check=False).returncode:
        raise ReleaseError("previous published release is not an ancestor of source")

    logs = command("git", "-c", "log.showSignature=false", "log", "--reverse", "--topo-order", "--no-decorate",
                   "--no-notes", "--no-color", "--encoding=UTF-8", "-z", "--format=%H%x00%B",
                   f"{previous_sha}..{source}", "--")
    if not logs:
        raise ReleaseError("no substantive changes since previous release")
    fields = logs.removesuffix(b"\x00").split(b"\x00")
    if len(fields) % 2:
        raise ReleaseError("invalid Git log record")
    tier = -1
    for i in range(0, len(fields), 2):
        sha = fields[i].decode("ascii")
        if not SHA.fullmatch(sha):
            raise ReleaseError("invalid commit in Git log")
        message = fields[i + 1].decode("utf-8")
        kind, scope, breaking, minor = classify(message)
        # Ignore empty commits and legacy version-only preparation commits.
        # Any other tree change, including a preparation-named commit, counts.
        parents = git("rev-list", "--parents", "-n", "1", sha).split()
        if len(parents) == 2 and subprocess.run(["git", "diff-tree", "--quiet", parents[1], sha], check=False).returncode == 0:
            continue
        if kind in ("build", "chore") and scope == "release" and not breaking and not minor:
            paths = command("git", "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "-z", sha, "--")
            if paths == b"action/cli-version\x00":
                continue
        tier = max(tier, 2 if breaking else 1 if minor else 0)
    if tier < 0:
        raise ReleaseError("no substantive changes since previous release")
    return next_version(previous_tag, tier), previous_tag, previous_sha


def main():
    try:
        allow_retry = sys.argv[1:] == ["--allow-retry"]
        if sys.argv[1:] and not allow_retry:
            raise ReleaseError("usage: release-version.py [--allow-retry]")
        repository = os.environ.get("GITHUB_REPOSITORY", "")
        source = os.environ.get("SOURCE_SHA", "")
        if not REPOSITORY.fullmatch(repository):
            raise ReleaseError("invalid repository identity")
        version, previous_tag, previous_sha = calculate(source, read_releases(repository), allow_retry)
        print(f"version={version}\nprevious-tag={previous_tag}\nprevious-sha={previous_sha}")
    except (ReleaseError, UnicodeError, ValueError, OverflowError) as error:
        print(f"release-version: {error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
