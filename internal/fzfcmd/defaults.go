package fzfcmd

import (
	"os"
	"strings"
	"unicode"
)

// appearanceOptions applies only display-oriented user defaults. Selection,
// output, and key-binding options remain under gh-qw's control.
func appearanceOptions() []string {
	var options []string
	if path := os.Getenv("FZF_DEFAULT_OPTS_FILE"); path != "" {
		// Ignore an unavailable optional file and retain the built-in appearance.
		if data, err := os.ReadFile(path); err == nil {
			options = append(options, parseAppearanceOptions(string(data))...)
		}
	}
	return append(options, parseAppearanceOptions(os.Getenv("FZF_DEFAULT_OPTS"))...)
}

// parseAppearanceOptions accepts only fzf visual flags. Quoted values are
// unwrapped without shell expansion; other flags cannot change the selection.
func parseAppearanceOptions(content string) []string {
	var result []string
	for _, rawLine := range strings.Split(content, "\n") {
		fields, ok := splitAppearanceWords(rawLine)
		if !ok {
			continue
		}
		for index := 0; index < len(fields); index++ {
			name, value, hasValue := strings.Cut(fields[index], "=")
			switch name {
			case "--cycle", "--no-cycle", "--no-border", "--no-height",
				"--no-color", "--bold", "--no-bold":
				if !hasValue {
					result = append(result, name)
				}
			case "--border", "--height", "--layout", "--color", "--info",
				"--margin", "--padding", "--pointer", "--marker",
				"--separator", "--scrollbar", "--style":
				if !hasValue && index+1 < len(fields) &&
					!strings.HasPrefix(fields[index+1], "-") {
					index++
					value = fields[index]
					hasValue = true
				}
				if name == "--border" && !hasValue {
					result = append(result, name)
				} else if hasValue && value != "" && !strings.ContainsAny(value, "\r\x00") {
					result = append(result, name+"="+value)
				}
			}
		}
	}
	return result
}

// splitAppearanceWords interprets quoting and escaping but never evaluates
// variables, commands, or other shell constructs. Invalid lines are skipped.
func splitAppearanceWords(line string) ([]string, bool) {
	var words []string
	var token strings.Builder
	var quote rune
	active := false
	escaped := false

scan:
	for _, char := range line {
		if escaped {
			token.WriteRune(char)
			escaped = false
			continue
		}
		if quote != 0 {
			switch {
			case char == quote:
				quote = 0
			case quote == '"' && char == '\\':
				escaped = true
			default:
				token.WriteRune(char)
			}
			continue
		}
		switch {
		case char == '#' && !active:
			break scan
		case char == '\\':
			escaped = true
			active = true
		case char == '\'' || char == '"':
			quote = char
			active = true
		case unicode.IsSpace(char):
			if active {
				words = append(words, token.String())
				token.Reset()
				active = false
			}
		default:
			token.WriteRune(char)
			active = true
		}
	}
	if quote != 0 || escaped {
		return nil, false
	}
	if active {
		words = append(words, token.String())
	}
	return words, true
}
