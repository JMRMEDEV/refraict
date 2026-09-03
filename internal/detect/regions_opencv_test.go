package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func rb(x0, y0, x1, y1 int) RegionBox {
	return RegionBox{BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}}
}

func TestDedupeBoxesIoUKeepsOuter(t *testing.T) {
	// Inner/outer twin of the same "Tokens" card (observed in the PoC),
	// offset by ~8px => IoU ~0.9. Dedup must keep exactly one (the outer).
	boxes := []RegionBox{
		rb(1238, 285, 1551, 397), // outer
		rb(1246, 293, 1543, 389), // inner twin
	}
	got := dedupeBoxesIoU(boxes)
	if len(got) != 1 {
		t.Fatalf("expected 1 box after dedup, got %d: %+v", len(got), got)
	}
	// The kept box should be the larger (outer) one.
	if got[0].BBox.Area() < rb(1246, 293, 1543, 389).BBox.Area() {
		t.Fatalf("dedup kept the inner box, expected the outer: %+v", got[0].BBox)
	}
}

func TestDedupeKeepsDistinctCards(t *testing.T) {
	// Three separate stat cards (low IoU) must all survive.
	boxes := []RegionBox{
		rb(606, 285, 919, 397),
		rb(921, 285, 1235, 397),
		rb(1238, 285, 1551, 397),
	}
	got := dedupeBoxesIoU(boxes)
	if len(got) != 3 {
		t.Fatalf("expected 3 distinct cards kept, got %d", len(got))
	}
}

func TestFilterNestedDropsPlotArtifact(t *testing.T) {
	// The spurious chart-interior box r0003 is fully inside the chart container.
	chart := rb(606, 412, 1551, 762)
	artifact := rb(1148, 607, 1487, 717) // fully within chart
	got := filterNested([]RegionBox{chart, artifact})
	if len(got) != 1 {
		t.Fatalf("expected artifact dropped, got %d boxes: %+v", len(got), got)
	}
	if got[0].BBox != chart.BBox {
		t.Fatalf("expected the chart container kept, got %+v", got[0].BBox)
	}
}

func TestFilterNestedKeepsSeparateRegions(t *testing.T) {
	// Chart and stat cards do not overlap => all kept.
	boxes := []RegionBox{
		rb(606, 412, 1551, 762), // chart
		rb(606, 285, 919, 397),  // stat card above the chart
	}
	got := filterNested(boxes)
	if len(got) != 2 {
		t.Fatalf("expected 2 non-overlapping regions kept, got %d", len(got))
	}
}

func TestBoxIoU(t *testing.T) {
	a := ir.BoundingBox{X0: 0, Y0: 0, X1: 10, Y1: 10}
	if iou := boxIoU(a, a); iou < 0.99 {
		t.Fatalf("identical boxes IoU should be ~1, got %f", iou)
	}
	disjoint := ir.BoundingBox{X0: 100, Y0: 100, X1: 110, Y1: 110}
	if iou := boxIoU(a, disjoint); iou != 0 {
		t.Fatalf("disjoint boxes IoU should be 0, got %f", iou)
	}
}

func TestUnionRegionBoxes(t *testing.T) {
	// keep = pass-1 (icons); add = pass-2 (CLAHE cards). A card box that does not
	// overlap any kept box is added; a near-duplicate of a kept box is dropped.
	keep := []RegionBox{rb(0, 0, 40, 40)}           // an icon
	add := []RegionBox{
		rb(200, 200, 760, 500),                      // a new card -> added
		rb(2, 2, 42, 42),                            // ~duplicate of the icon -> dropped
	}
	out := unionRegionBoxes(keep, add, 0.6)
	if len(out) != 2 {
		t.Fatalf("expected 2 boxes (icon + new card), got %d", len(out))
	}
	// The kept icon must always be present.
	if out[0].BBox != keep[0].BBox {
		t.Fatalf("kept box not preserved: %+v", out[0].BBox)
	}
}
