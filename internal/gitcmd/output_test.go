package gitcmd_test

import (
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestParseLinePreservesGitResponseContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, response, want, wantError string
	}{
		{name: "unterminated", response: "true", want: "true"},
		{name: "LF", response: "value\n", want: "value"},
		{name: "CRLF", response: "value\r\n", want: "value"},
		{name: "CR", response: "value\r", want: "value"},
		{name: "spaces", response: " path with spaces \n", want: " path with spaces "},
		{name: "only whitespace", response: " \n", want: " "},
		{name: "empty", wantError: "Git returned an empty response"},
		{name: "empty LF", response: "\n", wantError: "Git returned an empty response"},
		{name: "empty CRLF", response: "\r\n", wantError: "Git returned an empty response"},
		{name: "multiple lines", response: "a\nb\n", wantError: "Git returned a non-single-line response"},
		{name: "extra LF", response: "value\n\n", wantError: "Git returned a non-single-line response"},
		{name: "extra CR", response: "value\r\r\n", wantError: "Git returned a non-single-line response"},
		{name: "embedded CR", response: "a\rb", wantError: "Git returned a non-single-line response"},
		{name: "NUL", response: "value\x00\n", wantError: "Git returned a non-single-line response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := gitcmd.ParseLine([]byte(test.response), "response")
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError || got != "" {
					t.Fatalf("ParseLine() = (%q, %v), want empty value and %q", got, err, test.wantError)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ParseLine() = (%q, %v), want (%q, nil)", got, err, test.want)
			}
		})
	}
}
