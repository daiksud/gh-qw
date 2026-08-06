package ghauth

import "context"

// stubRunner is an in-memory ghauth.Runner test double. ghauth's own tests
// exercise parsing and resolution logic against canned Runner output; gh
// subprocess execution itself is already covered by internal/ghcmd's
// helper-process tests, so ghauth does not need a second subprocess harness.
type stubRunner struct {
	output  []byte
	err     error
	calls   [][]string
	perCall []stubCall
}

type stubCall struct {
	output []byte
	err    error
}

func (r *stubRunner) Output(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(r.perCall) > 0 {
		call := r.perCall[0]
		r.perCall = r.perCall[1:]
		return call.output, call.err
	}
	return r.output, r.err
}

func (r *stubRunner) lastArgs() []string {
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}
