package cli

import (
	"testing"

	"github.com/refraict/refraict/internal/dub"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
)

// TestBrokenVLMOutputDoesNotCollapse is an integration-style regression test for
// QA findings C1/C2/G1/G3: when a vision model returns components with empty
// IDs and zero bounding boxes, the pipeline must repair coordinates + synthesize
// IDs so that reconciliation produces a non-degenerate canonical IR rather than
// silently collapsing to null.
//
// It mirrors the crop-ingest path in analyze.go: raw component observations are
// repaired, then reconciled via dub, and the result must contain non-zero boxes
// with non-empty IDs.
func TestBrokenVLMOutputDoesNotCollapse(t *testing.T) {
	cropBox := ir.BoundingBox{X0: 0, Y0: 0, X1: 640, Y1: 480}
	toks := []ir.ORCToken{
		{Text: "Sign in", BBoxGlobal: ir.BoundingBox{X0: 40, Y0: 50, X1: 140, Y1: 80}},
		{Text: "Top up", BBoxGlobal: ir.BoundingBox{X0: 240, Y0: 90, X1: 320, Y1: 120}},
	}

	// Simulate two crops, each returning components exactly like the e2e VLM:
	// empty id and zero bbox, but with text.
	raw := []model.VisionResult{
		{
			CropID:       "c0001",
			BBoxGlobal:   cropBox,
			RoleGuess:    "header",
			Description:  "a banner with sign-in",
			Confidence:   0.9,
			SchemaFailed: false,
			Components: []model.VisionCompRef{
				{ID: "", Type: "button", BBoxGlobal: ir.BoundingBox{}, Text: "Sign in", Confidence: 0.9},
				{ID: "", Type: "button", BBoxGlobal: ir.BoundingBox{}, Text: "Top up", Confidence: 0.8},
			},
		},
	}

	// Repair + reconcile, exactly as analyze.go does.
	var cropComponents []ir.Component
	for _, vr := range raw {
		ocr := tokensIn(vr.BBoxGlobal, toks)
		for idx, comp := range vr.Components {
			cc, oc := repairComponent(comp, vr.CropID, idx, vr.BBoxGlobal, ocr, 1280, 914)
			if oc.Dropped {
				t.Fatalf("component %d should not be dropped: %+v", idx, oc)
			}
			cropComponents = append(cropComponents, cc)
		}
	}

	merged := dub.Reconcile(cropComponents, dub.Options{IoUThreshold: 0.8})

	// The canonical IR must not collapse.
	if len(merged) == 0 {
		t.Fatal("reconciled output collapsed to zero components; repair failed")
	}
	for _, c := range merged {
		if c.BBox.Empty() {
			t.Errorf("component %q has empty bbox: %+v", c.ID, c.BBox)
		}
		if c.ID == "" {
			t.Errorf("component has no ID after repair")
		}
	}
	// The OCR-matched component should have recovered the precise token box.
	foundSignIn := false
	for _, c := range merged {
		if c.Text != nil && c.Text.Value == "Sign in" && c.BBox.X0 == 40 {
			foundSignIn = true
		}
	}
	if !foundSignIn {
		t.Errorf("expected OCR token box recovery for 'Sign in', got components: %+v", merged)
	}
}
