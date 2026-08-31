package crop

import (
	"image"

	"testing"

	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
)

func mockImage(w, h int) *imageproc.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	return imageproc.NewImage(img)
}

func r(x0, y0, x1, y1 int) image.Rectangle {
	return image.Rect(x0, y0, x1, y1)
}

func TestPlanFixedCoversImage(t *testing.T) {
	crops := PlanFixed(100, 60, 50, 0.2)
	if len(crops) == 0 {
		t.Fatal("no crops generated")
	}
	// Last crop must reach the bottom-right edge.
	last := crops[len(crops)-1].BBox
	if last.X1 != 100 || last.Y1 != 60 {
		t.Fatalf("last crop does not reach bottom-right: %+v", last)
	}
}

func TestBuildPlanAlwaysHasOverview(t *testing.T) {
	im := mockImage(400, 400)
	plan := BuildPlan(im, nil, CropPlanConfig{CropLongSide: 128, Overlap: 0.2, Rect: r(0, 0, 400, 400)})
	if len(plan.Crops) == 0 || plan.Crops[0].ID != "ov" {
		t.Fatalf("expected overview as first crop, got %+v", plan.Crops)
	}
}

func TestNeedsSubdivision(t *testing.T) {
	if !NeedsSubdivision(1000, 10, 128, 12) {
		t.Error("expected subdivision needed for tiny text")
	}
	if NeedsSubdivision(100, 40, 128, 12) {
		t.Error("did not expect subdivision for comfortably large text")
	}
}

func TestMedianTextHeight(t *testing.T) {
	toks := []ir.ORCToken{
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}},
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 20}},
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 14}},
	}
	if m := MedianTextHeight(toks); m != 14 {
		t.Fatalf("expected median 14, got %v", m)
	}
}
