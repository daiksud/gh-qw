package fzfcmd

import (
	"os"
	"strings"
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

// parseAppearanceOptions accepts simple whitespace-separated fzf visual flags,
// without shell expansion. Unsupported flags cannot affect target selection.
func parseAppearanceOptions(content string) []string {
	var result []string
	for _, rawLine := range strings.Split(content, "\n") {
		fields := strings.Fields(rawLine)
		for index := 0; index < len(fields); index++ {
			token := fields[index]
			if strings.HasPrefix(token, "#") {
				break
			}
			name, value, hasValue := strings.Cut(token, "=")
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
					!strings.HasPrefix(fields[index+1], "-") &&
					!strings.HasPrefix(fields[index+1], "#") {
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
