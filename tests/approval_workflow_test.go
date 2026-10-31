package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type approvalObject = map[string]any

const approvalHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func approvalBot() approvalObject {
	return approvalObject{"login": "chatgpt-codex-connector[bot]", "id": 199175422, "type": "Bot"}
}

func approvalComment(id int, body, at string) approvalObject {
	return approvalObject{"id": id, "body": body, "created_at": at, "updated_at": at,
		"user": approvalBot(), "performed_via_github_app": approvalObject{"id": 1144995, "slug": "chatgpt-codex-connector"}}
}

func approvalRequest(id int, at string) approvalObject {
	return approvalObject{"id": id, "body": "@codex review", "created_at": at, "updated_at": at, "user": approvalObject{"login": "author", "type": "User"}}
}

// The nullable result, receipt wording, ordering and fractional completion times
// reproduce the public evidence from daiksud/gh-settings#534; identities and timestamps are
// deliberately independent of the workflow's implementation.
func approvalFixture() approvalObject {
	state := approvalObject{
		"code_review":     approvalObject{"commit_sha": approvalHead, "status": "completed", "has_unresolved_findings": false, "has_any_findings": false},
		"security_review": approvalObject{"commit_sha": approvalHead, "status": "completed", "has_unresolved_findings": nil, "has_any_findings": nil},
	}
	request := approvalRequest(100, "2026-10-31T02:11:22Z")
	code := approvalComment(102, "<!-- codex-review-receipt:v1 -->\n\nCodex Review: Didn't find any major issues. Keep them coming!\n\n**Reviewed commit:** `aaaaaaaaaa`\n", "2026-10-31T02:12:36Z")
	security := approvalComment(103, "<!-- codex-review-receipt:v1 -->\n\n### 🛡️ Codex Security Review · _Automatically triggered_\n\nSecurity review completed. No security issues were found in this pull request.\n\n**Reviewed commit:** `aaaaaaaaaa`\n\n[View security finding report](https://chatgpt.com/codex/cloud/tasks/task_example)", "2026-10-31T02:13:10Z")
	for _, receipt := range []approvalObject{code, security} {
		receipt["body"] = receipt["body"].(string) + "\n<details>\nReviews are triggered when you comment \"@codex review\" or \"@codex security review\".\n</details>\n"
	}
	summary := approvalComment(101, "", "2026-10-31T02:11:44Z")
	summary["updated_at"] = "2026-10-31T02:13:13Z"
	reactionUser := approvalBot()
	reactionUser["type"] = "User" // Actual reactions API representation for this Bot ID.
	return approvalObject{
		"state": state, "request": request, "summary": summary, "code": code, "security": security,
		"reaction": approvalObject{"id": 200, "user": reactionUser, "content": "+1", "created_at": "2026-10-31T02:13:15Z"},
		"pr":       approvalObject{"state": "open", "draft": false, "head": approvalObject{"sha": approvalHead}, "base": approvalObject{"ref": "main", "sha": strings.Repeat("b", 40)}},
	}
}

func approvalSummary(f approvalObject) string {
	state, _ := json.Marshal(f["state"])
	metadata := fmt.Sprintf(`{"blockingSeverityThreshold":"P0","headSha":%q,"mergeGateEnabled":false,"pullRequestNumber":7,"repository":"owner/repo","status":"completed"}`, approvalHead)
	return "<!-- codex-pull-request-review-summary -->\n<!-- codex-review-state:v1 " + string(state) + " -->\n<!-- codex-security-review:v1 " + metadata + " -->\n" +
		"| 📝 **Code Review** | ✅ **Completed** <relative-time datetime=\"2026-10-31T02:12:38.414268Z\">2026-10-31T02:12:38.414268Z</relative-time> | `aaaaaaa` | Manual request |\n" +
		"| 🔒 **Security Review** | ✅ **Completed** <relative-time datetime=\"2026-10-31T02:13:11.462255Z\">2026-10-31T02:13:11.462255Z</relative-time> | `aaaaaaa` | Manual request |\n" +
		"\n<details>\nReviews are triggered when you comment \"@codex review\" or \"@codex security review\".\n</details>\n"
}

func TestCodexApprovalEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the approval workflow runs on Linux")
	}
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	workflow := filepath.Join("..", ".github", "workflows", "approve-codex-review.yml")
	if override := os.Getenv("APPROVAL_WORKFLOW_FIXTURE"); override != "" {
		workflow = override
	}
	data, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatal(err)
	}
	_, block, found := strings.Cut(string(data), "        run: |\n")
	if !found {
		t.Fatal("missing approval script")
	}
	var script strings.Builder
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "          ") {
			break
		}
		script.WriteString(strings.TrimPrefix(line, "          ") + "\n")
	}
	cases := []struct {
		name    string
		approve bool
		fail    bool
	}{
		{"nullable clean", true, false}, {"structured clean", true, false}, {"draft", true, false},
		{"comment event", true, false}, {"old comment event", false, false}, {"prior completed cycle", true, false},
		{"code findings", false, false}, {"security findings", false, false}, {"security any findings", false, false},
		{"missing security flag", false, false}, {"wrong flag type", false, false}, {"code incomplete", false, false},
		{"security incomplete", false, false}, {"code stale", false, false}, {"security stale", false, false},
		{"spoofed summary", false, false}, {"spoofed receipt", false, false}, {"wrong app", false, false},
		{"no security receipt", false, false}, {"security receipt findings", false, false}, {"wrong receipt head", false, false},
		{"old same-head receipt", false, false}, {"edited receipt", false, false}, {"duplicate receipt", false, false},
		{"unknown bot comment", false, false}, {"no request", false, false}, {"edited request", false, false},
		{"overlapping requests", false, false}, {"security-only request", false, false}, {"new request", false, false},
		{"old reaction", false, false}, {"same-second reaction", false, false}, {"other reaction", false, false},
		{"missing reaction", false, false}, {"delayed reaction", true, false}, {"closed", false, false},
		{"head changed", false, false}, {"closed during check", false, false}, {"summary changed", false, false},
		{"request during check", false, false}, {"receipt edited during check", false, false}, {"reaction removed", false, false},
		{"already approved", false, false}, {"changes requested", false, false}, {"ambiguous short sha", false, true},
		{"pagination failed", false, true}, {"reaction fetch failed", false, true}, {"post rejected", false, true},
		{"post non-json", false, true}, {"malformed summary", false, false}, {"automatic nullable cycle", false, false},
		{"initial automatic overlap", false, false}, {"edited older request", false, false},
		{"wrong summary app", false, false}, {"receipt missing app", false, false}, {"duplicate marker", false, false},
		{"wrong security metadata", false, false}, {"completion before receipt", false, false},
		{"wrong resolved sha", false, false}, {"unconfirmed approval", true, true}, {"malformed pagination", false, true},
		{"wrong reaction account", false, false},
		{"edited older structured request", false, false}, {"same-second older structured request", false, false},
		{"prior retarget", true, false},
		{"retarget after review", false, false},
		{"retarget during review", false, false},
		{"same-second retarget", false, false},
		{"automatic retarget", false, false},
		{"retarget without request", false, false},
		{"structured retarget", false, false},
		{"retarget during check", false, false},
		{"base ref changed", false, false},
		{"base sha changed", false, false},
		{"missing base", false, false},
		{"timeline failed", false, true},
		{"malformed timeline", false, true},
		{"retarget missing timestamp", false, false},
		{"same-second requests", false, false}, {"same-second summary start", false, false}, {"wrong row head", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := approvalFixture()
			state := f["state"].(approvalObject)
			codeState, securityState := state["code_review"].(approvalObject), state["security_review"].(approvalObject)
			request, summary := f["request"].(approvalObject), f["summary"].(approvalObject)
			code, security := f["code"].(approvalObject), f["security"].(approvalObject)
			pr, reaction := f["pr"].(approvalObject), f["reaction"].(approvalObject)
			switch tc.name {
			case "structured clean", "edited older structured request", "same-second older structured request", "structured retarget", "retarget without request":
				securityState["has_any_findings"], securityState["has_unresolved_findings"] = false, false
			case "draft":
				pr["draft"] = true
			case "code findings":
				codeState["has_unresolved_findings"] = true
			case "security findings":
				securityState["has_unresolved_findings"] = true
			case "security any findings":
				securityState["has_any_findings"] = true
			case "missing security flag":
				delete(securityState, "has_unresolved_findings")
			case "wrong flag type":
				securityState["has_unresolved_findings"] = "false"
			case "code incomplete":
				codeState["status"] = "in_progress"
			case "security incomplete":
				securityState["status"] = "in_progress"
			case "code stale":
				codeState["commit_sha"] = strings.Repeat("b", 40)
			case "security stale":
				securityState["commit_sha"] = strings.Repeat("b", 40)
			case "spoofed summary":
				summary["user"] = approvalObject{"login": "outsider", "id": 1, "type": "User"}
			case "spoofed receipt":
				security["user"] = approvalObject{"login": "outsider", "id": 1, "type": "User"}
			case "wrong app":
				security["performed_via_github_app"] = approvalObject{"id": 42}
			case "wrong summary app":
				summary["performed_via_github_app"] = approvalObject{"id": 42}
			case "receipt missing app":
				delete(security, "performed_via_github_app")
			case "initial automatic overlap":
				summary["created_at"] = "2026-10-31T02:11:00Z"
			case "same-second summary start":
				summary["created_at"] = request["created_at"]
			case "security receipt findings":
				security["body"] = strings.ReplaceAll(security["body"].(string), "No security issues were found in this pull request.", "Security issues were found.")
			case "wrong receipt head":
				security["body"] = strings.ReplaceAll(security["body"].(string), "aaaaaaaaaa", "bbbbbbbbbb")
			case "old same-head receipt":
				security["created_at"], security["updated_at"] = "2026-10-31T02:10:00Z", "2026-10-31T02:10:00Z"
			case "edited receipt":
				security["updated_at"] = "2026-10-31T02:13:12Z"
			case "edited request":
				request["updated_at"] = "2026-10-31T02:11:23Z"
			case "security-only request":
				request["body"] = "@codex security review"
			case "old reaction":
				reaction["created_at"] = "2026-10-31T02:11:00Z"
			case "same-second reaction":
				reaction["created_at"] = summary["updated_at"]
			case "other reaction":
				reaction["user"] = approvalObject{"login": "other", "id": 1, "type": "User"}
			case "wrong reaction account":
				reaction["user"].(approvalObject)["id"] = 1
			case "closed":
				pr["state"] = "closed"
			case "missing base":
				delete(pr, "base")
			}
			summary["body"] = approvalSummary(f)
			if tc.name == "malformed summary" {
				summary["body"] = "<!-- codex-pull-request-review-summary -->\n<!-- codex-review-state:v1 broken -->"
			}
			if tc.name == "automatic nullable cycle" {
				summary["body"] = strings.ReplaceAll(summary["body"].(string), "Manual request", "Automatic review")
			}
			if tc.name == "duplicate marker" {
				summary["body"] = summary["body"].(string) + "<!-- codex-review-state:v1 {} -->\n"
			}
			if tc.name == "wrong security metadata" {
				summary["body"] = strings.ReplaceAll(summary["body"].(string), `"pullRequestNumber":7`, `"pullRequestNumber":8`)
			}
			if tc.name == "completion before receipt" {
				summary["body"] = strings.ReplaceAll(summary["body"].(string), "02:13:11.462255", "02:13:09.462255")
			}
			if tc.name == "wrong row head" {
				summary["body"] = strings.ReplaceAll(summary["body"].(string), "`aaaaaaa`", "`bbbbbbb`")
			}
			comments := []any{request, summary, code, security}
			switch tc.name {
			case "no request", "retarget without request":
				comments = comments[1:]
			case "no security receipt":
				comments = comments[:3]
			case "duplicate receipt":
				comments = append(comments, approvalComment(104, security["body"].(string), "2026-10-31T02:13:11Z"))
			case "unknown bot comment":
				comments = append(comments, approvalComment(104, "A security finding needs attention", "2026-10-31T02:13:12Z"))
			case "overlapping requests", "prior completed cycle", "edited older request", "edited older structured request", "same-second older structured request":
				comments = append([]any{approvalRequest(90, "2026-10-31T02:00:00Z")}, comments...)
				if tc.name != "overlapping requests" {
					comments = append(comments, approvalComment(91, code["body"].(string), "2026-10-31T02:01:00Z"), approvalComment(92, security["body"].(string), "2026-10-31T02:02:00Z"))
				}
				if tc.name == "edited older request" {
					comments[0].(approvalObject)["updated_at"] = "2026-10-31T02:13:14Z"
				}
				if tc.name == "edited older structured request" || tc.name == "same-second older structured request" {
					edited := "2026-10-31T02:13:14Z"
					if tc.name == "same-second older structured request" {
						edited = summary["updated_at"].(string)
					}
					comments[0].(approvalObject)["updated_at"] = edited
				}
			case "new request":
				comments = append(comments, approvalRequest(104, "2026-10-31T02:13:14Z"))
			case "same-second requests":
				comments = append(comments, approvalRequest(99, request["created_at"].(string)))
			}
			root := t.TempDir()
			write := func(name string, data []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), data, mode); err != nil {
					t.Fatal(err)
				}
			}
			fixture, _ := json.Marshal(approvalObject{"comments": comments, "pr": pr, "reaction": reaction, "summary": summary, "scenario": tc.name})
			write("fixture.json", fixture, 0600)
			write("workflow.sh", []byte(script.String()), 0600)
			write("sleep", []byte("#!/bin/sh\nexit 0\n"), 0700)
			write("gh", []byte(approvalAPIStub), 0700)
			commentID := ""
			if tc.name == "comment event" {
				commentID = "101"
			} else if tc.name == "old comment event" {
				commentID = "99"
			}
			cmd := exec.Command("bash", filepath.Join(root, "workflow.sh"))
			cmd.Env = []string{"PATH=" + root + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + root, "GH_CONFIG_DIR=" + root, "GH_TOKEN=dummy-not-a-token", "GITHUB_REPOSITORY=owner/repo", "PR_NUMBER=7", "COMMENT_ID=" + commentID, "FIXTURE_ROOT=" + root}
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected workflow status: %v %s", err, output)
			}
			approvals, _ := os.ReadFile(filepath.Join(root, "approvals"))
			if (len(approvals) > 0) != tc.approve {
				t.Fatalf("approval=%q, expected=%v; output=%s", approvals, tc.approve, output)
			}
			if tc.approve && string(approvals) != approvalHead+"\n" {
				t.Fatalf("approval did not pin the exact head: %s", approvals)
			}
			if strings.Contains(string(output), "dummy-not-a-token") {
				t.Fatal("credential or untrusted response leaked")
			}
			if len(strings.TrimSpace(string(output))) == 0 {
				t.Fatal("missing approval/hold diagnostic")
			}
		})
	}
}

// No live gh process is reachable. Each read is paginated, and races are injected
// at the second observation; all writes must be one exact-SHA review submission.
const approvalAPIStub = `#!/usr/bin/env python3
import json, os, pathlib, sys
root = pathlib.Path(os.environ['FIXTURE_ROOT'])
f = json.loads((root / 'fixture.json').read_text())
s = f['scenario']
a = sys.argv[1:]
assert a[0] == 'api', a
endpoint = a[1]
counts_file = root / 'counts'
counts = json.loads(counts_file.read_text()) if counts_file.exists() else {}
counts[endpoint] = counts.get(endpoint, 0) + 1
counts_file.write_text(json.dumps(counts))
n = counts[endpoint]
if endpoint == '--method':
    assert a[2:] == ['POST', 'repos/owner/repo/pulls/7/reviews', '-f', 'event=APPROVE', '-f', 'commit_id=' + 'a'*40], a
    if s in ('post rejected', 'post non-json'):
        print('dummy-not-a-token', file=sys.stderr)
        print(json.dumps({'message': 'denied', 'errors': [{'field': 'commit_id'}], 'secret': 'dummy-not-a-token'}) if s == 'post rejected' else 'dummy-not-a-token')
        sys.exit(42)
    with (root / 'approvals').open('a') as out: out.write('a'*40 + '\n')
    result = {'state': 'APPROVED', 'commit_id': 'a'*40}
    if s == 'unconfirmed approval': result['state'] = 'PENDING'
elif endpoint == 'repos/owner/repo/pulls/7':
    result = f['pr']
    if n > 1 and s == 'head changed': result['head']['sha'] = 'b'*40
    if n > 1 and s == 'closed during check': result['state'] = 'closed'
    if n > 1 and s == 'base ref changed': result['base']['ref'] = 'release'
    if n > 1 and s == 'base sha changed': result['base']['sha'] = 'c'*40
elif endpoint == 'repos/owner/repo/issues/7/comments?per_page=100':
    assert a[2:] == ['--paginate', '--slurp'], a
    if s == 'pagination failed': sys.exit(55)
    comments = f['comments']
    if n > 1:
        if s == 'summary changed':
            for c in comments:
                if c['id'] == 101: c['body'] = c['body'].replace('completed', 'in_progress')
        if s == 'receipt edited during check':
            for c in comments:
                if c['id'] == 103: c['body'] += '\nchanged'; c['updated_at'] = '2026-10-31T02:13:20Z'
        if s == 'request during check':
            comments.append({'id': 104, 'body': '@codex review', 'created_at': '2026-10-31T02:13:20Z', 'updated_at': '2026-10-31T02:13:20Z'})
    result = [comments[:2], comments[2:]]
    if s == 'malformed pagination': result = [comments[:2], {'message': 'error'}]
elif endpoint == 'repos/owner/repo/issues/7/timeline?per_page=100':
    assert a[2:] == ['--paginate', '--slurp'], a
    if s == 'timeline failed': sys.exit(55)
    result = [[], []]
    if s in ('prior retarget', 'retarget after review', 'retarget during review', 'same-second retarget', 'automatic retarget', 'retarget without request', 'structured retarget', 'retarget missing timestamp') or (s == 'retarget during check' and n > 1):
        at = '2026-10-31T02:13:14Z'
        if s == 'prior retarget': at = '2026-10-31T02:10:00Z'
        if s == 'retarget during review': at = '2026-10-31T02:12:00Z'
        if s == 'same-second retarget': at = '2026-10-31T02:11:22Z'
        event = {'id': 300, 'event': 'automatic_base_change_succeeded' if s == 'automatic retarget' else 'base_ref_changed', 'created_at': at}
        if s == 'retarget missing timestamp': del event['created_at']
        result[1] = [event]
    if s == 'malformed timeline': result = [[], {'message': 'error'}]
elif endpoint == 'repos/owner/repo/issues/7/reactions?per_page=100':
    assert a[2:] == ['--paginate', '--slurp'], a
    if s == 'reaction fetch failed': sys.exit(55)
    result = [[], [f['reaction']]]
    if s == 'missing reaction' or (s == 'delayed reaction' and n == 1) or (s == 'reaction removed' and n > 1): result = [[], []]
elif endpoint == 'repos/owner/repo/pulls/7/reviews?per_page=100':
    assert a[2:] == ['--paginate', '--slurp'], a
    result = [[], []]
    if s == 'already approved': result[1] = [{'user': {'login': 'github-actions[bot]'}, 'state': 'APPROVED', 'commit_id': 'a'*40}]
    if s == 'changes requested': result[1] = [{'user': {'login': 'reviewer'}, 'state': 'CHANGES_REQUESTED', 'commit_id': 'a'*40}]
elif endpoint == 'repos/owner/repo/commits/aaaaaaaaaa':
    if s == 'ambiguous short sha': sys.exit(22)
    result = {'sha': 'a'*40}
    if s == 'wrong resolved sha': result['sha'] = 'b'*40
else:
    raise AssertionError('unexpected API ' + str(a))
print(json.dumps(result))
`
