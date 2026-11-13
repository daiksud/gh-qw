// Package testregistry provides isolated location registries for command tests.
package testregistry

import (
	"path/filepath"
	"testing"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/registry"
)

type Registry struct {
	*registry.Store
	Read       func() (map[string]string, error)
	OnRegister func(string, string) error
	OnForgetAt func(string, string) error
}

func New(t testing.TB, repositories ...local.Repository) *Registry {
	t.Helper()
	store, err := registry.NewStoreAt(filepath.Join(t.TempDir(), "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, repository := range repositories {
		if err := store.Register(repository.Identity, repository.Path); err != nil {
			t.Fatal(err)
		}
	}
	return &Registry{Store: store}
}

func Locations(repositories ...local.Repository) map[string]string {
	result := make(map[string]string, len(repositories))
	for _, repository := range repositories {
		result[repository.Identity] = repository.Path
	}
	return result
}

func (store *Registry) List() (map[string]string, error) {
	if store.Read != nil {
		return store.Read()
	}
	return store.Store.List()
}

func (store *Registry) Register(identity, path string) error {
	if store.OnRegister != nil {
		return store.OnRegister(identity, path)
	}
	return store.Store.Register(identity, path)
}

func (store *Registry) ForgetAt(identity, path string) error {
	if store.OnForgetAt != nil {
		return store.OnForgetAt(identity, path)
	}
	return store.Store.ForgetAt(identity, path)
}

// From supplies a synthetic inventory and mutation hooks for command-boundary tests.
func From(read func() (map[string]string, error)) *Registry {
	return &Registry{Read: read, OnRegister: func(string, string) error { return nil }, OnForgetAt: func(string, string) error { return nil }}
}
