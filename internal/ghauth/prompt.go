package ghauth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Prompter asks the user to choose one of accounts for host/owner when
// automatic selection cannot determine one on its own.
type Prompter func(ctx context.Context, output io.Writer, host, owner string, accounts []Account) (Account, error)

// TerminalPrompt is the default Prompter. It opens the controlling terminal
// directly, mirroring the repository's existing confirmation-prompt pattern
// (internal/cmd migrate/rm), so a caller's own stdin — which may be piped,
// non-interactive input meant for something else — is never consumed.
func TerminalPrompt(
	ctx context.Context,
	output io.Writer,
	host, owner string,
	accounts []Account,
) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	terminalPath := "/dev/tty"
	if runtime.GOOS == "windows" {
		terminalPath = "CONIN$"
	}
	terminal, err := os.Open(terminalPath)
	if err != nil {
		return Account{}, fmt.Errorf("open controlling terminal: %w", err)
	}
	defer terminal.Close()

	return promptAccountSelection(output, bufio.NewReader(terminal), host, owner, accounts)
}

// promptAccountSelection is TerminalPrompt's terminal-agnostic core, kept
// separate so its selection and re-prompt logic is unit-testable without a
// real controlling terminal.
func promptAccountSelection(
	output io.Writer,
	input *bufio.Reader,
	host, owner string,
	accounts []Account,
) (Account, error) {
	if len(accounts) == 0 {
		return Account{}, fmt.Errorf(
			"choose a gh account for %s/%s: no gh accounts are available",
			host, owner,
		)
	}

	ordered := orderActiveFirst(accounts)
	if _, err := fmt.Fprintf(
		output,
		"gh-qw: multiple gh accounts can access %s; choose one to use for %s/%s:\n",
		host, host, owner,
	); err != nil {
		return Account{}, err
	}
	for i, account := range ordered {
		suffix := ""
		if account.Active {
			suffix = " (active)"
		}
		if _, err := fmt.Fprintf(output, "  %d) %s%s\n", i+1, account.Login, suffix); err != nil {
			return Account{}, err
		}
	}

	for {
		if _, err := fmt.Fprint(output, "Account [1]: "); err != nil {
			return Account{}, err
		}
		line, readErr := input.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Account{}, fmt.Errorf("read controlling terminal: %w", readErr)
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return ordered[0], nil
		}
		if index, convErr := strconv.Atoi(trimmed); convErr == nil && index >= 1 && index <= len(ordered) {
			return ordered[index-1], nil
		}
		if errors.Is(readErr, io.EOF) {
			return Account{}, fmt.Errorf(
				"choose a gh account for %s/%s: no valid selection was entered",
				host, owner,
			)
		}
		if _, err := fmt.Fprintf(output, "gh-qw: enter a number from 1 to %d\n", len(ordered)); err != nil {
			return Account{}, err
		}
	}
}

// orderActiveFirst returns accounts with any active account moved to the
// front, so the default (empty-input) selection is the active account when
// one exists.
func orderActiveFirst(accounts []Account) []Account {
	ordered := make([]Account, 0, len(accounts))
	var rest []Account
	for _, account := range accounts {
		if account.Active {
			ordered = append(ordered, account)
		} else {
			rest = append(rest, account)
		}
	}
	return append(ordered, rest...)
}
