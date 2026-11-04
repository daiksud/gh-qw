package fzfcmd

import (
	"reflect"
	"testing"
)

func TestParseAppearanceOptionsKeepsOnlyDisplayFlags(t *testing.T) {
	input := `# A user's fzf settings
--height=~40%
--layout 'reverse'
--border="rounded" --cycle
--color='fg:#abcdef' --padding 1,2
--filter=first --select-1 --exit-0 --multi --print-query
--bind 'start:accept' --expect=ctrl-a --preview 'cat {}'
--read0 --print0 --query=first
--no-cycle # Allowed display override
`
	want := []string{
		"--height=~40%", "--layout=reverse", "--border=rounded", "--cycle",
		"--color=fg:#abcdef", "--padding=1,2", "--no-cycle",
	}
	if got := parseAppearanceOptions(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("appearance options = %#v, want %#v", got, want)
	}
}

func TestParseAppearanceOptionsRejectsInvalidValues(t *testing.T) {
	input := "--layout= --height --filter=unexpected --cycle=true --border --info=inline\x00x"
	want := []string{"--border"}
	if got := parseAppearanceOptions(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("appearance options = %#v, want %#v", got, want)
	}
}

func TestParseAppearanceOptionsSkipsBrokenQuoting(t *testing.T) {
	input := "--layout='reverse\n--height=~40%\n--border=rounded --bind=start:accept"
	want := []string{"--height=~40%", "--border=rounded"}
	if got := parseAppearanceOptions(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("appearance options = %#v, want %#v", got, want)
	}
}
