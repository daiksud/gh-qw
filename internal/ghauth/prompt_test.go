package ghauth

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestPromptAccountSelectionDefaultsToActiveOnEmptyInput(t *testing.T) {
	accounts := []Account{
		{Login: "daiksud", Active: false},
		{Login: "TE-DaikiSudo", Active: true},
	}
	var output bytes.Buffer
	input := bufio.NewReader(strings.NewReader("\n"))

	got, err := promptAccountSelection(&output, input, "github.com", "acme", accounts)
	if err != nil {
		t.Fatalf("promptAccountSelection() error = %v", err)
	}
	if got.Login != "TE-DaikiSudo" {
		t.Fatalf("selected = %#v, want the active account listed first as the default", got)
	}
	if !strings.Contains(output.String(), "TE-DaikiSudo (active)") {
		t.Fatalf("prompt output = %q, want the active account annotated", output.String())
	}
}

func TestPromptAccountSelectionAcceptsAnExplicitNumber(t *testing.T) {
	accounts := []Account{
		{Login: "TE-DaikiSudo", Active: true},
		{Login: "daiksud", Active: false},
	}
	var output bytes.Buffer
	input := bufio.NewReader(strings.NewReader("2\n"))

	got, err := promptAccountSelection(&output, input, "github.com", "acme", accounts)
	if err != nil {
		t.Fatalf("promptAccountSelection() error = %v", err)
	}
	if got.Login != "daiksud" {
		t.Fatalf("selected = %#v, want daiksud", got)
	}
}

func TestPromptAccountSelectionRepromptsOnInvalidInput(t *testing.T) {
	accounts := []Account{
		{Login: "TE-DaikiSudo", Active: true},
		{Login: "daiksud", Active: false},
	}
	var output bytes.Buffer
	input := bufio.NewReader(strings.NewReader("nonsense\n99\n2\n"))

	got, err := promptAccountSelection(&output, input, "github.com", "acme", accounts)
	if err != nil {
		t.Fatalf("promptAccountSelection() error = %v", err)
	}
	if got.Login != "daiksud" {
		t.Fatalf("selected = %#v, want daiksud after invalid attempts", got)
	}
	if strings.Count(output.String(), "enter a number") < 2 {
		t.Fatalf("prompt output = %q, want a re-prompt for each invalid attempt", output.String())
	}
}

func TestPromptAccountSelectionRejectsExhaustedInputWithoutLooping(t *testing.T) {
	accounts := []Account{{Login: "daiksud", Active: true}}
	var output bytes.Buffer
	// No trailing newline: the reader reports io.EOF together with the final
	// (invalid) line, and there is nothing further to read.
	input := bufio.NewReader(strings.NewReader("nonsense"))

	_, err := promptAccountSelection(&output, input, "github.com", "acme", accounts)
	if err == nil {
		t.Fatal("promptAccountSelection() error = nil, want a failure instead of looping forever")
	}
}

func TestPromptAccountSelectionRequiresAtLeastOneAccount(t *testing.T) {
	var output bytes.Buffer
	input := bufio.NewReader(strings.NewReader("\n"))

	_, err := promptAccountSelection(&output, input, "github.com", "acme", nil)
	if err == nil {
		t.Fatal("promptAccountSelection() error = nil, want a failure for an empty account list")
	}
}

func TestOrderActiveFirstMovesTheActiveAccountToTheFront(t *testing.T) {
	accounts := []Account{
		{Login: "daiksud", Active: false},
		{Login: "acme-bot", Active: false},
		{Login: "TE-DaikiSudo", Active: true},
	}

	ordered := orderActiveFirst(accounts)

	want := []Account{
		{Login: "TE-DaikiSudo", Active: true},
		{Login: "daiksud", Active: false},
		{Login: "acme-bot", Active: false},
	}
	if len(ordered) != len(want) {
		t.Fatalf("orderActiveFirst() = %#v, want %#v", ordered, want)
	}
	for i := range want {
		if ordered[i] != want[i] {
			t.Fatalf("orderActiveFirst()[%d] = %#v, want %#v", i, ordered[i], want[i])
		}
	}
}
