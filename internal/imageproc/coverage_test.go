package imageproc

import (
	"image"
	"image/color"
	"testing"
)

// solidImage returns an WxH image filled with c.
func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestSampleRegionCoverage_SolidGrounds(t *testing.T) {
	green := color.RGBA{7, 138, 82, 255}
	img := solidImage(200, 20, green)
	hex, r, g, b, cov, px, ok := SampleRegionCoverage(img, 0, 0, 200, 20, 0.0, 24)
	if !ok {
		t.Fatal("expected ok")
	}
	if hex != "#078A52" {
		t.Fatalf("hex = %s, want #078A52", hex)
	}
	if r != 7 || g != 138 || b != 82 {
		t.Fatalf("rgb = (%d,%d,%d), want (7,138,82)", r, g, b)
	}
	if cov < 0.99 {
		t.Fatalf("coverage = %.3f, want ~1.0 for a solid fill", cov)
	}
	if px != 200*20 {
		t.Fatalf("pixels = %d, want %d", px, 200*20)
	}
}

// A half-green / half-white region: the dominant color still covers only ~half,
// so coverage must be LOW — the integrity signal that prevents false grounding
// of a mixed region.
func TestSampleRegionCoverage_MixedLowCoverage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 20))
	green := color.RGBA{7, 138, 82, 255}
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < 20; y++ {
		for x := 0; x < 200; x++ {
			if x < 100 {
				img.SetRGBA(x, y, green)
			} else {
				img.SetRGBA(x, y, white)
			}
		}
	}
	_, _, _, _, cov, _, ok := SampleRegionCoverage(img, 0, 0, 200, 20, 0.0, 24)
	if !ok {
		t.Fatal("expected ok")
	}
	if cov > 0.60 {
		t.Fatalf("coverage = %.3f, want LOW (<=0.60) for a two-tone region", cov)
	}
}

// A gradient region (continuously varying) must also yield low coverage: no
// single color dominates within the near threshold.
func TestSampleRegionCoverage_GradientLowCoverage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 256; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), 128, 128, 255})
		}
	}
	_, _, _, _, cov, _, ok := SampleRegionCoverage(img, 0, 0, 256, 20, 0.0, 24)
	if !ok {
		t.Fatal("expected ok")
	}
	if cov > 0.40 {
		t.Fatalf("coverage = %.3f, want LOW for a gradient", cov)
	}
}

func TestSampleRegionCoverage_Degenerate(t *testing.T) {
	img := solidImage(10, 10, color.RGBA{1, 2, 3, 255})
	if _, _, _, _, _, _, ok := SampleRegionCoverage(img, 5, 5, 5, 5, 0.0, 24); ok {
		t.Fatal("expected !ok for a zero-area region")
	}
}
