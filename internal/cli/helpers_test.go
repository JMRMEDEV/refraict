package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/summarize"
)

type staticBackend struct{ out string }

func (s *staticBackend) Complete(ctx context.Context, req model.TextRequest) (*model.TextResult, error) {
	return &model.TextResult{Output: s.out, Confidence: 1.0}, nil
}

// TestCrossRegionSummaryNilBackend verifies the region-summary adapter falls
// back gracefully when no text backend is available.
func TestCrossRegionSummaryNilBackend(t *testing.T) {
	cr := &model.VisionResult{CropID: "c0001", Description: "a hero header with buttons"}
	out := crossRegionSummary(nil, context.Background(), cr)
	if !strings.Contains(out, "a hero header with buttons") {
		t.Fatalf("fallback should include crop description, got %q", out)
	}
}

// TestCrossRegionSummaryBackend verifies the region-summary adapter forwards
// to the configured text backend.
func TestCrossRegionSummaryBackend(t *testing.T) {
	cr := &model.VisionResult{CropID: "c0001", Description: "a hero header"}
	s := summarize.New(&staticBackend{out: "Condensed region."})
	out := crossRegionSummary(s, context.Background(), cr)
	if !strings.Contains(out, "Condensed region.") {
		t.Fatalf("expected backend output, got %q", out)
	}
}

// TestProbableDOM ensures DOM inference emits a well-formed fragment.
func TestProbableDOM(t *testing.T) {
	comps := []ir.Component{
		{ID: "c1", Type: ir.ConstString{Value: "button_primary", Confidence: 0.9}, BBox: ir.BoundingBox{X0: 10, Y0: 10, X1: 100, Y1: 40}, Confidence: 0.9},
		{ID: "c2", Type: ir.ConstString{Value: "text", Confidence: 0.8}, BBox: ir.BoundingBox{X0: 10, Y0: 50, X1: 200, Y1: 70}, Confidence: 0.8, Text: &ir.ConstString{Value: "Welcome", Confidence: 0.9}},
	}
	dom := probableDOM(comps)
	if !strings.Contains(dom, "<button") || !strings.Contains(dom, "Welcome") {
		t.Fatalf("DOM missing content: %q", dom)
	}
}

// TestInferPageType verifies lightweight page-type classification.
func TestInferPageType(t *testing.T) {
	toks := []ir.ORCToken{{Text: "Sign in with your email and password", BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}}}
	if got := inferPageType(nil, toks); got != "login" {
		t.Fatalf("expected login, got %q", got)
	}
}
