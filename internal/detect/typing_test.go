package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func box(x0, y0, x1, y1 int) RegionBox {
	return RegionBox{BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}}
}

func TestChartTypingDeferred(t *testing.T) {
	// Deterministic chart typing was removed (unreliable on real sparse-bar
	// charts). A large chart-container region should fall through to a generic
	// structural type, NOT be typed "chart". Chart identification is a Tier-2
	// VLM concern.
	rb := box(600, 412, 1551, 762)
	rb.AreaFrac = 0.3
	got := classifyRegion(rb, RegionSignals{OCROverlap: 0.05, MaxSidePx: 951, MinSidePx: 350})
	if got == "chart" {
		t.Fatalf("deterministic classifier must not emit chart, got %q", got)
	}
	if got != "panel" && got != "card" && got != "container" && got != "image" {
		t.Fatalf("expected a structural fallback type, got %q", got)
	}
}

func TestClassifyIcon(t *testing.T) {
	rb := box(30, 180, 54, 204) // 24x24
	got := classifyRegion(rb, RegionSignals{
		OCROverlap: 0.0, MaxSidePx: 24, MinSidePx: 24, AspectRatio: 1.0, Encloses: 0,
	})
	if got != "icon" {
		t.Fatalf("expected icon, got %q", got)
	}
}

func TestClassifyIconRejectsLarge(t *testing.T) {
	// Same compact/text-empty shape but too large => not an icon.
	rb := box(0, 0, 200, 200)
	got := classifyRegion(rb, RegionSignals{
		OCROverlap: 0.0, MaxSidePx: 200, MinSidePx: 200, AspectRatio: 1.0, Encloses: 0,
	})
	if got == "icon" {
		t.Fatalf("200px region should not be an icon")
	}
}

func TestClassifyIconGeometryFirst(t *testing.T) {
	// Icons are classified by geometry (small + compact), NOT OCR-emptiness,
	// because OCR often misreads an icon glyph as a phantom character. A small
	// compact region is typed icon even with some OCR overlap.
	rb := box(30, 180, 54, 204)
	got := classifyRegion(rb, RegionSignals{
		OCROverlap: 0.5, MaxSidePx: 24, MinSidePx: 24, AspectRatio: 1.0,
	})
	if got != "icon" {
		t.Fatalf("small compact region should be icon by geometry, got %q", got)
	}
}

func TestClassifyLogo(t *testing.T) {
	rb := box(20, 60, 200, 100)
	rb.FillRatio = 0.4
	got := classifyRegion(rb, RegionSignals{
		OCROverlap: 0.0, HeaderBand: true, Encloses: 0,
		MaxSidePx: 180, MinSidePx: 40, AspectRatio: 4.5,
	})
	if got != "logo" {
		t.Fatalf("expected logo, got %q", got)
	}
}

func TestClassifyContainer(t *testing.T) {
	rb := box(0, 0, 900, 700)
	rb.AreaFrac = 0.5
	got := classifyRegion(rb, RegionSignals{Encloses: 3, OCROverlap: 0.1})
	if got != "container" {
		t.Fatalf("expected container, got %q", got)
	}
}

func TestClassifyCard(t *testing.T) {
	rb := box(600, 285, 919, 397)
	rb.FillRatio = 0.9
	rb.AreaFrac = 0.05
	got := classifyRegion(rb, RegionSignals{OCROverlap: 0.2, MaxSidePx: 319, MinSidePx: 112, AspectRatio: 2.8})
	if got != "card" {
		t.Fatalf("expected card, got %q", got)
	}
}

func TestClassifyImageFallback(t *testing.T) {
	// Text-empty, not header, not small, not solid => image.
	rb := box(300, 300, 500, 420)
	rb.FillRatio = 0.5
	rb.AreaFrac = 0.01
	got := classifyRegion(rb, RegionSignals{OCROverlap: 0.0, MaxSidePx: 200, MinSidePx: 120, AspectRatio: 1.6, Encloses: 0})
	if got != "image" {
		t.Fatalf("expected image, got %q", got)
	}
}

func TestOCROverlapFrac(t *testing.T) {
	region := ir.BoundingBox{X0: 0, Y0: 0, X1: 100, Y1: 100} // area 10000
	toks := []ir.OCRToken{
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 0, X1: 50, Y1: 20}},   // 1000
		{BBoxGlobal: ir.BoundingBox{X0: 0, Y0: 30, X1: 100, Y1: 40}}, // 1000
	}
	f := ocrOverlapFrac(region, toks)
	if f < 0.19 || f > 0.21 {
		t.Fatalf("expected ~0.20 overlap, got %v", f)
	}
	if ocrOverlapFrac(region, nil) != 0 {
		t.Fatalf("nil tokens => 0 overlap")
	}
}
