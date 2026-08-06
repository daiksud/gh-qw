package ghauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Source explains why a Resolution did or did not override gh's ambient
// authentication.
type Source int

const (
	// SourceSelected means Login and Token were chosen for the repository
	// owner (a cache hit, an owner-matching login, or an explicit prompt
	// answer). Token should be injected as GH_TOKEN for gh subprocesses and
	// API calls scoped to that repository.
	SourceSelected Source = iota
	// SourceExplicitEnv means the caller's environment already set GH_TOKEN
	// or GITHUB_TOKEN, so automatic selection was skipped entirely; an
	// explicit override always wins over automatic selection.
	SourceExplicitEnv
)

// Resolution is the outcome of resolving which gh account a network
// operation for a specific host and repository owner should use.
type Resolution struct {
	Source Source
	// Login is the selected account, set only when Source == SourceSelected.
	Login string
	// Token is the GH_TOKEN override to inject; empty means defer to gh's
	// ambient authentication.
	Token string
}

// CacheStore is the persistence capability required by Resolver. *Cache
// satisfies it.
type CacheStore interface {
	Lookup(host, owner string) (string, bool, error)
	Store(host, owner, login string) error
	Delete(host, owner string) error
}

// ResolverOptions supplies Resolver dependencies. Runner and Cache are
// required; other fields default to the operating system or TerminalPrompt.
type ResolverOptions struct {
	Runner     Runner
	Cache      CacheStore
	Prompt     Prompter
	Stdin      io.Reader
	Stderr     io.Writer
	IsTerminal func(io.Reader) bool
	LookupEnv  func(string) (string, bool)
}

// Resolver decides which gh account a network operation for a given host and
// repository owner should use to authenticate.
type Resolver struct {
	runner     Runner
	cache      CacheStore
	prompt     Prompter
	stdin      io.Reader
	stderr     io.Writer
	isTerminal func(io.Reader) bool
	lookupEnv  func(string) (string, bool)
}

// NewResolver returns a Resolver.
func NewResolver(options ResolverOptions) *Resolver {
	isTerminal := options.IsTerminal
	if isTerminal == nil {
		isTerminal = func(io.Reader) bool { return false }
	}
	prompt := options.Prompt
	if prompt == nil {
		prompt = TerminalPrompt
	}
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	stdin := options.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	return &Resolver{
		runner:     options.Runner,
		cache:      options.Cache,
		prompt:     prompt,
		stdin:      stdin,
		stderr:     stderr,
		isTerminal: isTerminal,
		lookupEnv:  lookupEnv,
	}
}

// Resolve determines which gh account to use for host/owner, following this
// order: (1) an explicit GH_TOKEN/GITHUB_TOKEN always wins and skips
// automatic selection; (2) a cached login is retried via the fast
// `gh auth token` path; (3) otherwise every gh-authenticated account for
// host is listed, then an owner-matching login or the sole account is selected
// and cached; (4) an interactive caller is prompted when multiple accounts
// remain. Every failure is returned rather than falling back to ambient gh
// authentication.
func (r *Resolver) Resolve(ctx context.Context, host, owner string) (Resolution, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	if strings.TrimSpace(host) == "" {
		return Resolution{}, errors.New("resolve gh account: host is required")
	}
	if strings.TrimSpace(owner) == "" {
		return Resolution{}, errors.New("resolve gh account: owner is required")
	}

	if r.hasExplicitToken() {
		return Resolution{Source: SourceExplicitEnv}, nil
	}

	if r.cache == nil {
		return Resolution{}, errors.New("resolve gh account: cache is required")
	}
	if r.runner == nil {
		return Resolution{}, errors.New("resolve gh account: runner is required")
	}
	login, ok, err := r.cache.Lookup(host, owner)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolve gh account for %s/%s: %w", host, owner, err)
	}
	if ok {
		token, tokenErr := Token(ctx, r.runner, host, login)
		if tokenErr == nil && token != "" {
			return Resolution{Source: SourceSelected, Login: login, Token: token}, nil
		}
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
		if err := r.cache.Delete(host, owner); err != nil {
			return Resolution{}, fmt.Errorf(
				"resolve gh account for %s/%s: delete stale cached account %q: %w",
				host,
				owner,
				login,
				err,
			)
		}
	}

	accounts, err := ListAccounts(ctx, r.runner, host)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolve gh account for %s/%s: %w", host, owner, err)
	}
	if len(accounts) == 0 {
		return Resolution{}, fmt.Errorf(
			"resolve gh account for %s/%s: no authenticated accounts are available for %q",
			host,
			owner,
			host,
		)
	}

	var ownerMatches []Account
	for _, account := range accounts {
		if strings.EqualFold(account.Login, owner) {
			ownerMatches = append(ownerMatches, account)
		}
	}
	if len(ownerMatches) == 1 {
		return r.resolveSelected(ctx, host, owner, ownerMatches[0])
	}
	if len(accounts) == 1 {
		return r.resolveSelected(ctx, host, owner, accounts[0])
	}

	if !r.isTerminal(r.stdin) {
		return Resolution{}, fmt.Errorf(
			"resolve gh account for %s/%s: %d authenticated accounts are available and input is non-interactive",
			host,
			owner,
			len(accounts),
		)
	}

	selected, err := r.prompt(ctx, r.stderr, host, owner, accounts)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolve gh account for %s/%s: choose account: %w", host, owner, err)
	}
	matches := make([]Account, 0, 1)
	for _, account := range accounts {
		if strings.EqualFold(account.Login, selected.Login) {
			matches = append(matches, account)
		}
	}
	if len(matches) != 1 {
		return Resolution{}, fmt.Errorf(
			"resolve gh account for %s/%s: prompt selected no unique authenticated account for login %q",
			host,
			owner,
			selected.Login,
		)
	}
	return r.resolveSelected(ctx, host, owner, matches[0])
}

func (r *Resolver) hasExplicitToken() bool {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if value, ok := r.lookupEnv(key); ok && value != "" {
			return true
		}
	}
	return false
}

func (r *Resolver) resolveSelected(
	ctx context.Context,
	host, owner string,
	account Account,
) (Resolution, error) {
	token, err := Token(ctx, r.runner, host, account.Login)
	if err != nil {
		return Resolution{}, fmt.Errorf(
			"resolve gh account for %s/%s: %w",
			host,
			owner,
			err,
		)
	}
	if token == "" {
		return Resolution{}, fmt.Errorf(
			"resolve gh account for %s/%s: account %q returned an empty token",
			host,
			owner,
			account.Login,
		)
	}
	if err := r.cache.Store(host, owner, account.Login); err != nil {
		return Resolution{}, fmt.Errorf(
			"resolve gh account for %s/%s: store selected account %q: %w",
			host,
			owner,
			account.Login,
			err,
		)
	}
	return Resolution{Source: SourceSelected, Login: account.Login, Token: token}, nil
}
