package detect

import (
	"image"
	"image/color"
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func TestIsWordy(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"Settings", true},
		{"OF", true},        // 2 letters, all letters
		{"Sign in", true},   // spaces ignored, all letters
		{"£63", false},      // mostly non-letter
		{"@®", false},       // no letters
		{"(C3)", false},     // 1 letter, mostly symbols
		{"a", false},        // <2 letters
		{"API", true},       // abbreviation, all letters
		{"user@x.io", true}, // email is legitimate text (letters dominate)
	}
	for _, c := range cases {
		if got := isWordy(c.s, 0.55); got != c.want {
			t.Errorf("isWordy(%q)=%v, want %v", c.s, got, c.want)
		}
	}
}

func TestWeightConfidence(t *testing.T) {
	// On the line -> ~0; far above -> toward 1.
	if c := weightConfidence(14.0, 14.0, 10.0); c > 0.01 {
		t.Errorf("on-line confidence should be ~0, got %v", c)
	}
	if c := weightConfidence(24.0, 14.0, 10.0); c < 0.9 {
		t.Errorf("far-above confidence should be high, got %v", c)
	}
}

func TestMedianF(t *testing.T) {
	if m := medianF([]float64{3, 1, 2}); m != 2 {
		t.Errorf("median odd: want 2, got %v", m)
	}
	if m := medianF([]float64{1, 2, 3, 4}); m != 2.5 {
		t.Errorf("median even: want 2.5, got %v", m)
	}
	if m := medianF(nil); m != 0 {
		t.Errorf("median empty: want 0, got %v", m)
	}
}

// drawStroke draws a horizontal bar of the given thickness (a crude "stroke") in
// dark ink on a light canvas at the given box — enough for the SWT to measure a
// thicker vs thinner stroke.
func drawWord(img *image.RGBA, box image.Rectangle, thickness int) {
	ink := color.RGBA{20, 20, 20, 255}
	midY := (box.Min.Y + box.Max.Y) / 2
	for y := midY - thickness/2; y <= midY+thickness/2; y++ {
		for x := box.Min.X + 4; x < box.Max.X-4; x++ {
			if y >= box.Min.Y && y < box.Max.Y {
				img.SetRGBA(x, y, ink)
			}
		}
	}
}

func TestAttachTextWeights_SeparatesThickFromThin(t *testing.T) {
	// Build a light canvas with several thin "regular" bars and one thick "bold"
	// bar, all same height box. The SWT stroke should separate them.
	W, H := 400, 300
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			img.SetRGBA(x, y, color.RGBA{245, 245, 245, 255})
		}
	}
	// 5 thin regular words + 1 thick bold, boxes 30px tall.
	boxes := []image.Rectangle{
		image.Rect(20, 20, 180, 50),   // regular
		image.Rect(20, 60, 180, 90),   // regular
		image.Rect(20, 100, 180, 130), // regular
		image.Rect(20, 140, 180, 170), // regular
		image.Rect(20, 180, 180, 210), // regular
		image.Rect(20, 220, 180, 250), // BOLD (thick)
	}
	thick := []int{4, 4, 4, 4, 4, 12}
	var comps []ir.Component
	for i, b := range boxes {
		drawWord(img, b, thick[i])
		comps = append(comps, ir.Component{
			ID:   string(rune('a' + i)),
			Type: ir.ConstString{Value: "text"},
			Text: &ir.ConstString{Value: "Word"},
			BBox: ir.BoundingBox{X0: b.Min.X, Y0: b.Min.Y, X1: b.Max.X, Y1: b.Max.Y},
		})
	}
	n := AttachTextWeights(img, comps, DefaultWeightOptions())
	if n == 0 {
		t.Fatal("expected some weights attached")
	}
	// The thick one (index 5) should be heavy; at least one thin one regular.
	last := comps[5].Weight
	if last == nil || last.Weight != "heavy" {
		t.Errorf("thick word: want heavy, got %+v", last)
	}
	regulars := 0
	for i := 0; i < 5; i++ {
		if comps[i].Weight != nil && comps[i].Weight.Weight == "regular" {
			regulars++
		}
	}
	if regulars == 0 {
		t.Errorf("expected at least one thin word classified regular")
	}
}

func TestAttachTextWeights_SkipsNonTextAndJunk(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.SetRGBA(x, y, color.RGBA{245, 245, 245, 255})
		}
	}
	comps := []ir.Component{
		{ID: "icon", Type: ir.ConstString{Value: "icon"}, BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 40, Y1: 40}},
		{ID: "junk", Type: ir.ConstString{Value: "text"}, Text: &ir.ConstString{Value: "£63"}, BBox: ir.BoundingBox{X0: 0, Y0: 50, X1: 40, Y1: 70}},
	}
	AttachTextWeights(img, comps, DefaultWeightOptions())
	if comps[0].Weight != nil {
		t.Error("icon component must not get a weight")
	}
	if comps[1].Weight != nil {
		t.Error("symbol-soup token must be filtered (no weight)")
	}
}

func TestAttachTextWeights_NilImage(t *testing.T) {
	if n := AttachTextWeights(nil, nil, DefaultWeightOptions()); n != 0 {
		t.Errorf("nil image: want 0, got %d", n)
	}
}
