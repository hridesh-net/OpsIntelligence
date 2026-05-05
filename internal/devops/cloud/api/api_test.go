package api

import (
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

func TestMergeTagFilters(t *testing.T) {
	a := []config.CloudTagFilter{{Key: "env", Values: []string{"prod"}}}
	b := []config.CloudTagFilter{{Key: "app", Values: []string{"api"}}}
	got := MergeTagFilters(a, b)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Key != "env" || got[1].Key != "app" {
		t.Fatalf("order/content: %#v", got)
	}
	// Ensure callers can mutate without aliasing underlying slices.
	got[0].Key = "x"
	if a[0].Key != "env" {
		t.Fatal("mutated original slice a")
	}
}

func TestEscapeODataQuotes(t *testing.T) {
	if got := EscapeODataQuotes(`O'Reilly`); got != `O''Reilly` {
		t.Fatalf("got %q", got)
	}
}
