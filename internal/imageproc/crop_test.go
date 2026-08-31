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
