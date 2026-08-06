package cmd

import (
	"fmt"
	"os"

	"github.com/daiksud/gh-qw/internal/ghauth"
)

// accountFailureHint returns a human-readable note to append to a gh
// command failure, naming the account gh-qw resolved for the operation so a
// multi-account environment's misconfiguration is diagnosable without
// re-running gh commands by hand. It returns "" for an explicit environment
// token, whose account gh-qw never inspects, or when no selected login exists.
func accountFailureHint(resolution ghauth.Resolution) string {
	if resolution.Source != ghauth.SourceSelected || resolution.Login == "" {
		return ""
	}

	login := resolution.Login
	hint := fmt.Sprintf("used gh account %q", login)
	if path, err := ghauth.DefaultCachePath(os.LookupEnv, os.UserHomeDir); err == nil {
		hint += fmt.Sprintf(
			"; run \"gh auth switch\" or delete the account cache at %s to choose another account",
			path,
		)
	} else {
		hint += "; run \"gh auth switch\" to choose another account"
	}
	return hint
}

// wrapAccountFailureHint appends accountFailureHint's note to err in
// parentheses, preserving err in the wrapped chain (so errors.Is/As and
// StderrOutput() extraction such as getPreserveSilentGhError's continue to
// work). A nil err or an empty hint returns err unchanged.
func wrapAccountFailureHint(err error, resolution ghauth.Resolution) error {
	if err == nil {
		return nil
	}
	hint := accountFailureHint(resolution)
	if hint == "" {
		return err
	}
	return fmt.Errorf("%w (%s)", err, hint)
}
