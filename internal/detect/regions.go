package detect

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"

	"github.com/anthonynsimon/bild/effect"
	"github.com/anthonynsimon/bild/segment"
	"github.com/refraict/refraict/internal/ir"
)

// RegionOptions tunes the connected-components region detector.
type RegionOptions struct {
	// DownscaleLongSide caps the working image's long side for speed and noise
	// reduction. Detected boxes are scaled back to original coordinates. 0
	// disables downscaling.
	DownscaleLongSide int
	// ForegroundDelta: a pixel is foreground when its grayscale value differs
	// from the estimated background by at least this amount (0..255).
	ForegroundDelta int
	// EdgeThreshold: Sobel edge-magnitude threshold (0..255). Lower catches
	// fainter card borders (at the cost of more noise).
	EdgeThreshold int
	// CloseRadius: dilation+erosion radius (working px) used to seal card
	// borders into filled blobs. This is the key knob for low-contrast cards.
	CloseRadius int
	// MorphRadius: dilation/erosion radius (px, in working-image space) used to
	// close gaps so a filled card becomes one solid blob. 0 disables.
	MorphRadius int
	// MinAreaFrac: drop blobs whose area is below this fraction of the working
	// image area (noise/tiny specks).
	MinAreaFrac float64
	// MaxAreaFrac: drop blobs whose area exceeds this fraction (the page
	// background itself).
	MaxAreaFrac float64
	// MinSidePx: drop blobs whose width or height (working space) is below this.
	MinSidePx int
}

// DefaultRegionOptions returns conservative defaults tuned for flat, modern
// UIs (solid-fill cards/panels on a near-uniform background).
func DefaultRegionOptions() RegionOptions {
	return RegionOptions{
		DownscaleLongSide: 1000,
		ForegroundDelta:   40,
		EdgeThreshold:     24,
		CloseRadius:       3,
		MorphRadius:       1,
		MinAreaFrac:       0.002,
		MaxAreaFrac:       0.60,
		MinSidePx:         12,
	}
}

// RegionBox is a detected non-text region in ORIGINAL image coordinates.
type RegionBox struct {
	BBox       ir.BoundingBox
	AreaFrac   float64 // fraction of the image area (original space)
	FillRatio  float64 // blob pixels / bbox area (working space); 1.0 = solid
}

// RegionComponents runs DetectRegions and converts the boxes into typed
// ir.Component values (Source "cv_region"). Typing is conservative and derived
// only from geometry and containment:
//
//   - a region that encloses two or more other regions => "container"
//   - a large solid region (high fill) => "card"/"panel"
//   - otherwise => "region"
//
// Text is intentionally not attached here; the pipeline's reconciler merges
// these boxes with OCR-derived text components by overlap.
func RegionComponents(img image.Image, opts RegionOptions) []ir.Component {
	boxes := DetectRegions(img, opts)
	if len(boxes) == 0 {
		return nil
	}
	// Precompute containment counts.
	encloses := make([]int, len(boxes))
	for i := range boxes {
		for j := range boxes {
			if i == j {
				continue
			}
			if boxes[i].BBox.Contains(boxes[j].BBox) && boxes[i].BBox != boxes[j].BBox {
				encloses[i]++
			}
		}
	}

	comps := make([]ir.Component, 0, len(boxes))
	for i, rb := range boxes {
		typ := regionType(rb, encloses[i])
		conf := regionConfidence(rb, encloses[i])
		comps = append(comps, ir.Component{
			ID:   regionID(i),
			Type: ir.ConstString{Value: typ, Source: "cv_region", Confidence: conf},
			BBox: rb.BBox,
			Appearance: nil,
			Confidence: conf,
			Source:     "cv_region",
		})
	}
	return comps
}

func regionType(rb RegionBox, encloses int) string {
	switch {
	case encloses >= 2:
		return "container"
	case rb.FillRatio >= 0.85 && rb.AreaFrac >= 0.02:
		// Solid, sizeable block: a card/panel.
		return "card"
	case rb.AreaFrac >= 0.02:
		return "panel"
	default:
		return "region"
	}
}

func regionConfidence(rb RegionBox, encloses int) float64 {
	// Solid, well-formed rectangles are more trustworthy than ragged blobs.
	c := 0.5
	if rb.FillRatio >= 0.85 {
		c += 0.2
	}
	if encloses >= 2 {
		c += 0.1
	}
	if c > 0.9 {
		c = 0.9
	}
	return c
}

func regionID(i int) string {
	return "r" + pad4detect(i+1)
}

func pad4detect(n int) string {
	digits := []byte{}
	if n == 0 {
		digits = []byte{'0'}
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	for len(digits) < 4 {
		digits = append([]byte{'0'}, digits...)
	}
	return string(digits)
}

// DetectRegions finds non-text rectangular regions (cards, panels, containers,
// chart areas) in an image using bild preprocessing plus a connected-components
// pass. The connected-components labeling is a standard two-pass union-find
// implementation written from the published method (not copied from any
// library), operating on a foreground/background mask.
//
// It is deterministic and CPU-only; no model or GPU is involved.
func DetectRegions(img image.Image, opts RegionOptions) []RegionBox {
	if img == nil {
		return nil
	}
	ob := img.Bounds()
	ow, oh := ob.Dx(), ob.Dy()
	if ow <= 0 || oh <= 0 {
		return nil
	}

	// 1. Downscale for speed/noise (boxes scaled back at the end).
	work, scale := downscale(img, opts.DownscaleLongSide)
	wb := work.Bounds()
	ww, wh := wb.Dx(), wb.Dy()

	// 2. Edge-based mask. Low-contrast flat UIs (faint cards on a near-uniform
	// background) defeat fill-difference thresholding, but card BORDERS still
	// produce a gradient. We compute Sobel edge magnitude, threshold it, then
	// dilate so the (thin, possibly broken) border of each card fuses into a
	// filled blob whose bounding box is the card. A background-difference mask
	// is unioned in so high-contrast solid blocks (banners, active pills) are
	// also captured.
	gray := effect.Grayscale(work)
	edges := effect.Sobel(gray)
	edgeMask := thresholdGray(edges, opts.EdgeThreshold)

	bg := estimateBackgroundGray(gray)
	fillMask := foregroundMask(gray, bg, opts.ForegroundDelta)
	mask := unionMask(edgeMask, fillMask)

	// Dilate to seal broken borders and fill card interiors, then a matching
	// erode to recover the true size (morphological close).
	if opts.CloseRadius > 0 {
		r := float64(opts.CloseRadius)
		mask = effect.Erode(effect.Dilate(mask, r), r)
	}

	if dbg := debugMaskPath(); dbg != "" {
		_ = savePNG(dbg, mask)
	}

	// 3. Light opening to drop 1px speckle without erasing sealed cards.
	if opts.MorphRadius > 0 {
		mask = effect.Dilate(effect.Erode(mask, 1.0), 1.0)
	}

	// 4. Connected-components labeling (two-pass union-find) on the mask.
	_, bounds, areas := connectedComponents(mask)

	// 5. Extract + filter boxes.
	workArea := float64(ww * wh)
	var out []RegionBox
	for lbl, bb := range bounds {
		area := float64(areas[lbl])
		af := area / workArea
		if af < opts.MinAreaFrac || af > opts.MaxAreaFrac {
			continue
		}
		bw := bb.Max.X - bb.Min.X
		bh := bb.Max.Y - bb.Min.Y
		if bw < opts.MinSidePx || bh < opts.MinSidePx {
			continue
		}
		bboxArea := float64(bw * bh)
		fill := 0.0
		if bboxArea > 0 {
			fill = area / bboxArea
		}
		// Scale box back to original coordinates.
		ox0 := int(float64(bb.Min.X) / scale)
		oy0 := int(float64(bb.Min.Y) / scale)
		ox1 := int(float64(bb.Max.X) / scale)
		oy1 := int(float64(bb.Max.Y) / scale)
		if ox1 > ow {
			ox1 = ow
		}
		if oy1 > oh {
			oy1 = oh
		}
		out = append(out, RegionBox{
			BBox:      ir.BoundingBox{X0: ox0, Y0: oy0, X1: ox1, Y1: oy1},
			AreaFrac:  (float64((ox1 - ox0) * (oy1 - oy0))) / float64(ow*oh),
			FillRatio: fill,
		})
	}
	// Stable order: larger regions first (containers before their children).
	sort.Slice(out, func(i, j int) bool {
		if out[i].AreaFrac != out[j].AreaFrac {
			return out[i].AreaFrac > out[j].AreaFrac
		}
		if out[i].BBox.Y0 != out[j].BBox.Y0 {
			return out[i].BBox.Y0 < out[j].BBox.Y0
		}
		return out[i].BBox.X0 < out[j].BBox.X0
	})
	return out
}

// downscale returns a resized copy (long side <= maxLong) and the scale factor
// applied (working = original * scale). If maxLong <= 0 or the image already
// fits, returns the image unchanged with scale 1.
func downscale(img image.Image, maxLong int) (image.Image, float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > w {
		long = h
	}
	if maxLong <= 0 || long <= maxLong {
		return img, 1.0
	}
	scale := float64(maxLong) / float64(long)
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// Simple nearest/area resize via bild-free box sampling would add code; use
	// a straightforward scaled draw through the standard library-friendly path.
	scaleDraw(dst, img)
	return dst, float64(nw) / float64(w)
}

// scaleDraw performs a simple nearest-neighbor scale of src into dst.
func scaleDraw(dst *image.RGBA, src image.Image) {
	db := dst.Bounds()
	sb := src.Bounds()
	dw, dh := db.Dx(), db.Dy()
	sw, sh := sb.Dx(), sb.Dy()
	for y := 0; y < dh; y++ {
		sy := sb.Min.Y + y*sh/dh
		for x := 0; x < dw; x++ {
			sx := sb.Min.X + x*sw/dw
			dst.Set(db.Min.X+x, db.Min.Y+y, src.At(sx, sy))
		}
	}
}

// estimateBackgroundGray estimates the page background as the MODE (most
// frequent) gray level across the whole image. Backgrounds dominate pixel
// counts in typical UIs, so the histogram peak is a robust estimate — more so
// than edge sampling, which can be contaminated by sidebars/banners.
func estimateBackgroundGray(gray image.Image) int {
	b := gray.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return 0
	}
	var hist [256]int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			hist[grayAt(gray, b.Min.X+x, b.Min.Y+y)]++
		}
	}
	mode, best := 0, -1
	for v, n := range hist {
		if n > best {
			best = n
			mode = v
		}
	}
	return mode
}

func grayAt(img image.Image, x, y int) int {
	c := color.GrayModel.Convert(img.At(x, y)).(color.Gray)
	return int(c.Y)
}

// foregroundMask returns a binary (thresholded) image where pixels differing
// from the background gray by >= delta are foreground (white). It uses bild's
// segment.Threshold after remapping difference into intensity.
func foregroundMask(gray image.Image, bg, delta int) *image.RGBA {
	b := gray.Bounds()
	w, h := b.Dx(), b.Dy()
	diff := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g := grayAt(gray, b.Min.X+x, b.Min.Y+y)
			d := g - bg
			if d < 0 {
				d = -d
			}
			v := uint8(0)
			if d >= delta {
				v = 255
			}
			diff.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	// segment.Threshold gives a clean binary image (idempotent here but keeps
	// the mask in the exact form the morphology ops expect).
	return toRGBA(segment.Threshold(diff, 128))
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// thresholdGray returns a binary RGBA mask: pixels whose grayscale magnitude is
// >= t become white (foreground), else black.
func thresholdGray(img image.Image, t int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g := grayAt(img, b.Min.X+x, b.Min.Y+y)
			v := uint8(0)
			if g >= t {
				v = 255
			}
			dst.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return dst
}

// unionMask returns the pixel-wise OR of two binary masks (same size).
func unionMask(a, b *image.RGBA) *image.RGBA {
	bounds := a.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(0)
			if a.RGBAAt(x, y).R > 127 || b.RGBAAt(x, y).R > 127 {
				v = 255
			}
			dst.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return dst
}

// connectedComponents performs two-pass union-find labeling on a binary mask
// (foreground = any channel > 127). Returns per-label bounding rectangles and
// pixel areas keyed by final root label. 4-connectivity.
//
// This is a standard textbook two-pass algorithm implemented from its published
// description; it is original code, not copied from any CV library.
func connectedComponents(mask *image.RGBA) (labels []int, bounds map[int]image.Rectangle, areas map[int]int) {
	b := mask.Bounds()
	w, h := b.Dx(), b.Dy()
	labels = make([]int, w*h)

	uf := newUnionFind()
	next := 1

	isFg := func(x, y int) bool {
		c := mask.RGBAAt(b.Min.X+x, b.Min.Y+y)
		return c.R > 127
	}

	// Pass 1: provisional labels + equivalences.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !isFg(x, y) {
				continue
			}
			left := 0
			up := 0
			if x > 0 {
				left = labels[y*w+(x-1)]
			}
			if y > 0 {
				up = labels[(y-1)*w+x]
			}
			switch {
			case left == 0 && up == 0:
				labels[y*w+x] = next
				uf.makeSet(next)
				next++
			case left != 0 && up == 0:
				labels[y*w+x] = left
			case left == 0 && up != 0:
				labels[y*w+x] = up
			default:
				labels[y*w+x] = left
				if left != up {
					uf.union(left, up)
				}
			}
		}
	}

	// Pass 2: resolve to roots, accumulate bounds/areas.
	bounds = map[int]image.Rectangle{}
	areas = map[int]int{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			l := labels[y*w+x]
			if l == 0 {
				continue
			}
			root := uf.find(l)
			labels[y*w+x] = root
			areas[root]++
			if r, ok := bounds[root]; ok {
				if x < r.Min.X {
					r.Min.X = x
				}
				if y < r.Min.Y {
					r.Min.Y = y
				}
				if x+1 > r.Max.X {
					r.Max.X = x + 1
				}
				if y+1 > r.Max.Y {
					r.Max.Y = y + 1
				}
				bounds[root] = r
			} else {
				bounds[root] = image.Rect(x, y, x+1, y+1)
			}
		}
	}
	return labels, bounds, areas
}

// unionFind is a minimal disjoint-set with path compression and union by rank.
type unionFind struct {
	parent map[int]int
	rank   map[int]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[int]int{}, rank: map[int]int{}}
}

func (u *unionFind) makeSet(x int) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
		u.rank[x] = 0
	}
}

func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]] // path compression (halving)
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}

// debugMaskPath returns REFRAICT_DEBUG_MASK if set, enabling a mask dump.
func debugMaskPath() string { return os.Getenv("REFRAICT_DEBUG_MASK") }

// savePNG writes an image to disk (debug only).
func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
