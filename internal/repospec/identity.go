package repospec

import (
	"errors"
	"strings"
)

// Identity contains the canonical repository components, without clone
// transport details such as a username, port, or .git suffix.
type Identity struct {
	Host  string
	Owner string
	Repo  string
}

// String derives the canonical identity from its components.
func (i Identity) String() string {
	return strings.Join([]string{i.Host, i.Owner, i.Repo}, "/")
}

// ParseIdentity validates an already-canonical host/owner/repository identity.
// It performs no filesystem, authentication, or Git operations.
func ParseIdentity(value string) (Identity, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return Identity{}, errors.New("must contain exactly host, owner, and repository")
	}
	auth, err := parseAuthority(parts[0])
	if err != nil {
		return Identity{}, err
	}
	if auth.hostname != parts[0] || auth.port != "" {
		return Identity{}, errors.New("host must already be canonical and contain no port")
	}
	if err := ValidateOwner(parts[1]); err != nil {
		return Identity{}, err
	}
	if err := ValidateRepository(parts[2]); err != nil {
		return Identity{}, err
	}
	return Identity{Host: parts[0], Owner: parts[1], Repo: parts[2]}, nil
}

// ValidateOwner validates one gh-qw repository-layout owner component.
func ValidateOwner(owner string) error {
	return validatePathComponent("owner", owner)
}

// ValidateRepository validates one canonical gh-qw repository-layout name.
func ValidateRepository(repo string) error {
	if err := validatePathComponent("repository", repo); err != nil {
		return err
	}
	if strings.HasSuffix(repo, ".git") {
		return errors.New("repository must not contain a .git suffix")
	}
	return nil
}
