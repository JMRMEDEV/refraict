package dub

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func TestReconcileMergesOverlappingSameType(t *testing.T) {
	raw := []ir.Component{
		{ID: "a", Type: ir.ConstString{Value: "button", Confidence: 0.9}, BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 100, Y1: 50}, Confidence: 0.9},
		{ID: "b", Type: ir.ConstString{Value: "button", Confidence: 0.8}, BBox: ir.BoundingBox{X0: 20, Y0: 10, X1: 120, Y1: 60}, Confidence: 0.8},
	}
	out := Reconcile(raw, Options{IoUThreshold: 0.3})
	if len(out) != 1 {
		t.Fatalf("expected 1 merged component, got %d", len(out))
	}
	c := out[0]
	if c.ID != "a" {
		t.Fatalf("expected first component to win identity, got %q", c.ID)
	}
}

func TestReconcileKeepsDisjoint(t *testing.T) {
	raw := []ir.Component{
		{ID: "a", Type: ir.ConstString{Value: "button", Confidence: 0.9}, BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 100, Y1: 50}, Confidence: 0.9},
		{ID: "b", Type: ir.ConstString{Value: "button", Confidence: 0.8}, BBox: ir.BoundingBox{X0: 400, Y0: 200, X1: 500, Y1: 250}, Confidence: 0.8},
	}
	out := Reconcile(raw, Options{IoUThreshold: 0.3})
	if len(out) != 2 {
		t.Fatalf("expected 2 components kept, got %d", len(out))
	}
}

func TestReconcileMergesSharedText(t *testing.T) {
	raw := []ir.Component{
		{ID: "a", Text: strptr("Submit"), BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 100, Y1: 50}, Confidence: 0.9},
		{ID: "b", Text: strptr("Submit"), BBox: ir.BoundingBox{X0: 30, Y0: 10, X1: 130, Y1: 60}, Confidence: 0.7},
	}
	out := Reconcile(raw, Options{IoUThreshold: 0.3})
	if len(out) != 1 {
		t.Fatalf("expected shared-text components to merge, got %d", len(out))
	}
}

func strptr(s string) *ir.ConstString {
	return &ir.ConstString{Value: s, Confidence: 0.8, Source: "ocr_or_vlm"}
}

func TestMergeUnionBox(t *testing.T) {
	a := ir.Component{ID: "a", BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 100, Y1: 50}, Confidence: 0.5}
	b := ir.Component{ID: "b", BBox: ir.BoundingBox{X0: 50, Y0: 25, X1: 200, Y1: 80}, Confidence: 0.9}
	merged := Merge(a, b)
	if merged.BBox.X0 != 0 || merged.BBox.Y0 != 0 || merged.BBox.X1 != 200 || merged.BBox.Y1 != 80 {
		t.Fatalf("union box wrong: %+v", merged.BBox)
	}
	// Confidence blended toward the larger b region.
	if merged.Confidence <= a.Confidence || merged.Confidence >= b.Confidence {
		t.Fatalf("confidence blend out of range: %v", merged.Confidence)
	}
}

func TestMergeOptsConfidenceWeight(t *testing.T) {
	low := ir.Component{ID: "a", Type: ir.ConstString{Value: "text"}, BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}, Confidence: 0.8}
	hi := ir.Component{ID: "b", Type: ir.ConstString{Value: "text"}, BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}, Confidence: 0.99}

	// weight=0 selects the legacy area-weighted average (equal areas => mean).
	legacy := MergeOpts(low, hi, 0)
	wantMean := (low.Confidence + hi.Confidence) / 2
	if legacy.Confidence != wantMean {
		t.Fatalf("weight=0 should be legacy average, got %v want %v", legacy.Confidence, wantMean)
	}

	// weight=1 keeps the conservative (area-equal) average, below the high.
	conservative := MergeOpts(low, hi, 1.0)
	if conservative.Confidence != wantMean {
		t.Fatalf("weight=1 should keep the average, got %v", conservative.Confidence)
	}

	// A small weight trusts the higher-confidence observation.
	trusting := MergeOpts(low, hi, 0.1)
	want := 0.1*wantMean + 0.9*hi.Confidence
	if trusting.Confidence != want {
		t.Fatalf("weight=0.1 blend wrong: got %v want %v", trusting.Confidence, want)
	}
	if trusting.Confidence <= wantMean {
		t.Fatalf("small weight should push toward the high reading, got %v", trusting.Confidence)
	}

	// Excessive weights are clamped to 1.
	clamped := MergeOpts(low, hi, 2.0)
	if clamped.Confidence != conservative.Confidence {
		t.Fatalf("weight clamped to 1: got %v want %v", clamped.Confidence, conservative.Confidence)
	}

	// Reconcile threads ConfidenceMerge from Options.
	out := Reconcile([]ir.Component{low, hi}, Options{IoUThreshold: 0.1, ConfidenceMerge: 0.1})
	if len(out) != 1 {
		t.Fatalf("expected overlap merged, got %d", len(out))
	}
	if out[0].Confidence != want {
		t.Fatalf("ConfidenceMerge not threaded through Reconcile: got %v want %v", out[0].Confidence, want)
	}
}
