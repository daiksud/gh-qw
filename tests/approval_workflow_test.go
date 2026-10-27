package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Execute the Linux workflow's actual inline script with local paginated API
// fixtures and real jq. Never send a review or use an ambient credential.
func TestCodexApprovalPagination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the approval workflow runs on Linux, not Windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal(err)
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
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	summary := func(sha string, unresolved bool) string {
		state := map[string]any{}
		for _, kind := range []string{"code_review", "security_review"} {
			state[kind] = map[string]any{"commit_sha": sha, "status": "completed", "has_unresolved_findings": unresolved}
		}
		encoded, _ := json.Marshal(state)
		return "<!-- codex-pull-request-review-summary -->\n<!-- codex-review-state:v1 " + string(encoded) + " -->"
	}
	for _, scenario := range []string{"approve", "comment event", "stale", "unresolved", "already approved", "head changed", "pagination failed", "no bot summary", "post rejected", "post non-json"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, value string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			body := summary(head, scenario == "unresolved")
			if scenario == "stale" {
				body = summary(strings.Repeat("b", 40), false)
			}
			pages := [][]map[string]any{{{"user": map[string]string{"login": "someone"}, "body": body}}, {{"user": map[string]string{"login": "chatgpt-codex-connector[bot]"}, "body": body}}}
			if scenario == "no bot summary" {
				pages = pages[:1]
			}
			encoded, _ := json.Marshal(pages)
			write("comments.json", string(encoded), 0600)
			encoded, _ = json.Marshal(map[string]string{"body": body})
			write("summary.json", string(encoded), 0600)
			write("workflow.sh", script.String(), 0600)
			write("gh", `#!/bin/sh
set -eu
case " $* " in *" --slurp "*) case " $* " in *" --jq "*) echo incompatible-gh-flags >&2; exit 64;; esac;; esac
endpoint=$2
case "$endpoint" in
  repos/owner/repo/pulls/7)
    if test -f "$FIXTURE_ROOT/pr-read" && test "$SCENARIO" = 'head changed'; then sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb; else sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; fi
    touch "$FIXTURE_ROOT/pr-read"
    printf '{"state":"open","draft":false,"head":{"sha":"%s"}}\n' "$sha";;
  'repos/owner/repo/issues/7/comments?per_page=100')
    test "$SCENARIO" != 'pagination failed' || exit 55
    test "$#" = 4 && test "$3" = --paginate && test "$4" = --slurp
    cat "$FIXTURE_ROOT/comments.json";;
  repos/owner/repo/issues/comments/123)
    test "$3" = --jq && test "$4" = .body
    jq -r .body "$FIXTURE_ROOT/summary.json";;
  'repos/owner/repo/issues/7/reactions?per_page=100')
    printf '[[{"user":{"login":"other"},"content":"eyes"}],[{"user":{"login":"chatgpt-codex-connector[bot]"},"content":"+1"}]]\n';;
  'repos/owner/repo/pulls/7/reviews?per_page=100')
    if test "$SCENARIO" = 'already approved'; then
      printf '[[{"user":{"login":"github-actions[bot]"},"state":"APPROVED","commit_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]]\n'
    else printf '[[],[]]\n'; fi;;
  --method)
    test "$3" = POST && test "$4" = repos/owner/repo/pulls/7/reviews
    test "$5" = -f && test "$6" = event=APPROVE
    test "$7" = -f && test "$8" = commit_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    if test "$SCENARIO" = 'post rejected'; then
      printf '{"message":"fixture rejected review","errors":[{"field":"commit_id","code":"invalid"}],"ignored":"dummy-not-a-token"}\n'
      exit 42
    fi
    if test "$SCENARIO" = 'post non-json'; then
      printf 'dummy-not-a-token unexpected proxy response\n'
      exit 42
    fi
    printf '%s\n' "$8" >> "$FIXTURE_ROOT/approvals";;
  *) echo unexpected-api-call >&2; exit 70;;
esac
`, 0700)
			comment := ""
			if scenario == "comment event" {
				comment = "123"
			}
			cmd := exec.Command(bash, filepath.Join(root, "workflow.sh"))
			cmd.Env = []string{"PATH=" + root + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + root, "XDG_CONFIG_HOME=" + root, "GH_CONFIG_DIR=" + root, "GH_TOKEN=dummy-not-a-token", "GITHUB_REPOSITORY=owner/repo", "PR_NUMBER=7", "COMMENT_ID=" + comment, "FIXTURE_ROOT=" + root, "SCENARIO=" + scenario}
			output, err := cmd.CombinedOutput()
			if (err != nil) != (scenario == "pagination failed" || scenario == "post rejected" || scenario == "post non-json") {
				t.Fatalf("unexpected workflow status: %v %s", err, output)
			}
			if scenario == "post rejected" && (!strings.Contains(string(output), "fixture rejected review") || !strings.Contains(string(output), "commit_id")) {
				t.Fatal("GitHub validation error details were discarded", string(output))
			}
			if scenario == "post non-json" && !strings.Contains(string(output), "without a JSON validation response") {
				t.Fatal("missing safe fallback diagnostic", string(output))
			}
			if strings.Contains(string(output), "dummy-not-a-token") {
				t.Fatal("credential leaked")
			}
			approvals, _ := os.ReadFile(filepath.Join(root, "approvals"))
			want := ""
			if scenario == "approve" || scenario == "comment event" {
				want = "commit_id=" + head + "\n"
			}
			if string(approvals) != want {
				t.Fatalf("wrong approval side effect: %q, want %q", approvals, want)
			}
		})
	}
}
