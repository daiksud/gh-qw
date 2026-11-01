"""Execute the actual inline script with isolated GitHub API responses."""

import contextlib
import copy
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest
from unittest.mock import patch

WORKFLOW = Path(os.environ.get("APPROVAL_WORKFLOW_FIXTURE", Path(__file__).resolve().parents[1] / ".github/workflows/approve-codex-review.yml"))
ROOT = "repos/owner/repo"
BOT = "chatgpt-codex-connector[bot]"
PREFIX = "<!-- codex-pull-request-review-summary -->"
HEAD = "a" * 40
STATE = "<!-- codex-review-state:v1 "


def summary(code="Completed", security="Completed", identifier=10, code_sha=HEAD, security_sha=HEAD):
    # Observed Codex table: emoji/bold labels, status, time, commit, trigger.
    body = (PREFIX + "\n## Codex Review Summary\n\n"
            "| Review | Status | Commit | Review trigger |\n"
            "| --- | --- | --- | --- |\n"
            f'| 📝 **Code Review** | ✅ **{code}** <relative-time datetime="not-a-clock">done</relative-time> | `aaaaaaa` | Manual request |\n'
            f'| 🔒 **Security Review** | ✅ **{security}** <relative-time datetime="not-a-clock">done</relative-time> | `aaaaaaa` | Manual request |\n')
    state = {"code_review": {"status": "completed", "commit_sha": code_sha},
             "security_review": {"status": "completed", "commit_sha": security_sha}}
    body += STATE + json.dumps(state) + " -->\n"
    return {"id": identifier, "body": body, "user": {"login": BOT}}


def fixture():
    return {"pr": {"head": {"sha": HEAD}}, "comments": [summary()], "reactions": [{"user": {"login": BOT, "type": "User"}, "content": "+1"}]}


def execute(data, mutate=None, failure=None, pagination=None, response=None, summary_file=True, expected_head=HEAD):
    source = textwrap.dedent(WORKFLOW.read_text().split("python3 - <<'PYTHON'\n", 1)[1].rsplit("\n          PYTHON", 1)[0])
    data = copy.deepcopy(data)
    writes, reads, sleeps = [], {}, []
    stdout, stderr = io.StringIO(), io.StringIO()

    def process(args, **kwargs):
        if args == ["sleep", "30"]:
            sleeps.append(30)
            return subprocess.CompletedProcess(args, 0)
        if args[:2] != ["gh", "api"]:
            raise AssertionError("Unexpected external command")
        if args[2] == "--method":
            # Only the verified full SHA is submitted, never an implicit/latest HEAD.
            if args != ["gh", "api", "--method", "POST", f"{ROOT}/pulls/7/reviews", "-f", "event=APPROVE", "-f", "commit_id=" + expected_head]:
                raise AssertionError("Unexpected write")
            if mutate:
                mutate("POST", 1, data)
            if failure == "post":
                return subprocess.CompletedProcess(args, 1, "secret API body", "secret stderr")
            writes.append(args)
            return subprocess.CompletedProcess(args, 0, json.dumps({"state": "APPROVED", "commit_id": expected_head} if response is None else response))
        endpoint = args[2].split("?", 1)[0]
        reads[endpoint] = reads.get(endpoint, 0) + 1
        if mutate:
            mutate(endpoint, reads[endpoint], data)
        if failure == endpoint:
            return subprocess.CompletedProcess(args, 1, "secret API body", "secret stderr")
        if failure == "json":
            return subprocess.CompletedProcess(args, 0, "secret non-JSON response")
        if endpoint == f"{ROOT}/pulls/7":
            if args[3:]:
                raise AssertionError("PR read must have no extra query options")
            return subprocess.CompletedProcess(args, 0, json.dumps(data["pr"]))
        if args[3:] != ["--paginate", "--slurp"]:
            raise AssertionError("Collection must be paginated")
        values = {f"{ROOT}/issues/7/comments": data["comments"], f"{ROOT}/issues/7/reactions": data["reactions"]}
        # Any base, receipt, timeline, commit-resolution or review-history read fails here.
        if endpoint not in values:
            raise AssertionError("Unexpected extra eligibility read")
        rows = values[endpoint]
        result = [rows[:1], rows[1:]] if pagination is None else pagination
        return subprocess.CompletedProcess(args, 0, json.dumps(result))

    with tempfile.TemporaryDirectory() as folder:
        path = Path(folder) / "summary.md"
        env = {"GITHUB_REPOSITORY": "owner/repo", "PR_NUMBER": "7", "GH_TOKEN": "dummy-test-token", "COMMENT_ID": "superseded-event"}
        if summary_file:
            env["GITHUB_STEP_SUMMARY"] = str(path)
        with patch.dict(os.environ, env, clear=True), patch("subprocess.run", side_effect=process), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = 0
            try:
                exec(compile(source, str(WORKFLOW), "exec"), {"__name__": "__main__"})
            except SystemExit as error:
                code = error.code
        note = path.read_text() if path.exists() else ""
    return {"code": code, "writes": writes, "reads": reads, "sleeps": sleeps, "output": stdout.getvalue() + stderr.getvalue(), "summary": note}


class ApprovalWorkflowTests(unittest.TestCase):
    def approved(self, result):
        self.assertEqual(result["code"], 0, result)
        self.assertEqual(len(result["writes"]), 1, result)
        self.assertIn("Codex approval: Approved", result["output"])

    def withheld(self, result):
        self.assertEqual(result["code"], 0, result)
        self.assertEqual(result["writes"], [], result)
        self.assertIn("Codex approval: Withheld", result["output"])

    def test_thumbs_up_completed_and_matching_full_shas_approve(self):
        self.approved(execute(fixture()))

    def test_each_review_must_be_completed(self):
        for label in ("code", "security"):
            for status in ("Running", "Failed", "Queued", "Incomplete", ""):
                with self.subTest(label=label, status=status):
                    data = fixture()
                    data["comments"] = [summary(**{label: status})]
                    self.withheld(execute(data))

    def test_missing_summary_or_review_row_withholds(self):
        for mode in ("summary", "code", "security"):
            with self.subTest(mode=mode):
                data = fixture()
                if mode == "summary":
                    data["comments"] = []
                else:
                    label = "Code Review" if mode == "code" else "Security Review"
                    data["comments"][0]["body"] = "\n".join(line for line in data["comments"][0]["body"].splitlines() if f"**{label}**" not in line)
                self.withheld(execute(data))

    def test_only_codex_summary_and_reaction_count(self):
        for target in ("comments", "reactions"):
            with self.subTest(target=target):
                data = fixture()
                data[target][0]["user"]["login"] = "other-user"
                self.withheld(execute(data))

    def test_setup_notice_cannot_replace_summary(self):
        data = fixture()
        data["comments"][0]["body"] = "To use Codex here, create an environment."
        self.withheld(execute(data))

    def test_missing_or_other_reaction_withholds_after_bounded_wait(self):
        for content in (None, "eyes", "heart"):
            with self.subTest(content=content):
                data = fixture()
                data["reactions"] = [] if content is None else [{"user": {"login": BOT}, "content": content}]
                result = execute(data)
                self.withheld(result)
                self.assertEqual(result["sleeps"], [30] * 6)

    def test_late_thumbs_up_rechecks_reviewed_head(self):
        def change(endpoint, count, data):
            if endpoint.endswith("/reactions") and count == 1:
                data["reactions"] = []
            elif endpoint.endswith("/reactions"):
                data["reactions"] = fixture()["reactions"]
        result = execute(fixture(), mutate=change)
        self.approved(result)
        self.assertEqual(result["sleeps"], [30])

    def test_incomplete_review_during_wait_withholds(self):
        data = fixture()
        data["reactions"] = []
        def change(endpoint, count, data):
            if endpoint.endswith("/comments") and count == 2:
                data["comments"] = [summary(security="Running")]
        self.withheld(execute(data, mutate=change))

    def test_latest_codex_summary_is_used_across_pages(self):
        for latest, expected in [(summary(identifier=20), True), (summary(security="Running", identifier=20), False)]:
            with self.subTest(expected=expected):
                data = fixture()
                data["comments"] = [latest, summary(identifier=5)]
                (self.approved if expected else self.withheld)(execute(data))

    def test_non_codex_newer_summary_is_ignored(self):
        data = fixture()
        other = summary(security="Running", identifier=30)
        other["user"]["login"] = "other-user"
        data["comments"].append(other)
        self.approved(execute(data))

    def test_completed_elsewhere_does_not_replace_status(self):
        data = fixture()
        data["comments"] = [summary(code="Running")]
        data["comments"][0]["body"] += "\nCode Review is **Completed** in another paragraph.\n"
        self.withheld(execute(data))

    def test_ambiguous_duplicate_review_row_withholds(self):
        data = fixture()
        data["comments"][0]["body"] += "| **Code Review** | **Running** | other |\n"
        self.withheld(execute(data))

    def test_findings_timestamps_base_requests_and_receipts_are_not_gates(self):
        data = fixture()
        item = data["comments"][0]
        item.update(created_at="invalid", updated_at="older", performed_via_github_app=None)
        item["body"] = item["body"].replace("`aaaaaaa`", "`another-commit`").replace("Manual request", "Automatic review")
        item["body"] = "\n".join(line for line in item["body"].splitlines() if not line.startswith(STATE))
        item["body"] += "\n" + STATE + json.dumps({
            "code_review": {"commit_sha": HEAD, "has_unresolved_findings": True},
            "security_review": {"commit_sha": HEAD, "has_any_findings": True}}) + " -->\n"
        item["body"] += "\n### Security findings\nAn accepted risk remains.\n"
        data["comments"] += [{"id": 20, "body": "@codex review", "user": {"login": "author"}}, {"id": 21, "body": "A receipt reports findings", "user": {"login": BOT}}]
        data["reactions"][0]["created_at"] = "old-or-invalid"
        data["reviews"] = [{"state": "CHANGES_REQUESTED"}, {"state": "APPROVED"}]
        data["pr"] = {"head": {"sha": HEAD}, "base": {"ref": "retargeted"}}
        self.approved(execute(data))

    def test_full_review_shas_are_required(self):
        for kind in ("code", "security"):
            for sha in (None, "", "a" * 7, "g" * 40, "a" * 39, "a" * 41, 42, [], " " + HEAD):
                with self.subTest(kind=kind, sha=sha):
                    data = fixture()
                    data["comments"] = [summary(**{kind + "_sha": sha})]
                    self.withheld(execute(data))

    def test_either_review_for_another_full_sha_withholds(self):
        # Even matching seven-character prefixes do not establish full-SHA equality.
        other = HEAD[:7] + "b" * 33
        for kind in ("code", "security"):
            with self.subTest(kind=kind):
                data = fixture()
                data["comments"] = [summary(**{kind + "_sha": other})]
                self.withheld(execute(data))

    def test_missing_malformed_or_ambiguous_state_withholds(self):
        for marker in (None, "malformed", "null", "[]", "{}",
                       '{"code_review":null,"security_review":{}}',
                       '{"code_review":{"commit_sha":"' + HEAD + '","commit_sha":"' + HEAD + '"},"security_review":{"commit_sha":"' + HEAD + '"}}'):
            with self.subTest(marker=marker):
                data = fixture()
                item = data["comments"][0]
                item["body"] = "\n".join(line for line in item["body"].splitlines() if not line.startswith(STATE))
                if marker is not None:
                    item["body"] += "\n" + STATE + marker + " -->\n"
                self.withheld(execute(data))
        data = fixture()
        data["comments"][0]["body"] += STATE + "{} -->\n"
        self.withheld(execute(data))

    def test_full_shas_are_case_insensitive(self):
        data = fixture()
        data["pr"]["head"]["sha"] = HEAD.upper()
        data["comments"] = [summary(code_sha=HEAD.upper(), security_sha=HEAD.upper())]
        self.approved(execute(data))

    def test_another_latest_head_with_matching_reviews_approves(self):
        other = HEAD[:7] + "b" * 33
        data = fixture()
        data["pr"]["head"]["sha"] = other
        data["comments"] = [summary(code_sha=other, security_sha=other)]
        self.approved(execute(data, expected_head=other))

    def test_missing_or_malformed_pr_head_fails_without_writing(self):
        for pr in (None, [], {}, {"head": []}, {"head": {}},
                   {"head": {"sha": None}}, {"head": {"sha": "a" * 7}},
                   {"head": {"sha": "not-a-commit"}}):
            with self.subTest(pr=pr):
                data = fixture()
                data["pr"] = pr
                result = execute(data)
                self.assertEqual(result["code"], 1, result)
                self.assertEqual(result["writes"], [])

    def test_head_change_before_submission_withholds(self):
        for when in ("summary", "reaction", "head_read"):
            with self.subTest(when=when):
                def change(endpoint, count, data):
                    if ((when == "summary" and endpoint.endswith("/comments") and count == 1)
                        or (when == "reaction" and endpoint.endswith("/reactions") and count == 1)
                        or (when == "head_read" and endpoint == f"{ROOT}/pulls/7" and count == 1)):
                        data["pr"]["head"]["sha"] = "b" * 40
                self.withheld(execute(fixture(), mutate=change))

    def test_head_change_during_reaction_wait_withholds(self):
        def change(endpoint, count, data):
            if endpoint.endswith("/reactions") and count == 1:
                data["reactions"] = []
            if endpoint.endswith("/comments") and count == 2:
                data["pr"]["head"]["sha"] = "b" * 40
                data["reactions"] = fixture()["reactions"]
        result = execute(fixture(), mutate=change)
        self.withheld(result)
        self.assertEqual(result["reads"][f"{ROOT}/pulls/7"], 1)

    def test_reviewed_sha_at_single_summary_read_must_match(self):
        def change(endpoint, count, data):
            if endpoint.endswith("/comments") and count == 1:
                data["comments"] = [summary(security_sha="b" * 40)]
        self.withheld(execute(fixture(), mutate=change))

    def test_push_after_final_read_cannot_retarget_the_post(self):
        data = fixture()
        def change(endpoint, count, data):
            if endpoint == "POST":
                data["pr"]["head"]["sha"] = "b" * 40
        result = execute(data, mutate=change)
        self.approved(result)
        self.assertEqual(result["writes"][0][-1], "commit_id=" + HEAD)
        self.assertIn(HEAD, result["output"])

    def test_extra_api_identity_metadata_is_not_required(self):
        data = fixture()
        data["comments"][0]["user"].update(id=0, type="User")
        data["comments"][0]["performed_via_github_app"] = {"id": 0}
        self.approved(execute(data))

    def test_duplicate_reaction_rows_still_mean_thumbs_up_exists(self):
        data = fixture()
        data["reactions"] *= 2
        self.approved(execute(data))

    def test_unrelated_summary_edit_during_wait_does_not_add_a_gate(self):
        def change(endpoint, count, data):
            if endpoint.endswith("/reactions") and count == 1:
                data["reactions"] = []
            elif endpoint.endswith("/comments") and count == 2:
                data["comments"][0]["body"] += "\nAdvisory text updated.\n"
                data["reactions"] = fixture()["reactions"]
        self.approved(execute(fixture(), mutate=change))

    def test_each_approval_input_is_read_once(self):
        result = execute(fixture())
        self.approved(result)
        self.assertEqual(result["reads"], {f"{ROOT}/issues/7/comments": 1,
                                         f"{ROOT}/issues/7/reactions": 1,
                                         f"{ROOT}/pulls/7": 1})
        self.assertEqual(result["writes"][0][-1], "commit_id=" + HEAD)

    def test_no_extra_head_read_while_thumbs_up_is_missing(self):
        data = fixture()
        data["reactions"] = []
        result = execute(data)
        self.withheld(result)
        self.assertNotIn(f"{ROOT}/pulls/7", result["reads"])

    def test_signal_changes_after_read_do_not_trigger_an_extra_read(self):
        def change(endpoint, count, data):
            if endpoint == f"{ROOT}/pulls/7":
                data["comments"] = [summary(code="Running")]
                data["reactions"] = []
        result = execute(fixture(), mutate=change)
        self.approved(result)
        self.assertTrue(all(count == 1 for count in result["reads"].values()))

    def test_api_failures_and_invalid_json_fail_without_leaking(self):
        for failure in (f"{ROOT}/pulls/7", f"{ROOT}/issues/7/comments", f"{ROOT}/issues/7/reactions", "json", "post"):
            with self.subTest(failure=failure):
                result = execute(fixture(), failure=failure)
                self.assertEqual(result["code"], 1, result)
                self.assertNotIn("secret", result["output"])
                self.assertNotIn("dummy-test-token", result["output"])
                self.assertNotIn("Codex approval: Approved", result["output"])

    def test_malformed_pagination_fails(self):
        for value in ({}, [None], [[None]]):
            with self.subTest(value=value):
                result = execute(fixture(), pagination=value)
                self.assertEqual(result["code"], 1, result)
                self.assertEqual(result["writes"], [])

    def test_unconfirmed_approval_is_reported_as_failure(self):
        for response in ({"state": "PENDING"}, [], {"state": "APPROVED"}, {"state": "APPROVED", "commit_id": "b" * 40}):
            with self.subTest(response=response):
                result = execute(fixture(), response=response)
                self.assertEqual(result["code"], 1, result)
                self.assertNotIn("Codex approval: Approved", result["output"])

    def test_missing_step_summary_file_does_not_change_decision(self):
        self.approved(execute(fixture(), summary_file=False))

    def test_repeated_events_do_not_consult_review_history(self):
        self.approved(execute(fixture()))
        self.approved(execute(fixture()))


if __name__ == "__main__":
    unittest.main()
