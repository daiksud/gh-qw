package ghauth

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestListAccountsParsesSuccessfulEntriesAndDedupesByLogin(t *testing.T) {
	runner := &stubRunner{output: []byte(`{"hosts":{"github.com":[
		{"state":"success","active":true,"host":"github.com","login":"daiksud","tokenSource":"GH_TOKEN"},
		{"state":"success","active":false,"host":"github.com","login":"daiksud","tokenSource":"keyring"},
		{"state":"success","active":false,"host":"github.com","login":"TE-DaikiSudo","tokenSource":"keyring"}
	]}}`)}

	accounts, err := ListAccounts(context.Background(), runner, "github.com")
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	want := []Account{
		{Login: "daiksud", Active: true},
		{Login: "TE-DaikiSudo", Active: false},
	}
	if !reflect.DeepEqual(accounts, want) {
		t.Fatalf("ListAccounts() = %#v, want %#v", accounts, want)
	}
	wantArgs := []string{"auth", "status", "--json", "hosts"}
	if !reflect.DeepEqual(runner.lastArgs(), wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.lastArgs(), wantArgs)
	}
}

func TestListAccountsExcludesFailedEntriesAndOtherHosts(t *testing.T) {
	runner := &stubRunner{output: []byte(`{"hosts":{
		"github.com":[
			{"state":"failure","active":false,"host":"github.com","login":"expired"},
			{"state":"success","active":true,"host":"github.com","login":"daiksud"}
		],
		"ghe.example.com":[
			{"state":"success","active":true,"host":"ghe.example.com","login":"other"}
		]
	}}`)}

	accounts, err := ListAccounts(context.Background(), runner, "github.com")
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	want := []Account{{Login: "daiksud", Active: true}}
	if !reflect.DeepEqual(accounts, want) {
		t.Fatalf("ListAccounts() = %#v, want %#v", accounts, want)
	}
}

func TestListAccountsReturnsEmptyWhenHostIsAbsent(t *testing.T) {
	runner := &stubRunner{output: []byte(`{"hosts":{"ghe.example.com":[{"state":"success","active":true,"host":"ghe.example.com","login":"other"}]}}`)}

	accounts, err := ListAccounts(context.Background(), runner, "github.com")
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("ListAccounts() = %#v, want empty", accounts)
	}
}

func TestListAccountsReturnsEmptyWhenAllEntriesFailed(t *testing.T) {
	runner := &stubRunner{output: []byte(`{"hosts":{"github.com":[{"state":"failure","active":false,"host":"github.com","login":"expired"}]}}`)}

	accounts, err := ListAccounts(context.Background(), runner, "github.com")
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("ListAccounts() = %#v, want empty", accounts)
	}
}

func TestListAccountsRejectsMalformedJSON(t *testing.T) {
	runner := &stubRunner{output: []byte("not json")}

	_, err := ListAccounts(context.Background(), runner, "github.com")
	if err == nil {
		t.Fatal("ListAccounts() error = nil, want a parse failure")
	}
}

func TestListAccountsRejectsEmptyHost(t *testing.T) {
	runner := &stubRunner{output: []byte(`{"hosts":{}}`)}

	_, err := ListAccounts(context.Background(), runner, "")
	if err == nil {
		t.Fatal("ListAccounts() error = nil, want host validation failure")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("Output() calls = %d, want 0 (validated before execution)", len(runner.calls))
	}
}

func TestListAccountsPropagatesRunnerFailure(t *testing.T) {
	wantErr := errors.New("gh not installed")
	runner := &stubRunner{err: wantErr}

	_, err := ListAccounts(context.Background(), runner, "github.com")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListAccounts() error = %v, want wrapping %v", err, wantErr)
	}
}
