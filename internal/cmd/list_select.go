package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/daiksud/gh-qw/internal/fzfcmd"
	"golang.org/x/term"
)

func listIsTerminal(output io.Writer) bool {
	descriptor, ok := output.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(descriptor.Fd()))
}

func listSelector(stderr io.Writer) func(context.Context, []string, string) (string, error) {
	return func(ctx context.Context, rows []string, query string) (string, error) {
		return (&fzfcmd.Selector{
			Stderr: stderr,
			Prompt: "List> ",
			Header: "Enter: select; Esc: cancel.",
			Query:  query,
		}).Select(ctx, rows)
	}
}

func listSelectOutput(ctx context.Context, entries []listEntry, query string, fullPath, unique bool, selectRow func(context.Context, []string, string) (string, error)) (string, error) {
	outputs, err := listOutputLines(entries, fullPath, unique)
	if err != nil {
		return "", err
	}
	paths, err := listOutputLines(entries, true, false)
	if err != nil {
		return "", err
	}
	rows := make([]string, len(entries))
	results := make(map[string]string, len(entries))
	for index, entry := range entries {
		kind := "repository"
		if entry.slot != "" {
			kind = "worktree"
		}
		display := entry.displayIdentity()
		// Keep paths literal and visible even when they contain terminal controls.
		fields := []string{kind, display, fmt.Sprintf("%q", paths[index])}
		identities := map[string]bool{display: true}
		aliases := []string{entry.identity}
		if entry.slot != "" && entry.branch != "" {
			aliases = append(aliases, entry.host+"/"+entry.owner+"/"+entry.repo+"@"+entry.branch)
		}
		for _, identity := range aliases {
			if !identities[identity] {
				fields = append(fields, identity)
				identities[identity] = true
			}
		}
		row := strings.Join(fields, "\t")
		if _, exists := results[row]; exists {
			return "", errors.New("list selection candidates must be distinct")
		}
		rows[index] = row
		results[row] = outputs[index]
	}
	sort.Strings(rows)
	selected, err := selectRow(ctx, rows, query)
	if err != nil {
		switch {
		case errors.Is(err, fzfcmd.ErrCanceled):
			return "", newSilentStatusError(130)
		case errors.Is(err, fzfcmd.ErrNoMatch):
			return "", newSilentStatusError(1)
		default:
			return "", fmt.Errorf("select list result: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	output, offered := results[selected]
	if !offered {
		return "", errors.New("selected list result was not offered")
	}
	return output, nil
}
