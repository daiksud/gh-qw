package ghauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// routingRunner is a Runner test double that answers `gh auth status` and
// `gh auth token` distinctly, so resolver tests can assert exactly which gh
// subcommands ran and in what order without depending on account.go's or
// token.go's own stub conventions.
type routingRunner struct {
	statusOutput []byte
	statusErr    error
	statusCalls  int

	tokens     map[string]tokenResult
	tokenCalls []string
}

type tokenResult struct {
	token string
	err   error
}

func (r *routingRunner) Output(_ context.Context, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		r.statusCalls++
		return r.statusOutput, r.statusErr
	}
	if len(args) >= 4 && args[0] == "auth" && args[1] == "token" {
		login := args[3]
		r.tokenCalls = append(r.tokenCalls, login)
		result, ok := r.tokens[login]
		if !ok {
			return nil, fmt.Errorf("unexpected token request for %q", login)
		}
		if result.err != nil {
			return nil, result.err
		}
		return []byte(result.token), nil
	}
	return nil, fmt.Errorf("unexpected gh invocation: %v", args)
}

func accountsJSON(accounts ...Account) []byte {
	entries := make([]string, len(accounts))
	for i, a := range accounts {
		entries[i] = fmt.Sprintf(
			`{"state":"success","active":%v,"host":"github.com","login":%q}`,
			a.Active, a.Login,
		)
	}
	return []byte(fmt.Sprintf(`{"hosts":{"github.com":[%s]}}`, strings.Join(entries, ",")))
}

type fakeCache struct {
	mappings  map[string]string
	stored    []string
	deleted   []string
	lookupErr error
	storeErr  error
	deleteErr error
}

func newFakeCache() *fakeCache {
	return &fakeCache{mappings: make(map[string]string)}
}

func (c *fakeCache) key(host, owner string) string {
	return strings.ToLower(host) + "/" + strings.ToLower(owner)
}

func (c *fakeCache) Lookup(host, owner string) (string, bool, error) {
	if c.lookupErr != nil {
		return "", false, c.lookupErr
	}
	login, ok := c.mappings[c.key(host, owner)]
	return login, ok, nil
}

func (c *fakeCache) Store(host, owner, login string) error {
	if c.storeErr != nil {
		return c.storeErr
	}
	c.stored = append(c.stored, login)
	c.mappings[c.key(host, owner)] = login
	return nil
}

func (c *fakeCache) Delete(host, owner string) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	key := c.key(host, owner)
	c.deleted = append(c.deleted, key)
	delete(c.mappings, key)
	return nil
}

func neverPrompt(context.Context, io.Writer, string, string, []Account) (Account, error) {
	return Account{}, errors.New("prompt must not be called")
}

func TestResolveSkipsAutomaticSelectionWhenGHTokenIsExplicitlySet(t *testing.T) {
	runner := &routingRunner{}
	cache := newFakeCache()
	cache.lookupErr = errors.New("cache must not be read")
	resolver := NewResolver(ResolverOptions{
		Runner: runner,
		Cache:  cache,
		Prompt: neverPrompt,
		LookupEnv: func(key string) (string, bool) {
			if key == "GH_TOKEN" {
				return "gho_explicit", true
			}
			return "", false
		},
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "daiksud")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Source != SourceExplicitEnv || resolution.Token != "" || resolution.Login != "" {
		t.Fatalf("Resolve() = %#v, want SourceExplicitEnv with no token or login", resolution)
	}
	if runner.statusCalls != 0 || len(runner.tokenCalls) != 0 {
		t.Fatalf("gh was invoked despite an explicit token: statusCalls=%d tokenCalls=%v", runner.statusCalls, runner.tokenCalls)
	}
}

func TestResolveSkipsAutomaticSelectionWhenGITHUBTokenIsExplicitlySet(t *testing.T) {
	runner := &routingRunner{}
	resolver := NewResolver(ResolverOptions{
		Runner: runner,
		Cache:  newFakeCache(),
		Prompt: neverPrompt,
		LookupEnv: func(key string) (string, bool) {
			if key == "GITHUB_TOKEN" {
				return "ghp_explicit", true
			}
			return "", false
		},
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "daiksud")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Source != SourceExplicitEnv {
		t.Fatalf("Resolve() Source = %v, want SourceExplicitEnv", resolution.Source)
	}
}

func TestResolveUsesCachedLoginViaFastPath(t *testing.T) {
	runner := &routingRunner{tokens: map[string]tokenResult{
		"daiksud": {token: "gho_cached"},
	}}
	cache := newFakeCache()
	_ = cache.Store("github.com", "daiksud", "daiksud")
	resolver := NewResolver(ResolverOptions{
		Runner:    runner,
		Cache:     cache,
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "daiksud")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Resolution{Source: SourceSelected, Login: "daiksud", Token: "gho_cached"}
	if resolution != want {
		t.Fatalf("Resolve() = %#v, want %#v", resolution, want)
	}
	if runner.statusCalls != 0 {
		t.Fatalf("statusCalls = %d, want 0 (cache hit must skip gh auth status)", runner.statusCalls)
	}
}

func TestResolveFallsBackWhenCachedLoginTokenFails(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "daiksud"}),
		tokens: map[string]tokenResult{
			"stale-login": {err: errors.New("account removed")},
			"daiksud":     {token: "gho_fresh"},
		},
	}
	cache := newFakeCache()
	cache.mappings[cache.key("github.com", "daiksud")] = "stale-login"
	resolver := NewResolver(ResolverOptions{
		Runner:    runner,
		Cache:     cache,
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "daiksud")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Resolution{Source: SourceSelected, Login: "daiksud", Token: "gho_fresh"}
	if resolution != want {
		t.Fatalf("Resolve() = %#v, want %#v", resolution, want)
	}
	if len(cache.deleted) != 1 {
		t.Fatalf("cache.deleted = %v, want the stale mapping removed", cache.deleted)
	}
	if len(cache.stored) != 1 || cache.stored[0] != "daiksud" {
		t.Fatalf("cache.stored = %v, want [daiksud]", cache.stored)
	}
}

func TestResolveStopsWhenStaleCacheDeletionFails(t *testing.T) {
	runner := &routingRunner{
		tokens: map[string]tokenResult{
			"stale-login": {err: errors.New("account removed")},
		},
	}
	cache := newFakeCache()
	cache.mappings[cache.key("github.com", "daiksud")] = "stale-login"
	cache.deleteErr = errors.New("disk full")
	resolver := NewResolver(ResolverOptions{
		Runner:    runner,
		Cache:     cache,
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "daiksud"); err == nil {
		t.Fatal("Resolve() error = nil, want stale cache deletion failure")
	}
	if runner.statusCalls != 0 {
		t.Fatalf("statusCalls = %d, want 0 when stale cache deletion did not persist", runner.statusCalls)
	}
}

func TestResolvePropagatesCacheLookupFailure(t *testing.T) {
	cache := newFakeCache()
	cache.lookupErr = errors.New("permission denied")
	runner := &routingRunner{}
	resolver := NewResolver(ResolverOptions{
		Runner:    runner,
		Cache:     cache,
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want cache lookup failure")
	}
	if runner.statusCalls != 0 || len(runner.tokenCalls) != 0 {
		t.Fatalf("gh calls occurred after cache failure: status=%d token=%v", runner.statusCalls, runner.tokenCalls)
	}
}

func TestResolveMatchesOwnerCaseInsensitivelyAndStoresCache(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "Acme-Bot"}),
		tokens: map[string]tokenResult{
			"Acme-Bot": {token: "gho_matched"},
		},
	}
	cache := newFakeCache()
	resolver := NewResolver(ResolverOptions{
		Runner:    runner,
		Cache:     cache,
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "acme-bot")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Resolution{Source: SourceSelected, Login: "Acme-Bot", Token: "gho_matched"}
	if resolution != want {
		t.Fatalf("Resolve() = %#v, want %#v", resolution, want)
	}
	if len(cache.stored) != 1 || cache.stored[0] != "Acme-Bot" {
		t.Fatalf("cache.stored = %v, want [Acme-Bot]", cache.stored)
	}
}

func TestResolvePropagatesCacheStoreFailure(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "acme"}),
		tokens: map[string]tokenResult{
			"acme": {token: "gho_acme"},
		},
	}
	cache := newFakeCache()
	cache.storeErr = errors.New("read-only filesystem")
	resolver := NewResolver(ResolverOptions{
		Runner:    runner,
		Cache:     cache,
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want cache store failure")
	}
}

func TestResolveFailsWhenNoAccountsAreAuthenticated(t *testing.T) {
	resolver := NewResolver(ResolverOptions{
		Runner:     &routingRunner{statusOutput: accountsJSON()},
		Cache:      newFakeCache(),
		Prompt:     neverPrompt,
		IsTerminal: func(io.Reader) bool { return true },
		LookupEnv:  func(string) (string, bool) { return "", false },
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want no-account failure")
	}
}

func TestResolveUsesSoleAuthenticatedAccount(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}),
		tokens: map[string]tokenResult{
			"TE-DaikiSudo": {token: "gho_sole"},
		},
	}
	cache := newFakeCache()
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      cache,
		Prompt:     neverPrompt,
		IsTerminal: func(io.Reader) bool { return false },
		LookupEnv:  func(string) (string, bool) { return "", false },
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "acme")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Resolution{Source: SourceSelected, Login: "TE-DaikiSudo", Token: "gho_sole"}
	if resolution != want {
		t.Fatalf("Resolve() = %#v, want %#v", resolution, want)
	}
}

func TestResolveFailsWhenNonInteractiveAndMultipleAccountsHaveNoOwnerMatch(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "daiksud"}),
	}
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      newFakeCache(),
		Prompt:     neverPrompt,
		IsTerminal: func(io.Reader) bool { return false },
		LookupEnv:  func(string) (string, bool) { return "", false },
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want non-interactive ambiguity failure")
	}
	if len(runner.tokenCalls) != 0 {
		t.Fatalf("tokenCalls = %v, want none for an ambiguous resolution", runner.tokenCalls)
	}
}

func TestResolvePromptsWhenInteractiveAndNoMatch(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "daiksud"}),
		tokens: map[string]tokenResult{
			"daiksud": {token: "gho_prompted"},
		},
	}
	cache := newFakeCache()
	var promptedHost, promptedOwner string
	var promptedAccounts []Account
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      cache,
		IsTerminal: func(io.Reader) bool { return true },
		LookupEnv:  func(string) (string, bool) { return "", false },
		Prompt: func(_ context.Context, _ io.Writer, host, owner string, accounts []Account) (Account, error) {
			promptedHost, promptedOwner = host, owner
			promptedAccounts = accounts
			return Account{Login: "daiksud"}, nil
		},
	})

	resolution, err := resolver.Resolve(context.Background(), "github.com", "acme")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Resolution{Source: SourceSelected, Login: "daiksud", Token: "gho_prompted"}
	if resolution != want {
		t.Fatalf("Resolve() = %#v, want %#v", resolution, want)
	}
	if promptedHost != "github.com" || promptedOwner != "acme" {
		t.Fatalf("prompt was called with (%q, %q), want (github.com, acme)", promptedHost, promptedOwner)
	}
	if len(promptedAccounts) != 2 {
		t.Fatalf("prompt received %d accounts, want 2", len(promptedAccounts))
	}
	if len(cache.stored) != 1 || cache.stored[0] != "daiksud" {
		t.Fatalf("cache.stored = %v, want [daiksud]", cache.stored)
	}
}

func TestResolveFailsWhenPromptFails(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "daiksud"}),
	}
	cache := newFakeCache()
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      cache,
		IsTerminal: func(io.Reader) bool { return true },
		LookupEnv:  func(string) (string, bool) { return "", false },
		Prompt: func(context.Context, io.Writer, string, string, []Account) (Account, error) {
			return Account{}, errors.New("user cancelled")
		},
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want prompt failure")
	}
	if len(cache.stored) != 0 {
		t.Fatalf("cache.stored = %v, want none after a cancelled prompt", cache.stored)
	}
}

func TestResolveFailsWhenPromptDoesNotSelectAListedAccount(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "daiksud"}),
	}
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      newFakeCache(),
		IsTerminal: func(io.Reader) bool { return true },
		LookupEnv:  func(string) (string, bool) { return "", false },
		Prompt: func(context.Context, io.Writer, string, string, []Account) (Account, error) {
			return Account{Login: "unknown"}, nil
		},
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want invalid prompt selection failure")
	}
	if len(runner.tokenCalls) != 0 {
		t.Fatalf("tokenCalls = %v, want none for an invalid prompt selection", runner.tokenCalls)
	}
}

func TestResolveFailsWhenTokenLookupFailsAfterPromptSelection(t *testing.T) {
	runner := &routingRunner{
		statusOutput: accountsJSON(Account{Login: "TE-DaikiSudo", Active: true}, Account{Login: "daiksud"}),
		tokens: map[string]tokenResult{
			"daiksud": {err: errors.New("keyring locked")},
		},
	}
	cache := newFakeCache()
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      cache,
		IsTerminal: func(io.Reader) bool { return true },
		LookupEnv:  func(string) (string, bool) { return "", false },
		Prompt: func(context.Context, io.Writer, string, string, []Account) (Account, error) {
			return Account{Login: "daiksud"}, nil
		},
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want token lookup failure")
	}
	if len(cache.stored) != 0 {
		t.Fatalf("cache.stored = %v, want none when the token lookup fails", cache.stored)
	}
}

func TestResolveFailsWhenListAccountsFails(t *testing.T) {
	runner := &routingRunner{statusErr: errors.New("gh not authenticated")}
	resolver := NewResolver(ResolverOptions{
		Runner:     runner,
		Cache:      newFakeCache(),
		Prompt:     neverPrompt,
		IsTerminal: func(io.Reader) bool { return true },
		LookupEnv:  func(string) (string, bool) { return "", false },
	})

	if _, err := resolver.Resolve(context.Background(), "github.com", "acme"); err == nil {
		t.Fatal("Resolve() error = nil, want account listing failure")
	}
}

func TestResolveRejectsEmptyHostOrOwner(t *testing.T) {
	resolver := NewResolver(ResolverOptions{
		Runner:    &routingRunner{},
		Cache:     newFakeCache(),
		Prompt:    neverPrompt,
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	tests := []struct{ host, owner string }{
		{host: "", owner: "acme"},
		{host: "github.com", owner: ""},
	}
	for _, test := range tests {
		if _, err := resolver.Resolve(context.Background(), test.host, test.owner); err == nil {
			t.Fatalf("Resolve(%q, %q) error = nil, want validation failure", test.host, test.owner)
		}
	}
}
