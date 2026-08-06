package local

import (
	"fmt"
	"strings"
)

// Selector is one exact local repository selector.
type Selector struct {
	Raw   string
	Host  string
	Owner string
	Repo  string
	Depth int
}

// ParseSelector accepts only <repo>, <owner>/<repo>, and
// <host>/<owner>/<repo>.
func ParseSelector(value string) (Selector, error) {
	fail := func(reason string) (Selector, error) {
		return Selector{}, &SelectorError{
			Selector: value,
			Kind:     SelectorInvalid,
			Reason:   reason,
		}
	}

	if value == "" {
		return fail("selector is empty")
	}
	if strings.TrimSpace(value) != value {
		return fail("selector has leading or trailing whitespace")
	}
	if strings.Contains(value, `\`) ||
		strings.Contains(value, "://") ||
		strings.HasPrefix(value, ".") {
		return fail("filesystem paths and URLs are not accepted")
	}

	components := strings.Split(value, "/")
	switch len(components) {
	case 1:
		parts, err := ParseIdentity("github.com/gh-qw/" + components[0])
		if err != nil {
			return fail(parserReason(err))
		}
		return Selector{Raw: value, Repo: parts.Repo, Depth: 1}, nil
	case 2:
		parts, err := ParseIdentity("github.com/" + value)
		if err != nil {
			return fail(parserReason(err))
		}
		return Selector{
			Raw:   value,
			Owner: parts.Owner,
			Repo:  parts.Repo,
			Depth: 2,
		}, nil
	case 3:
		parts, err := ParseIdentity(value)
		if err != nil {
			return fail(parserReason(err))
		}
		return Selector{
			Raw:   value,
			Host:  parts.Host,
			Owner: parts.Owner,
			Repo:  parts.Repo,
			Depth: 3,
		}, nil
	default:
		return fail("must be <repo>, <owner>/<repo>, or <host>/<owner>/<repo>")
	}
}

// ResolveRepository resolves an exact selector against every supplied record.
// Duplicate full identities remain ambiguous, making this suitable for
// destructive operations.
func ResolveRepository(repositories []Repository, selector string) (Repository, error) {
	parsed, err := ParseSelector(selector)
	if err != nil {
		return Repository{}, err
	}

	matches := make([]Repository, 0, 1)
	for _, repository := range repositories {
		if selectorMatches(repository, parsed) {
			matches = append(matches, repository)
		}
	}

	switch len(matches) {
	case 0:
		return Repository{}, &SelectorError{
			Selector: selector,
			Kind:     SelectorNotFound,
		}
	case 1:
		return matches[0], nil
	default:
		return Repository{}, &SelectorError{
			Selector: selector,
			Kind:     SelectorAmbiguous,
			Matches:  append([]Repository(nil), matches...),
		}
	}
}

// ResolveRepositoryForMutation is the destructive-selection spelling of
// ResolveRepository.
func ResolveRepositoryForMutation(repositories []Repository, selector string) (Repository, error) {
	return ResolveRepository(repositories, selector)
}

// ResolveEarliestRepository resolves after retaining only the earliest record
// for each canonical identity.
func ResolveEarliestRepository(repositories []Repository, selector string) (Repository, error) {
	return ResolveRepository(EarliestByIdentity(repositories), selector)
}

// EarliestByIdentity returns the first record for each canonical identity
// while preserving input order.
func EarliestByIdentity(repositories []Repository) []Repository {
	seen := make(map[string]struct{}, len(repositories))
	unique := make([]Repository, 0, len(repositories))
	for _, repository := range repositories {
		if _, exists := seen[repository.Identity]; exists {
			continue
		}
		seen[repository.Identity] = struct{}{}
		unique = append(unique, repository)
	}
	return unique
}

// FindEarliestIdentity returns the first record with the exact canonical
// identity.
func FindEarliestIdentity(repositories []Repository, identity string) (Repository, bool, error) {
	parts, err := ParseIdentity(identity)
	if err != nil {
		return Repository{}, false, err
	}
	for _, repository := range repositories {
		if repository.Identity == parts.Identity {
			return repository, true, nil
		}
	}
	return Repository{}, false, nil
}

func selectorMatches(repository Repository, selector Selector) bool {
	switch selector.Depth {
	case 1:
		return repository.Repo == selector.Repo
	case 2:
		return repository.Owner == selector.Owner &&
			repository.Repo == selector.Repo
	case 3:
		return repository.Identity == fmt.Sprintf(
			"%s/%s/%s",
			selector.Host,
			selector.Owner,
			selector.Repo,
		)
	default:
		return false
	}
}
