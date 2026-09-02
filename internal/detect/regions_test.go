package detect

import (
	"image"
	"image/color"
	"testing"
)

// drawRect fills a solid rectangle on img.
func drawRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, c)
		}
	}
}

// synthUI builds a dark canvas with two clearly separated lighter cards.
func synthUI() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	drawRect(img, 0, 0, 400, 300, color.RGBA{18, 20, 22, 255}) // dark bg
	drawRect(img, 20, 20, 180, 120, color.RGBA{60, 62, 64, 255})  // card A
	drawRect(img, 220, 20, 380, 120, color.RGBA{60, 62, 64, 255}) // card B
	return img
}

func TestConnectedComponentsCountsTwoBlobs(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 100, 50))
	// two separate white blobs on black
	drawRect(m, 0, 0, 100, 50, color.RGBA{0, 0, 0, 255})
	drawRect(m, 5, 5, 25, 25, color.RGBA{255, 255, 255, 255})
	drawRect(m, 60, 5, 90, 40, color.RGBA{255, 255, 255, 255})
	_, bounds, areas := connectedComponents(m)
	if len(bounds) != 2 {
		t.Fatalf("expected 2 components, got %d", len(bounds))
	}
	for lbl, a := range areas {
		if a <= 0 {
			t.Fatalf("component %d has non-positive area", lbl)
		}
	}
}

func TestConnectedComponentsMergesConnected(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 60, 20))
	drawRect(m, 0, 0, 60, 20, color.RGBA{0, 0, 0, 255})
	// One L-shaped connected blob.
	drawRect(m, 2, 2, 40, 8, color.RGBA{255, 255, 255, 255})
	drawRect(m, 2, 8, 10, 18, color.RGBA{255, 255, 255, 255})
	_, bounds, _ := connectedComponents(m)
	if len(bounds) != 1 {
		t.Fatalf("expected 1 merged component, got %d", len(bounds))
	}
}

func TestDetectRegionsFindsTwoCards(t *testing.T) {
	img := synthUI()
	opts := DefaultRegionOptions()
	opts.DownscaleLongSide = 0 // no downscale for the small synthetic image
	opts.MinAreaFrac = 0.01
	boxes := DetectRegions(img, opts)
	if len(boxes) < 2 {
		t.Fatalf("expected at least 2 card regions, got %d: %+v", len(boxes), boxes)
	}
	// Both cards should be roughly 160x100 and within bounds.
	for _, b := range boxes {
		if b.BBox.X1 > 400 || b.BBox.Y1 > 300 || b.BBox.X0 < 0 || b.BBox.Y0 < 0 {
			t.Errorf("box out of bounds: %+v", b.BBox)
		}
	}
}

func TestRegionComponentsTyping(t *testing.T) {
	img := synthUI()
	opts := DefaultRegionOptions()
	opts.DownscaleLongSide = 0
	opts.MinAreaFrac = 0.01
	comps := RegionComponents(img, opts)
	if len(comps) < 2 {
		t.Fatalf("expected >=2 region components, got %d", len(comps))
	}
	for _, c := range comps {
		if c.Source != "cv_region" {
			t.Errorf("expected source cv_region, got %q", c.Source)
		}
		switch c.Type.Value {
		case "card", "panel", "container", "region":
		default:
			t.Errorf("unexpected region type %q", c.Type.Value)
		}
	}
}

func TestDetectRegionsEmptyImage(t *testing.T) {
	if r := DetectRegions(nil, DefaultRegionOptions()); r != nil {
		t.Fatalf("expected nil for nil image")
	}
}

func TestDetectRegionsDropsBackground(t *testing.T) {
	// A uniform image has no foreground => no regions.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	drawRect(img, 0, 0, 200, 200, color.RGBA{20, 20, 20, 255})
	boxes := DetectRegions(img, DefaultRegionOptions())
	if len(boxes) != 0 {
		t.Fatalf("expected 0 regions on uniform image, got %d", len(boxes))
	}
}
