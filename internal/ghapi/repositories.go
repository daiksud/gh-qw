package ghapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/repospec"
)

// RepositoryExists checks a repository using gh's authentication and redirect
// handling. Only an HTTP 404 from a completed gh request means not found;
// authentication, transport, rate-limit, and process failures remain errors.
func (c *Client) RepositoryExists(ctx context.Context, host, owner, repo string) (bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	identity := strings.Join([]string{host, owner, repo}, "/")
	if _, err := repositoryScope(ctx, host, owner, repo); err != nil {
		return false, fmt.Errorf("lookup repository %q: %w", identity, err)
	}

	path := fmt.Sprintf("repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	output, err := c.output(ctx, "api", "--hostname", host, path, "--method", "HEAD", "--include", "--silent")
	if ctx.Err() != nil {
		return false, fmt.Errorf("lookup repository %q: %w", identity, errors.Join(err, ctx.Err()))
	}
	response, parseErr := http.ReadResponse(bufio.NewReader(bytes.NewReader(output)), &http.Request{Method: http.MethodHead})
	if parseErr == nil {
		defer response.Body.Close()
	}
	if err != nil {
		exitCode, exited := ghcmd.CommandExitCode(err)
		if parseErr == nil && response.StatusCode == http.StatusNotFound && exited && exitCode == 1 &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return false, nil
		}
		return false, fmt.Errorf("lookup repository %q: %w", identity, err)
	}
	if parseErr != nil {
		return false, fmt.Errorf("lookup repository %q: invalid gh HTTP response: %w", identity, parseErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("lookup repository %q: unexpected HTTP status %d", identity, response.StatusCode)
	}
	return true, nil
}

const ownerRepositoriesQuery = `query($owner: String!, $endCursor: String) {
  repositoryOwner(login: $owner) {
    login
    repositories(first: 100, after: $endCursor, ownerAffiliations: OWNER) {
      nodes { nameWithOwner }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

type ownerRepositoriesPage struct {
	Data struct {
		Owner *struct {
			Login        string `json:"login"`
			Repositories *struct {
				Nodes []struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"nodes"`
				PageInfo *struct {
					HasNextPage *bool  `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"repositories"`
		} `json:"repositoryOwner"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

// OwnerRepositories returns all visible repositories owned by a user or
// organization, including forks and private repositories allowed by gh's
// credentials. gh owns pagination; a failed page never yields a partial list.
func (c *Client) OwnerRepositories(ctx context.Context, host, owner string) ([]string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	owner = strings.TrimSpace(owner)
	identity := host + "/" + owner
	scope, err := repositoryScope(ctx, host, owner, "repository")
	if err != nil {
		return nil, fmt.Errorf("list repositories for %q: %w", identity, err)
	}
	output, err := c.output(ctx, "api", "--hostname", host, "graphql", "--paginate", "--slurp",
		"--raw-field", "query="+ownerRepositoriesQuery, "--raw-field", "owner="+owner)
	if err != nil || ctx.Err() != nil {
		return nil, fmt.Errorf("list repositories for %q: %w", identity, errors.Join(err, ctx.Err()))
	}
	var pages []ownerRepositoriesPage
	if err := json.Unmarshal(output, &pages); err != nil {
		return nil, fmt.Errorf("list repositories for %q: invalid gh JSON response: %w", identity, err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("list repositories for %q: API response did not include a page", identity)
	}
	var repositories []string
	for index, page := range pages {
		if len(page.Errors) != 0 {
			return nil, fmt.Errorf("list repositories for %q: API response contains errors", identity)
		}
		if page.Data.Owner == nil || !strings.EqualFold(page.Data.Owner.Login, owner) {
			return nil, fmt.Errorf("list repositories for %q: API response did not include the requested owner", identity)
		}
		connection := page.Data.Owner.Repositories
		if connection == nil || connection.Nodes == nil || connection.PageInfo == nil || connection.PageInfo.HasNextPage == nil {
			return nil, fmt.Errorf("list repositories for %q: API response has an incomplete repository page", identity)
		}
		more := *connection.PageInfo.HasNextPage
		if more != (index < len(pages)-1) || (more && connection.PageInfo.EndCursor == "") {
			return nil, fmt.Errorf("list repositories for %q: API response has incomplete pagination", identity)
		}
		for _, node := range connection.Nodes {
			repository, err := repospec.ParseIdentity(scope.Host + "/" + node.NameWithOwner)
			if err != nil || !strings.EqualFold(repository.Owner, owner) {
				return nil, fmt.Errorf("list repositories for %q: API response contains an invalid repository identity", identity)
			}
			repositories = append(repositories, node.NameWithOwner)
		}
	}
	slices.Sort(repositories)
	return slices.Compact(repositories), nil
}

// API hosts retain their optional transport port, while repository identities
// omit it. Reuse the transport-aware parser rather than treating a host as an
// already-canonical identity component.
func repositoryScope(ctx context.Context, host, owner, repo string) (repospec.Spec, error) {
	if host == "" {
		return repospec.Spec{}, errors.New("host is required")
	}
	if err := repospec.ValidateOwner(owner); err != nil {
		return repospec.Spec{}, err
	}
	if err := repospec.ValidateRepository(repo); err != nil {
		return repospec.Spec{}, err
	}
	parsed, err := repospec.Parse(host + "/" + owner + "/" + repo)
	if err != nil {
		return repospec.Spec{}, err
	}
	return parsed.Resolve(ctx, repospec.Options{})
}
