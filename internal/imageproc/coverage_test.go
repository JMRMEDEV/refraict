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

// drawCenteredInk fills an WxH image with `fill`, then stamps a block of `ink`
// pixels in the centre covering roughly `inkFrac` of the area — a crude stand-in
// for centered glyphs on a solid fill (e.g. white "Open" on a blue button).
func drawCenteredInk(w, h int, fill, ink color.RGBA, inkFrac float64) *image.RGBA {
	img := solidImage(w, h, fill)
	// Centre block sized so its area ≈ inkFrac of the whole.
	bw := int(float64(w) * inkFrac)
	bh := h / 2
	if bw < 1 {
		bw = 1
	}
	if bh < 1 {
		bh = 1
	}
	x0 := (w - bw) / 2
	y0 := (h - bh) / 2
	for y := y0; y < y0+bh; y++ {
		for x := x0; x < x0+bw; x++ {
			img.SetRGBA(x, y, ink)
		}
	}
	return img
}

// Example 1: a solid blue button (~90x40) with white centered text. The FILL
// behind the ink must measure as the blue, grounded, despite the white glyphs.
func TestSampleFillExcludingInk_SolidButtonCenteredText(t *testing.T) {
	blue := color.RGBA{2, 96, 201, 255} // #0260C9
	white := color.RGBA{255, 255, 255, 255}
	img := drawCenteredInk(90, 40, blue, white, 0.4)
	m := SampleFillExcludingInk(img, 0, 0, 90, 40, 0.0, 24, 90)
	if !m.OK {
		t.Fatal("expected ok")
	}
	if m.Hex != "#0260C9" {
		t.Fatalf("fill hex = %s, want #0260C9 (the ink must not flip the fill)", m.Hex)
	}
	if m.Coverage < 0.99 {
		t.Fatalf("coverage = %.3f, want ~1.0 for a solid fill (ink excluded)", m.Coverage)
	}
	if m.InkFraction < 0.1 {
		t.Fatalf("ink_fraction = %.3f, expected the white glyphs to be detected as ink", m.InkFraction)
	}
}

// Example 2: a full-width highlighted menu row — solid blue with white text.
// Same property: the fill grounds as blue.
func TestSampleFillExcludingInk_FullWidthHighlightedRow(t *testing.T) {
	blue := color.RGBA{2, 96, 201, 255}
	white := color.RGBA{255, 255, 255, 255}
	img := drawCenteredInk(400, 32, blue, white, 0.3)
	m := SampleFillExcludingInk(img, 0, 0, 400, 32, 0.0, 24, 90)
	if !m.OK {
		t.Fatal("expected ok")
	}
	if m.Hex != "#0260C9" {
		t.Fatalf("fill hex = %s, want #0260C9", m.Hex)
	}
	if m.Coverage < 0.99 {
		t.Fatalf("coverage = %.3f, want ~1.0 for a solid row fill", m.Coverage)
	}
}

// Integrity: a GRADIENT fill behind text must NOT ground — removing the ink does
// not rescue an ambiguous fill. Coverage stays low because the fill pixels
// themselves scatter across colors.
func TestSampleFillExcludingInk_GradientFillNotGrounded(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 32))
	black := color.RGBA{0, 0, 0, 255}
	for y := 0; y < 32; y++ {
		for x := 0; x < 256; x++ {
			// A horizontal gradient as the fill...
			img.SetRGBA(x, y, color.RGBA{uint8(x), 128, 200, 255})
		}
	}
	// ...with a centered dark ink block (the "glyphs").
	for y := 8; y < 24; y++ {
		for x := 100; x < 156; x++ {
			img.SetRGBA(x, y, black)
		}
	}
	m := SampleFillExcludingInk(img, 0, 0, 256, 32, 0.0, 24, 90)
	if !m.OK {
		t.Fatal("expected ok")
	}
	if m.Coverage > 0.60 {
		t.Fatalf("coverage = %.3f, want LOW for a gradient fill (must not falsely ground)", m.Coverage)
	}
}

// A glyph-MAJORITY tight bbox (mostly ink, little fill) is the case that SHOULD
// NOT ground: there is no fill to speak of. Coverage of the tiny residual fill
// may be high, but the ink fraction is dominant — the MCP verb's grounding gate
// (fill-pixel floor) handles rejection; here we assert the ink is detected.
func TestSampleFillExcludingInk_GlyphMajority(t *testing.T) {
	blue := color.RGBA{2, 96, 201, 255}
	white := color.RGBA{255, 255, 255, 255}
	// 80% ink — a tight glyph bbox, not a padded element rect.
	img := drawCenteredInk(50, 20, blue, white, 0.8)
	m := SampleFillExcludingInk(img, 0, 0, 50, 20, 0.0, 24, 90)
	if !m.OK {
		t.Fatal("expected ok")
	}
	if m.InkFraction < 0.4 {
		t.Fatalf("ink_fraction = %.3f, expected a high ink fraction for a glyph-majority rect", m.InkFraction)
	}
}

func TestSampleFillExcludingInk_Degenerate(t *testing.T) {
	img := solidImage(10, 10, color.RGBA{1, 2, 3, 255})
	if m := SampleFillExcludingInk(img, 5, 5, 5, 5, 0.0, 24, 90); m.OK {
		t.Fatal("expected !ok for a zero-area region")
	}
}
