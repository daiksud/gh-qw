package gitcmd

import (
	"bytes"
	"fmt"
)

// ParseLine validates a nonempty single-line Git response, removing at most one
// trailing LF and CR. It preserves whitespace and never interprets the value.
func ParseLine(output []byte, description string) (string, error) {
	output = bytes.TrimSuffix(output, []byte{'\n'})
	output = bytes.TrimSuffix(output, []byte{'\r'})
	if len(output) == 0 {
		return "", fmt.Errorf("Git returned an empty %s", description)
	}
	if bytes.IndexByte(output, 0) >= 0 ||
		bytes.IndexByte(output, '\n') >= 0 ||
		bytes.IndexByte(output, '\r') >= 0 {
		return "", fmt.Errorf("Git returned a non-single-line %s", description)
	}
	return string(output), nil
}
