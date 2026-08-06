package ghcmd

import "fmt"

// ResolveExecutable locates the gh executable. A non-empty GH_PATH (as
// reported by lookupEnv) takes precedence, matching gh's own documented
// override for environments where gh cannot determine its own path.
// Otherwise, lookPath searches PATH for "gh". A caller running as a gh
// extension can rely on "gh" always being resolvable in PATH; a failure here
// is reported explicitly rather than silently skipping gh integration.
func ResolveExecutable(
	lookupEnv func(string) (string, bool),
	lookPath func(string) (string, error),
) (string, error) {
	if value, ok := lookupEnv("GH_PATH"); ok && value != "" {
		return value, nil
	}

	path, err := lookPath("gh")
	if err != nil {
		return "", fmt.Errorf("resolve gh executable: %w", err)
	}
	return path, nil
}
