package router

import (
	"errors"
	"testing"

	"AgentSwitch/internal/account"
)

func TestSinglePicksLowestPriorityNumber(t *testing.T) {
	accounts := []account.Account{
		{ID: "b", Priority: 20, Enabled: true},
		{ID: "a", Priority: 10, Enabled: true},
		{ID: "c", Priority: 10, Enabled: false},
	}
	d, err := Single{}.Resolve(accounts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Chosen.ID != "a" {
		t.Fatalf("chosen %s", d.Chosen.ID)
	}
	if len(d.Candidates) != 3 {
		t.Fatalf("candidates %d", len(d.Candidates))
	}
	if !d.Candidates[2].Skipped || d.Candidates[2].Reason == "" {
		t.Fatal("disabled account must be recorded skipped with reason")
	}
}

func TestSingleNoCandidate(t *testing.T) {
	if _, err := (Single{}).Resolve(nil); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("got %v", err)
	}
	if _, err := (Single{}).Resolve([]account.Account{{ID: "x", Enabled: false}}); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("all disabled: %v", err)
	}
}
