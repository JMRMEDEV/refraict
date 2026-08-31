// Package crop plans the decomposition of a large screenshot into overlapping
// crops for analysis by a small VLM, informed by OCR density and whitespace/
// edge signals from the image.
package crop

import (
	"image"
	"sort"

	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
)

// Crop describes one proposed crop region in global coordinates.
type Crop struct {
	ID    string         `json:"id"`
	BBox  ir.BoundingBox `json:"bbox"`
	Level int            `json:"level"` // 0=overview,1=section,2=detail
}

// Plan holds the set of crops to analyze.
type Plan struct {
	Crops []Crop `json:"crops"`
}

// CropPlanConfig controls crop generation.
type CropPlanConfig struct {
	CropLongSide int
	Overlap      float64
	Rect         image.Rectangle
}

// PlanFixed generates an overlapping fixed tile grid covering the whole image.
// This is the baseline benchmark strategy.
func PlanFixed(w, h, cropSide int, overlap float64) []Crop {
	var crops []Crop
	stepX := int(float64(cropSide) * (1 - overlap))
	if stepX < 1 {
		stepX = 1
	}
	stepY := int(float64(cropSide) * (1 - overlap))
	if stepY < 1 {
		stepY = 1
	}
	id := 0
	for y0 := 0; y0 < h; y0 += stepY {
		for x0 := 0; x0 < w; x0 += stepX {
			x1 := x0 + cropSide
			y1 := y0 + cropSide
			if x1 > w {
				x1 = w
			}
			if y1 > h {
				y1 = h
			}
			if x1 <= x0 || y1 <= y0 {
				continue
			}
			id++
			lvl := 1
			crops = append(crops, Crop{ID: cID(id), BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}, Level: lvl})
		}
	}
	return crops
}

// BuildPlan assembles a multi-scale crop plan: an overview crop covering the
// whole page plus section crops derived from OCR density, subdivided to stay
// within the target long side.
func BuildPlan(im *imageproc.Image, toks []ir.ORCToken, cfg CropPlanConfig) *Plan {
	w, h := im.Bounds()
	var p Plan
	p.Crops = append(p.Crops, Crop{ID: "ov", BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: w, Y1: h}, Level: 0})
	regions := HorizontalPartition(im, toks, image.Rect(0, 0, w, h))
	for _, reg := range regions {
		sub := SubdivideRegion(im, toks, reg, cfg, 0)
		p.Crops = append(p.Crops, sub...)
	}
	return &p
}

// HorizontalPartition slices the image into vertical bands separated by
// whitespace gaps in OCR token density.
func HorizontalPartition(im *imageproc.Image, toks []ir.ORCToken, rect image.Rectangle) []ir.BoundingBox {
	if len(toks) == 0 {
		return []ir.BoundingBox{{X0: rect.Min.X, Y0: rect.Min.Y, X1: rect.Max.X, Y1: rect.Max.Y}}
	}
	// Use token top coordinates to find clustering gaps.
	ys := make([]int, 0, len(toks))
	ymap := map[int]ir.ORCToken{}
	for _, t := range toks {
		ys = append(ys, t.BBoxGlobal.Y0)
		ymap[t.BBoxGlobal.Y0] = t
	}
	sort.Ints(ys)
	_ = ymap
	// Group tokens into rows.
	rows := groupTokensByRow(toks)
	var bounds []ir.BoundingBox
	var prevBottom = rect.Min.Y
	for _, row := range rows {
		top := rect.Max.Y
		bottom := rect.Min.Y
		for _, t := range row {
			if t.BBoxGlobal.Y0 < top {
				top = t.BBoxGlobal.Y0
			}
			if t.BBoxGlobal.Y1 > bottom {
				bottom = t.BBoxGlobal.Y1
			}
		}
		if top > prevBottom+1 {
			bounds = append(bounds, ir.BoundingBox{X0: rect.Min.X, Y0: prevBottom, X1: rect.Max.X, Y1: top})
		}
		prevBottom = bottom
	}
	if prevBottom < rect.Max.Y {
		bounds = append(bounds, ir.BoundingBox{X0: rect.Min.X, Y0: prevBottom, X1: rect.Max.X, Y1: rect.Max.Y})
	}
	if len(bounds) == 0 {
		return []ir.BoundingBox{{X0: rect.Min.X, Y0: rect.Min.Y, X1: rect.Max.X, Y1: rect.Max.Y}}
	}
	return bounds
}

// groupTokensByRow groups OCR tokens into rows based on vertical overlap.
func groupTokensByRow(toks []ir.ORCToken) [][]ir.ORCToken {
	sorted := append([]ir.ORCToken(nil), toks...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].BBoxGlobal.Y0 < sorted[j].BBoxGlobal.Y0
	})
	var rows [][]ir.ORCToken
	for _, t := range sorted {
		placed := false
		for i := range rows {
			if rowOverlaps(rows[i], t.BBoxGlobal) {
				rows[i] = append(rows[i], t)
				placed = true
				break
			}
		}
		if !placed {
			rows = append(rows, []ir.ORCToken{t})
		}
	}
	return rows
}

func rowOverlaps(row []ir.ORCToken, b ir.BoundingBox) bool {
	for _, r := range row {
		if r.BBoxGlobal.Y0 < b.Y1 && b.Y0 < r.BBoxGlobal.Y1 {
			return true
		}
	}
	return false
}

// SubdivideRegion splits a region into overlapping crops whose longest side
// stays under the target.
func SubdivideRegion(im *imageproc.Image, toks []ir.ORCToken, reg ir.BoundingBox, cfg CropPlanConfig, depth int) []Crop {
	_ = im
	_ = depth
	if reg.X1-reg.X0 <= cfg.CropLongSide && reg.Y1-reg.Y0 <= cfg.CropLongSide {
		return []Crop{{ID: cID(regIndex(reg)), BBox: reg, Level: 1}}
	}
	side := cfg.CropLongSide
	step := int(float64(side) * (1 - cfg.Overlap))
	if step < 1 {
		step = 1
	}
	var out []Crop
	id := 0
	for y0 := reg.Y0; y0 < reg.Y1; y0 += step {
		y1 := y0 + side
		if y1 > reg.Y1 {
			y1 = reg.Y1
		}
		for x0 := reg.X0; x0 < reg.X1; x0 += step {
			x1 := x0 + side
			if x1 > reg.X1 {
				x1 = reg.X1
			}
			if x1 <= x0 || y1 <= y0 {
				continue
			}
			id++
			out = append(out, Crop{ID: subID(reg, id), BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}, Level: 2})
		}
	}
	return out
}

func cID(n int) string {
	return "c" + pad4(n)
}

func subID(reg ir.BoundingBox, n int) string {
	return "c" + pad4(regIndex(reg)*1000+n)
}

func pad4(n int) string {
	if n < 0 {
		n = 0
	}
	if n < 10 {
		return "000" + itoa(n)
	}
	if n < 100 {
		return "00" + itoa(n)
	}
	if n < 1000 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func regIndex(reg ir.BoundingBox) int {
	return reg.X0 + reg.Y0*100000
}

// MedianTextHeight computes the median height of OCR text tokens.
func MedianTextHeight(toks []ir.ORCToken) float64 {
	if len(toks) == 0 {
		return 0
	}
	heights := make([]float64, 0, len(toks))
	for _, t := range toks {
		heights = append(heights, float64(t.BBoxGlobal.Y1-t.BBoxGlobal.Y0))
	}
	sort.Float64s(heights)
	return heights[len(heights)/2]
}

// NeedsSubdivision reports whether resizing a region to targetLong would reduce
// the median text height below the minimum threshold.
func NeedsSubdivision(regionH int, medianTextH float64, targetLong, minTextH int) bool {
	if regionH <= 0 {
		return false
	}
	scale := float64(targetLong) / float64(regionH)
	resized := medianTextH * scale
	return resized < float64(minTextH)
}
