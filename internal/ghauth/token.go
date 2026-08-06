package ghauth

import (
	"context"
	"fmt"
	"strings"
)

// Token returns the authentication token for login on host, using
// `gh auth token --user <login> --hostname <host>`. It works for any
// gh-authenticated account, not only the active one, and never includes the
// retrieved token in a returned error.
func Token(ctx context.Context, runner Runner, host, login string) (string, error) {
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("get gh account token: host is required")
	}
	if strings.TrimSpace(login) == "" {
		return "", fmt.Errorf("get gh account token: login is required")
	}

	output, err := runner.Output(ctx, "auth", "token", "--user", login, "--hostname", host)
	if err != nil {
		return "", fmt.Errorf("get gh account token for %q on %q: %w", login, host, err)
	}
	return strings.TrimSpace(string(output)), nil
}
