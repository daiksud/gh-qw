package gitcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidRef means Git rejected a literal ref name.
var ErrInvalidRef = errors.New("invalid Git ref name")

// CheckRefFormat asks Git to validate a literal, possibly one-level ref name.
// Unlike --branch, --allow-onelevel never expands checkout-history syntax.
// The input is never replaced with command output.
func (r *Runner) CheckRefFormat(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// check-ref-format has no -- separator. Keep an operand out of its
	// option position, including options that would change validation.
	if strings.HasPrefix(name, "-") {
		return ErrInvalidRef
	}

	var quiet Runner
	if r != nil {
		quiet = *r
	}
	quiet.Stdout = io.Discard
	quiet.Stderr = io.Discard
	err := quiet.Run(ctx, "check-ref-format", "--allow-onelevel", name)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if code, ok := CommandExitCode(err); ok && code == 1 {
		return ErrInvalidRef
	}
	if err != nil {
		return fmt.Errorf("validate Git ref: %w", err)
	}
	return nil
}
