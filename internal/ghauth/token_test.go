package ghauth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTokenReturnsRunnerOutputTrimmed(t *testing.T) {
	runner := &stubRunner{output: []byte("gho_abcdef1234567890\n")}

	token, err := Token(context.Background(), runner, "github.com", "daiksud")
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if token != "gho_abcdef1234567890" {
		t.Fatalf("Token() = %q, want trimmed token", token)
	}
	wantArgs := []string{"auth", "token", "--user", "daiksud", "--hostname", "github.com"}
	if !reflect.DeepEqual(runner.lastArgs(), wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.lastArgs(), wantArgs)
	}
}

func TestTokenRejectsEmptyLoginOrHost(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		login string
	}{
		{name: "empty host", host: "", login: "daiksud"},
		{name: "empty login", host: "github.com", login: ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &stubRunner{output: []byte("gho_should-not-be-read")}

			_, err := Token(context.Background(), runner, test.host, test.login)
			if err == nil {
				t.Fatal("Token() error = nil, want validation failure")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("Output() calls = %d, want 0 (validated before execution)", len(runner.calls))
			}
		})
	}
}

func TestTokenPropagatesRunnerFailureWithoutLeakingOutput(t *testing.T) {
	wantErr := errors.New("no such account")
	runner := &stubRunner{
		output: []byte("gho_super-secret-token"),
		err:    wantErr,
	}

	_, err := Token(context.Background(), runner, "github.com", "missing-user")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Token() error = %v, want wrapping %v", err, wantErr)
	}
	if strings.Contains(err.Error(), "gho_super-secret-token") {
		t.Fatalf("Token() error exposed captured output: %q", err)
	}
}
