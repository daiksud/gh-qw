// Package testenv isolates Git fixtures from the caller's configuration and credentials.
package testenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Environment preserves ordinary process settings and installs fixture-owned
// HOME/XDG paths. Append intentional test settings after this base environment.
func Environment(home string) []string {
	blocked := map[string]struct{}{
		"EMAIL": {}, "HOME": {}, "USERPROFILE": {}, "APPDATA": {}, "LOCALAPPDATA": {},
		"LANG": {}, "LC_ALL": {}, "NO_COLOR": {}, "SSH_AUTH_SOCK": {}, "SSH_AGENT_PID": {},
		"XDG_CACHE_HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_DATA_HOME": {},
	}
	environment := make([]string, 0, len(os.Environ())+24)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if _, remove := blocked[key]; remove {
			continue
		}
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "GH_") ||
			strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "GHQ_") ||
			strings.HasPrefix(key, "GHQW_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"HOME="+home,
		"USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "xdg-config"),
		"LOCALAPPDATA="+filepath.Join(home, "xdg-data"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "xdg-config"),
		"XDG_DATA_HOME="+filepath.Join(home, "xdg-data"),
		"XDG_CACHE_HOME="+filepath.Join(home, "xdg-cache"),
		"GH_CONFIG_DIR="+filepath.Join(home, "gh"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=gh-qw tests",
		"GIT_AUTHOR_EMAIL=gh-qw@example.invalid",
		"GIT_COMMITTER_NAME=gh-qw tests",
		"GIT_COMMITTER_EMAIL=gh-qw@example.invalid",
		"EMAIL=gh-qw@example.invalid",
		"LC_ALL=C",
		"LANG=C",
		"NO_COLOR=1",
	)
}

// Git runs a real Git command with isolated configuration and reports failures
// at its caller. Repository-local configuration and explicit -c options remain active.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = Environment(t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %q: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return string(output)
}
