package detect

import (
	"image"
	"image/color"

	"github.com/refraict/refraict/internal/ir"
)

// cornerStyleTypes are the component types worth a corner-style check — filled
// containers with borders. Icons/text/logos are excluded.
var cornerStyleTypes = map[string]bool{
	"card": true, "region": true, "panel": true, "container": true,
}

// AttachCornerStyles measures each card/region/panel component's corners as
// rounded or square (Milestone F) and attaches ir.CornerStyle. Deterministic,
// no model: at a ROUNDED corner the fill is clipped away so the corner pixel
// shows the page background; at a SQUARE corner it shows the region fill. Votes
// across the 4 corners (>=3 rounded => "rounded"). Returns the count attached.
//
// Guards: samples a few px in from the true corner (anti-aliasing); requires the
// region interior and the surrounding background to be measurably DIFFERENT
// (else the test is meaningless — the zero-fill-contrast light-theme case) and
// skips with no attachment when they are too close.
func AttachCornerStyles(img image.Image, comps []ir.Component) int {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	n := 0
	for i := range comps {
		c := &comps[i]
		if c.CornerStyle != nil || !cornerStyleTypes[c.Type.Value] {
			continue
		}
		box := c.BBox
		w, h := box.X1-box.X0, box.Y1-box.Y0
		if w < 24 || h < 24 {
			continue // too small to sample corners reliably
		}
		if cs := cornerStyle(img, b, box); cs != nil {
			c.CornerStyle = cs
			n++
		}
	}
	return n
}

const (
	cornerInset   = 3  // px in from the true corner (skip the anti-aliased edge)
	cornerBGProbe = 6  // px outside the region for the background reference
	minFillBGSep  = 30 // min interior-vs-bg color distance to trust the test
)

func cornerStyle(img image.Image, bounds image.Rectangle, box ir.BoundingBox) *ir.CornerStyle {
	// Interior reference: median-ish sample well inside the region.
	inx0, iny0 := box.X0+cornerInset*4, box.Y0+cornerInset*4
	inx1, iny1 := box.X1-cornerInset*4, box.Y1-cornerInset*4
	if inx1 <= inx0 || iny1 <= iny0 {
		return nil
	}
	interior := avgColor(img, bounds, inx0, iny0, inx1, iny1)
	// Background reference: just above the top edge (fall back to just below).
	bgx := (box.X0 + box.X1) / 2
	bg, ok := pixel(img, bounds, bgx, box.Y0-cornerBGProbe)
	if !ok {
		bg, ok = pixel(img, bounds, bgx, box.Y1+cornerBGProbe)
	}
	if !ok {
		return nil
	}
	// Guard: interior and background must be distinguishable, else the corner
	// test can't mean anything (zero-fill-contrast case).
	if colorDist(interior, bg) < minFillBGSep {
		return nil
	}
	corners := [4][2]int{
		{box.X0 + cornerInset, box.Y0 + cornerInset}, // TL
		{box.X1 - 1 - cornerInset, box.Y0 + cornerInset}, // TR
		{box.X0 + cornerInset, box.Y1 - 1 - cornerInset}, // BL
		{box.X1 - 1 - cornerInset, box.Y1 - 1 - cornerInset}, // BR
	}
	rounded := 0
	var sep float64
	for _, cxy := range corners {
		px, ok := pixel(img, bounds, cxy[0], cxy[1])
		if !ok {
			continue
		}
		dInt := colorDist(px, interior)
		dBG := colorDist(px, bg)
		if dBG < dInt { // looks more like background => corner clipped => rounded
			rounded++
		}
		// separation magnitude feeds confidence
		if d := dInt - dBG; d > 0 {
			sep += float64(d)
		} else {
			sep += float64(-d)
		}
	}
	style := "square"
	if rounded >= 3 {
		style = "rounded"
	} else if rounded == 2 {
		// ambiguous — withhold rather than guess.
		return nil
	}
	// Confidence: mean corner separation normalized against the interior-bg
	// separation (how decisive each corner was), clamped.
	base := colorDist(interior, bg)
	conf := 0.5
	if base > 0 {
		conf = (sep / 4.0) / float64(base)
	}
	if conf > 1 {
		conf = 1
	}
	if conf < 0.1 {
		conf = 0.1
	}
	return &ir.CornerStyle{Style: style, Confidence: conf, RoundedCorners: rounded}
}

func pixel(img image.Image, b image.Rectangle, x, y int) ([3]int, bool) {
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
		return [3]int{}, false
	}
	c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	return [3]int{int(c.R), int(c.G), int(c.B)}, true
}

func avgColor(img image.Image, b image.Rectangle, x0, y0, x1, y1 int) [3]int {
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	var rs, gs, bs, cnt int
	// sample on a coarse grid for speed
	stepX := (x1 - x0) / 16
	stepY := (y1 - y0) / 16
	if stepX < 1 {
		stepX = 1
	}
	if stepY < 1 {
		stepY = 1
	}
	for y := y0; y < y1; y += stepY {
		for x := x0; x < x1; x += stepX {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			rs += int(c.R)
			gs += int(c.G)
			bs += int(c.B)
			cnt++
		}
	}
	if cnt == 0 {
		return [3]int{}
	}
	return [3]int{rs / cnt, gs / cnt, bs / cnt}
}

func colorDist(a, b [3]int) int {
	return absI(a[0]-b[0]) + absI(a[1]-b[1]) + absI(a[2]-b[2])
}

func absI(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
