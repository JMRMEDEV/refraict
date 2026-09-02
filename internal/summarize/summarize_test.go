package summarize

import (
	"context"
	"testing"
	"strings"

	"github.com/refraict/refraict/internal/model"
)

// staticBackend returns a fixed completion.
type staticBackend struct{ out string }

func (s *staticBackend) Complete(ctx context.Context, req model.TextRequest) (*model.TextResult, error) {
	return &model.TextResult{Output: s.out, Confidence: 1.0}, nil
}

func TestPageSummaryUsesBackend(t *testing.T) {
	regions := []string{"hero", "footer"}
	s := New(&staticBackend{out: "A page."})
	got, err := s.PageSummary(context.Background(), regions, "dashboard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "A page." {
		t.Fatalf("expected backend output, got %q", got)
	}
}

func TestPageSummaryFallbackNilBackend(t *testing.T) {
	var s *Summarizer
	regions := []string{"hero region", "footer region"}
	got, err := s.PageSummary(context.Background(), regions, "dashboard")
	if err != nil {
		t.Fatalf("fallback should not error, got %v", err)
	}
	if !strings.Contains(got, "hero region") || !strings.Contains(got, "dashboard") {
		t.Fatalf("fallback missing content: %q", got)
	}
}

func TestRegionSummaryFallbackJoins(t *testing.T) {
	s := New(nil)
	got, err := s.RegionSummary(context.Background(), []string{"crop one", "crop two"})
	if err != nil {
		t.Fatalf("fallback should not error, got %v", err)
	}
	if !strings.Contains(got, "crop one") || !strings.Contains(got, "crop two") {
		t.Fatalf("fallback did not join crops: %q", got)
	}
}

// TestAssemblePage verifies deterministic page composition: overview leads, each
// section appears verbatim under its own header, and page type is included.
func TestAssemblePage(t *testing.T) {
	out := AssemblePage("kanban", "A kanban board with four columns.", []Section{
		{ID: "c0001", Description: "TO DO column with 4 cards."},
		{ID: "c0002", Description: "IN PROGRESS column with 3 cards."},
	})
	for _, want := range []string{
		"Page type: kanban",
		"Overview (whole-image read)",
		"A kanban board with four columns.",
		"### c0001",
		"TO DO column with 4 cards.",
		"### c0002",
		"IN PROGRESS column with 3 cards.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("assembled page missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(AssemblePage("generic", "x", nil), "Page type: generic") {
		t.Fatal("generic page type should not be labeled")
	}
	if AssemblePage("", "", nil) == "" {
		t.Fatal("empty assembly should still return a document")
	}
}
