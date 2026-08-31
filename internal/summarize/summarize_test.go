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
