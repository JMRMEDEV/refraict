package ocr

import (
	"image"
	"image/color"
	"testing"
)

func TestNormalizeToken(t *testing.T) {
	cases := map[string]string{
		"usp":   "USD",
		"USO":   "USD",
		"usd.":  "USD.",
		"Sign":  "Sign",   // untouched
		"$1.58": "$1.58",  // untouched
	}
	for in, want := range cases {
		if got := normalizeToken(in); got != want {
			t.Fatalf("normalizeToken(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMeanLuminanceAndInvert(t *testing.T) {
	dark := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			dark.SetRGBA(x, y, color.RGBA{20, 20, 20, 255})
		}
	}
	if l := meanLuminance(dark); l > 40 {
		t.Fatalf("dark image luminance too high: %f", l)
	}
	inv := invert(dark)
	c := color.RGBAModel.Convert(inv.At(0, 0)).(color.RGBA)
	if c.R != 235 || c.G != 235 || c.B != 235 {
		t.Fatalf("invert wrong: %+v", c)
	}
	if meanLuminance(inv) < 200 {
		t.Fatalf("inverted image should be bright, got %f", meanLuminance(inv))
	}
}

func TestPreprocessUpscales(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 40))
	out := preprocess(img, 2.0, 110)
	b := out.Bounds()
	if b.Dx() != 100 || b.Dy() != 80 {
		t.Fatalf("expected 2x upscale to 100x80, got %dx%d", b.Dx(), b.Dy())
	}
}
