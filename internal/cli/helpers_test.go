package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/summarize"
)

func TestInferPageTypeBillingUsage(t *testing.T) {
	toks := []ir.OCRToken{
		{Text: "Usage"}, {Text: "API keys"}, {Text: "Top up"}, {Text: "Billing"},
		{Text: "Total cost"}, {Text: "$1.58"}, {Text: "USD"},
		{Text: "API requests"}, {Text: "Tokens"}, {Text: "Last 30 days"},
	}
	got := inferPageType(nil, toks)
	// Should classify as one of the finance/usage-oriented types, not "generic".
	switch got {
	case "billing", "usage", "analytics", "api", "dashboard":
		// acceptable
	default:
		t.Fatalf("expected a billing/usage-family page type, got %q", got)
	}
}

func TestInferPageTypeGenericFallback(t *testing.T) {
	toks := []ir.OCRToken{{Text: "lorem"}, {Text: "ipsum"}}
	if got := inferPageType(nil, toks); got != "generic" {
		t.Fatalf("expected generic for unrelated text, got %q", got)
	}
}

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
	toks := []ir.OCRToken{{Text: "Sign in with your email and password", BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}}}
	if got := inferPageType(nil, toks); got != "login" {
		t.Fatalf("expected login, got %q", got)
	}
}

func TestInferPageGraphModelAugmentation(t *testing.T) {
	comps := []ir.Component{
		{ID: "a", Type: ir.ConstString{Value: "navbar"}, BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 200, Y1: 40}},
		{ID: "b", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X0: 0, Y0: 80, X1: 200, Y1: 160}},
	}
	// No backend => geometric only, no model relationships.
	geo := inferPageGraph(context.Background(), comps, nil)
	for _, r := range geo.Relationships {
		if r.Source == "model" {
			t.Fatalf("expected no model relationships without backend, got %+v", r)
		}
	}

	// Mock backend that returns a model relationship the geometric pass misses.
	backend := &staticBackend{out: "- a contains b\n"}
	aug := inferPageGraph(context.Background(), comps, backend)
	foundModel := false
	for _, r := range aug.Relationships {
		if r.Source == "model" && r.A == "a" && r.B == "b" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Fatalf("expected model relationship to be appended, got %+v", aug.Relationships)
	}

	// A backend returning nothing parseable keeps the geometric graph intact.
	empty := &staticBackend{out: "not a relationship line"}
	en := inferPageGraph(context.Background(), comps, empty)
	for _, r := range en.Relationships {
		if r.Source == "model" {
			t.Fatalf("parse-failure should not add model relationships, got %+v", r)
		}
	}
}

func TestPageConfidence(t *testing.T) {
	comps := []ir.Component{
		{Confidence: 0.9},
		{Confidence: 0.5},
	}
	if pc := pageConfidence(comps); pc != 0.7 {
		t.Fatalf("page confidence: got %v want 0.7", pc)
	}
	if pc := pageConfidence(nil); pc != 0.5 {
		t.Fatalf("empty page confidence: got %v want 0.5", pc)
	}
}

func TestUnresolvedComponents(t *testing.T) {
	comps := []ir.Component{
		{Confidence: 0.9},
		{Confidence: 0.4},
		{Confidence: 0.2},
	}
	if n := unresolvedComponents(comps, 0.8); n != 2 {
		t.Fatalf("unresolved: got %d want 2", n)
	}
}

func TestCropDisagreementRate(t *testing.T) {
	if r := cropDisagreementRate(2, 1, 4); r != 0.5 {
		t.Fatalf("disagreement rate: got %v want 0.5", r)
	}
	if r := cropDisagreementRate(0, 0, 0); r != 0 {
		t.Fatalf("empty rate: got %v want 0", r)
	}
}

func TestRedactText(t *testing.T) {
	got := redactText("Hello, world! 123")
	if strings.Contains(got, "Hello") || strings.Contains(got, "world") || strings.Contains(got, "123") {
		t.Fatalf("redaction left visible text: %q", got)
	}
	// Structural punctuation must survive so layout shape is preserved.
	for _, keep := range []string{",", "!", " "} {
		if !strings.Contains(got, keep) {
			t.Fatalf("redaction dropped structural char %q in %q", keep, got)
		}
	}
}
