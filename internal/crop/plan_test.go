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

func TestSubdivideRespectsDetailLongSide(t *testing.T) {
	reg := ir.BoundingBox{X0: 0, Y0: 0, X1: 400, Y1: 300} // fits 1000 but not 250
	cfg := CropPlanConfig{
		CropLongSide:   1000,
		DetailLongSide: 250,
		Overlap:        0.0,
	}
	crops := SubdivideRegion(nil, nil, reg, cfg, 0)
	if len(crops) == 0 {
		t.Fatal("expected detail-level subdivision")
	}
	for _, c := range crops {
		if c.BBox.X1-c.BBox.X0 > 250+1 || c.BBox.Y1-c.BBox.Y0 > 250+1 {
			t.Errorf("crop %s exceeds DetailLongSide: %+v", c.ID, c.BBox)
		}
	}
}

func TestSubdivideSplitsByTextLegibility(t *testing.T) {
	// Region fits within CropLongSide, but text is tiny relative to the
	// after-resize minimum: NeedsSubdivision should force finer crops.
	reg := ir.BoundingBox{X0: 0, Y0: 0, X1: 600, Y1: 400}
	toks := []ir.OCRToken{
		{Text: "a", BBoxGlobal: ir.BoundingBox{X0: 10, Y0: 20, X1: 11, Y1: 21}},
		{Text: "b", BBoxGlobal: ir.BoundingBox{X0: 100, Y0: 20, X1: 101, Y1: 21}},
	}
	// Region fits within both long sides (600,600), min text 10px:
	// resized text = 1 * (600/400) = 1.5 < 10 => subdivide on legibility.
	cfg := CropPlanConfig{
		CropLongSide:      600,
		DetailLongSide:    600,
		MinimumTextHeight: 10,
	}
	crops := SubdivideRegion(nil, toks, reg, cfg, 0)
	if len(crops) < 2 {
		t.Fatalf("expected text-legibility subdivision, got %d crops", len(crops))
	}

	// A region with no legibility pressure (large text) should stay as one crop.
	big := []ir.OCRToken{
		{Text: "a", BBoxGlobal: ir.BoundingBox{X0: 10, Y0: 20, X1: 50, Y1: 120}},
		{Text: "b", BBoxGlobal: ir.BoundingBox{X0: 100, Y0: 20, X1: 140, Y1: 120}},
	}
	cfg2 := CropPlanConfig{
		CropLongSide:      600,
		DetailLongSide:    600,
		MinimumTextHeight: 10,
	}
	// median height 100px, resized to 600/400 => 150 >= 10, no subdivision.
	single := SubdivideRegion(nil, big, reg, cfg2, 0)
	if len(single) != 1 {
		t.Fatalf("expected single crop for legible text, got %d", len(single))
	}
}

func TestPlanOverviewGridBounded(t *testing.T) {
	// 2x3 grid => 1 overview + 6 tiles, regardless of content.
	plan := PlanOverviewGrid(1912, 914, GridPlanConfig{Rows: 2, Cols: 3, Overlap: 0.15})
	if got := len(plan.Crops); got != 1+2*3 {
		t.Fatalf("expected %d crops (1 overview + 6 tiles), got %d", 1+2*3, got)
	}
	if plan.Crops[0].ID != "ov" || plan.Crops[0].Level != 0 {
		t.Fatalf("first crop must be the overview, got %+v", plan.Crops[0])
	}
	// Tiles must stay within image bounds.
	for _, c := range plan.Crops[1:] {
		if c.BBox.X0 < 0 || c.BBox.Y0 < 0 || c.BBox.X1 > 1912 || c.BBox.Y1 > 914 {
			t.Errorf("tile %s out of bounds: %+v", c.ID, c.BBox)
		}
		if c.BBox.Empty() {
			t.Errorf("tile %s is empty: %+v", c.ID, c.BBox)
		}
	}
}

func TestPlanOverviewGridUnionCoversImage(t *testing.T) {
	w, h := 1000, 800
	plan := PlanOverviewGrid(w, h, GridPlanConfig{Rows: 2, Cols: 2, Overlap: 0.1})
	// Every pixel must be covered by at least one tile (excluding the overview).
	// Sample a coarse grid of points and confirm coverage.
	tiles := plan.Crops[1:]
	for _, px := range []int{0, w / 2, w - 1} {
		for _, py := range []int{0, h / 2, h - 1} {
			covered := false
			for _, c := range tiles {
				if px >= c.BBox.X0 && px < c.BBox.X1 && py >= c.BBox.Y0 && py < c.BBox.Y1 {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("point (%d,%d) not covered by any tile", px, py)
			}
		}
	}
}

func TestPlanOverviewGridClampsDegenerate(t *testing.T) {
	// Rows/Cols < 1 must clamp to 1 (=> 1 overview + 1 tile).
	plan := PlanOverviewGrid(200, 100, GridPlanConfig{Rows: 0, Cols: 0})
	if got := len(plan.Crops); got != 2 {
		t.Fatalf("expected 2 crops for clamped 1x1 grid, got %d", got)
	}
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
	toks := []ir.OCRToken{
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}},
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 20}},
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 14}},
	}
	if m := MedianTextHeight(toks); m != 14 {
		t.Fatalf("expected median 14, got %v", m)
	}
}
