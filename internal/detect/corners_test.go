package detect

import (
	"image"
	"image/color"
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, c)
		}
	}
}

func cardComp(x0, y0, x1, y1 int) ir.Component {
	return ir.Component{
		ID:   "r1",
		Type: ir.ConstString{Value: "card"},
		BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1},
	}
}

func TestCornerStyleSquare(t *testing.T) {
	bg := color.RGBA{37, 36, 33, 255}
	fill := color.RGBA{90, 92, 94, 255}
	img := image.NewRGBA(image.Rect(0, 0, 200, 160))
	fillRect(img, 0, 0, 200, 160, bg)
	fillRect(img, 20, 20, 180, 140, fill) // hard square card
	comps := []ir.Component{cardComp(20, 20, 180, 140)}
	if n := AttachCornerStyles(img, comps); n != 1 {
		t.Fatalf("expected 1 corner-style, got %d", n)
	}
	cs := comps[0].CornerStyle
	if cs == nil || cs.Style != "square" {
		t.Fatalf("expected square, got %+v", cs)
	}
}

func TestCornerStyleRounded(t *testing.T) {
	bg := color.RGBA{37, 36, 33, 255}
	fill := color.RGBA{90, 92, 94, 255}
	img := image.NewRGBA(image.Rect(0, 0, 200, 160))
	fillRect(img, 0, 0, 200, 160, bg)
	fillRect(img, 20, 20, 180, 140, fill)
	// clip the 4 corners back to bg (rounded)
	r := 16
	for dy := 0; dy < r; dy++ {
		for dx := 0; dx < r; dx++ {
			if (dx-r)*(dx-r)+(dy-r)*(dy-r) > r*r {
				img.Set(20+dx, 20+dy, bg)
				img.Set(179-dx, 20+dy, bg)
				img.Set(20+dx, 139-dy, bg)
				img.Set(179-dx, 139-dy, bg)
			}
		}
	}
	comps := []ir.Component{cardComp(20, 20, 180, 140)}
	AttachCornerStyles(img, comps)
	cs := comps[0].CornerStyle
	if cs == nil || cs.Style != "rounded" {
		t.Fatalf("expected rounded, got %+v", cs)
	}
	if cs.RoundedCorners < 3 {
		t.Fatalf("expected >=3 rounded corners, got %d", cs.RoundedCorners)
	}
}

func TestCornerStyleLowContrastWithholds(t *testing.T) {
	// interior fill == background (zero fill contrast): the test is meaningless
	// and must WITHHOLD (no CornerStyle attached).
	bg := color.RGBA{245, 245, 245, 255}
	img := image.NewRGBA(image.Rect(0, 0, 200, 160))
	fillRect(img, 0, 0, 200, 160, bg)
	fillRect(img, 20, 20, 180, 140, bg) // same as bg
	comps := []ir.Component{cardComp(20, 20, 180, 140)}
	AttachCornerStyles(img, comps)
	if comps[0].CornerStyle != nil {
		t.Fatalf("expected withheld corner-style on zero-contrast card, got %+v", comps[0].CornerStyle)
	}
}

func TestCornerStyleSkipsNonContainers(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	comps := []ir.Component{
		{ID: "t1", Type: ir.ConstString{Value: "text"}, BBox: ir.BoundingBox{X0: 10, Y0: 10, X1: 90, Y1: 40}},
		{ID: "i1", Type: ir.ConstString{Value: "icon"}, BBox: ir.BoundingBox{X0: 10, Y0: 50, X1: 40, Y1: 80}},
	}
	if n := AttachCornerStyles(img, comps); n != 0 {
		t.Fatalf("expected 0 (text/icon skipped), got %d", n)
	}
}
