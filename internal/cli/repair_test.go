package cli

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
)

// TestRepairComponentSynthesizesID verifies a component without an ID gets a
// deterministic crop-index ID.
func TestRepairComponentSynthesizesID(t *testing.T) {
	cropBox := ir.BoundingBox{X0: 0, Y0: 0, X1: 640, Y1: 900}
	cmp := model.VisionCompRef{
		ID:         "",
		Type:       "button",
		BBoxGlobal: ir.BoundingBox{X0: 10, Y0: 20, X1: 100, Y1: 50},
		Confidence: 0.9,
	}
	cc, oc := repairComponent(cmp, "c0001", 3, cropBox, nil, 1280, 914)
	if cc.ID != "c0001-3" {
		t.Fatalf("expected synthesized ID c0001-3, got %q", cc.ID)
	}
	if oc.Dropped || oc.BoxesFlagged {
		t.Fatalf("valid box should not be flagged/dropped: %+v", oc)
	}
	if !cc.BBox.Empty() {
		// present box should be preserved
		if cc.BBox.X0 != 10 {
			t.Fatalf("box not preserved: %+v", cc.BBox)
		}
	}
}

// TestRepairComponentEmptyBoxFallsBackToCrop verifies an empty box falls back
// to the crop box so the component survives.
func TestRepairComponentEmptyBoxFallsBackToCrop(t *testing.T) {
	cropBox := ir.BoundingBox{X0: 0, Y0: 100, X1: 640, Y1: 900}
	cmp := model.VisionCompRef{
		ID:         "x",
		Type:       "text",
		BBoxGlobal: ir.BoundingBox{},
		Text:       "Hello",
		Confidence: 0.8,
	}
	cc, oc := repairComponent(cmp, "c0001", 0, cropBox, nil, 1280, 914)
	if oc.Dropped {
		t.Fatal("component should not be dropped")
	}
	if !oc.BoxesFlagged || !oc.Repaired {
		t.Fatalf("expected flagged+repaired, got %+v", oc)
	}
	if cc.BBox != cropBox {
		t.Fatalf("expected fallback to crop box %+v, got %+v", cropBox, cc.BBox)
	}
}

// TestRepairComponentOCRTokenFallback verifies a text-equal OCR token box is
// preferred over the crop box when the model omits coordinates.
func TestRepairComponentOCRTokenFallback(t *testing.T) {
	cropBox := ir.BoundingBox{X0: 0, Y0: 0, X1: 640, Y1: 900}
	toks := []ir.ORCToken{
		{Text: "Sign in", BBoxGlobal: ir.BoundingBox{X0: 40, Y0: 50, X1: 120, Y1: 70}},
	}
	cmp := model.VisionCompRef{
		ID:         "",
		Type:       "button",
		BBoxGlobal: ir.BoundingBox{},
		Text:       "Sign in",
		Confidence: 0.7,
	}
	cc, oc := repairComponent(cmp, "c0001", 1, cropBox, toks, 1280, 914)
	if oc.Dropped || !oc.Repaired {
		t.Fatalf("expected repair via OCR, got %+v", oc)
	}
	if cc.BBox.X0 != 40 || cc.BBox.X1 != 120 {
		t.Fatalf("expected OCR token box, got %+v", cc.BBox)
	}
}

// TestRepairComponentClampsOutOfBounds verifies boxes are clamped to the image.
func TestRepairComponentClampsOutOfBounds(t *testing.T) {
	cropBox := ir.BoundingBox{X0: 0, Y0: 0, X1: 640, Y1: 500}
	cmp := model.VisionCompRef{
		ID:         "a",
		Type:       "card",
		BBoxGlobal: ir.BoundingBox{X0: -20, Y0: -10, X1: 2000, Y1: 3000},
		Confidence: 0.5,
	}
	cc, oc := repairComponent(cmp, "c0001", 2, cropBox, nil, 1280, 914)
	if oc.Dropped {
		t.Fatal("out-of-bounds but non-empty box should be clamped, not dropped")
	}
	// Clamped to image then intersected with crop.
	if cc.BBox.X0 != 0 || cc.BBox.Y0 != 0 || cc.BBox.X1 != 640 || cc.BBox.Y1 != 500 {
		t.Fatalf("box not clamped to crop: %+v", cc.BBox)
	}
}

// TestRepairComponentDropsWhenNoBox verifies a component cannot survive without
// any recoverable box.
func TestRepairComponentDropsWhenNoBox(t *testing.T) {
	cropBox := ir.BoundingBox{}
	cmp := model.VisionCompRef{
		ID:         "a",
		Type:       "text",
		BBoxGlobal: ir.BoundingBox{},
	}
	_, oc := repairComponent(cmp, "c0001", 0, cropBox, nil, 1280, 914)
	if !oc.Dropped {
		t.Fatal("expected component to be dropped when no box is recoverable")
	}
}

// TestRepairComponentPreservesID verifies a provided ID is kept.
func TestRepairComponentPreservesID(t *testing.T) {
	cropBox := ir.BoundingBox{X0: 0, Y0: 0, X1: 640, Y1: 900}
	cmp := model.VisionCompRef{
		ID:         "comp-42",
		Type:       "text",
		BBoxGlobal: ir.BoundingBox{X0: 1, Y0: 2, X1: 20, Y1: 10},
	}
	cc, _ := repairComponent(cmp, "c0001", 0, cropBox, nil, 1280, 914)
	if cc.ID != "comp-42" {
		t.Fatalf("expected preserved ID, got %q", cc.ID)
	}
}
