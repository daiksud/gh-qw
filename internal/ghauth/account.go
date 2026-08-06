package ghauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Account is one gh-authenticated login for a host.
type Account struct {
	Login  string
	Active bool
}

// Runner is the gh CLI capability required by ghauth. *ghcmd.Runner
// satisfies it.
type Runner interface {
	Output(ctx context.Context, args ...string) ([]byte, error)
}

type authStatusHost struct {
	State  string `json:"state"`
	Active bool   `json:"active"`
	Login  string `json:"login"`
}

type authStatusOutput struct {
	Hosts map[string][]authStatusHost `json:"hosts"`
}

// ListAccounts returns every successfully authenticated account for host,
// using `gh auth status --json hosts`. Entries whose state is not "success"
// are excluded, and duplicate logins are removed while keeping the first
// occurrence's Active flag.
func ListAccounts(ctx context.Context, runner Runner, host string) ([]Account, error) {
	if strings.TrimSpace(host) == "" {
		return nil, errors.New("list gh accounts: host is required")
	}

	output, err := runner.Output(ctx, "auth", "status", "--json", "hosts")
	if err != nil {
		return nil, fmt.Errorf("list gh accounts for %q: %w", host, err)
	}

	var parsed authStatusOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("list gh accounts for %q: parse gh auth status output: %w", host, err)
	}

	seen := make(map[string]struct{}, len(parsed.Hosts[host]))
	accounts := make([]Account, 0, len(parsed.Hosts[host]))
	for _, entry := range parsed.Hosts[host] {
		if entry.State != "success" {
			continue
		}
		if entry.Login == "" {
			continue
		}
		if _, duplicate := seen[entry.Login]; duplicate {
			continue
		}
		seen[entry.Login] = struct{}{}
		accounts = append(accounts, Account{Login: entry.Login, Active: entry.Active})
	}
	return accounts, nil
}
