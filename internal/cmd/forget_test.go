package cmd

import (
	"io"
	"testing"
)

func TestNewForgetCommandRejectsArgumentCountAsUsage(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "missing"},
		{name: "extra", args: []string{"one", "two"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command := NewForgetCommand(nil, io.Discard)
			command.SetArgs(test.args)
			err := command.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("Execute() error = %v, exit = %d; want usage exit 2", err, ExitCode(err))
			}
		})
	}
}
