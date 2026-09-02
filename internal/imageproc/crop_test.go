package imageproc

import (
	"image"
	"image/color"
	"testing"
)

func testImage(w, h int) *Image {
	img := newRGBA(w, h, color.RGBA{200, 100, 50, 255})
	return &Image{img: img}
}

func newRGBA(w, h int, c color.RGBA) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rgba.SetRGBA(x, y, c)
		}
	}
	return rgba
}

func TestCropRegion(t *testing.T) {
	im := testImage(100, 100)
	sub := im.CropRegion(10, 10, 60, 60, 0)
	if sub == nil {
		t.Fatal("crop returned nil")
	}
	b := sub.Bounds()
	if b.Dx() != 50 || b.Dy() != 50 {
		t.Fatalf("expected 50x50 crop, got %v", b)
	}
	pix := sub.At(0, 0).(color.RGBA)
	if pix.R != 200 || pix.G != 100 || pix.B != 50 {
		t.Fatalf("unexpected pixel color: %v", pix)
	}
}

func TestCropRegionResize(t *testing.T) {
	im := testImage(400, 200)
	// Crop a tall region and cap longest side at 64.
	sub := im.CropRegion(0, 0, 100, 300, 64)
	if sub == nil {
		t.Fatal("crop returned nil")
	}
	w, h := sub.Bounds().Dx(), sub.Bounds().Dy()
	if w > 64 || h > 64 {
		t.Fatalf("crop longest side exceeded 64: %v x %v", w, h)
	}
}

func TestHasBarChartGeometry(t *testing.T) {
	// Synthetic bar chart: light bg, 4 tall dark bars sharing a bottom baseline.
	w, h := 120, 80
	rgba := newRGBA(w, h, color.RGBA{240, 240, 240, 255})
	bar := color.RGBA{20, 20, 20, 255}
	barW := 12
	gap := 18
	heights := []int{60, 40, 55, 30}
	x := 10
	for _, bh := range heights {
		for px := x; px < x+barW && px < w; px++ {
			for py := h - 5 - bh; py < h-5; py++ {
				rgba.SetRGBA(px, py, bar)
			}
		}
		x += barW + gap
	}
	if !HasBarChartGeometry(rgba, 0, 0, w, h) {
		t.Fatal("expected synthetic bar chart to pass geometry gate")
	}

	// Solid blob: not a chart.
	blob := newRGBA(w, h, color.RGBA{240, 240, 240, 255})
	for py := 20; py < 60; py++ {
		for px := 40; px < 80; px++ {
			blob.SetRGBA(px, py, color.RGBA{20, 20, 20, 255})
		}
	}
	if HasBarChartGeometry(blob, 0, 0, w, h) {
		t.Fatal("solid blob must not pass the bar-chart gate")
	}

	// Uniform region: not a chart.
	uni := newRGBA(w, h, color.RGBA{128, 128, 128, 255})
	if HasBarChartGeometry(uni, 0, 0, w, h) {
		t.Fatal("uniform region must not pass the bar-chart gate")
	}
}

func TestCropRegionClamp(t *testing.T) {
	im := testImage(50, 50)
	sub := im.CropRegion(-10, 0, 100, 50, 0)
	if sub == nil {
		t.Fatal("crop returned nil")
	}
	b := sub.Bounds()
	if b.Dx() != 50 || b.Dy() != 50 {
		t.Fatalf("expected clamped 50x50 crop, got %v", b)
	}
}
