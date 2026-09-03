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
	// EdgeBoost: strength (0..~1) of a deterministic contrast/edge-amplification
	// applied to the grayscale WORKING copy before edge detection, so faint
	// hairline card borders on near-white (light-theme) backgrounds become steep
	// enough gradients for the Sobel threshold to catch. 0 disables (no boost).
	// Only the edge-detection input is boosted; the original image (color
	// sampling, coordinates) is untouched.
	EdgeBoost float64
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
		// EdgeBoost default OFF: the always-on 0.6 boost helped light themes
		// (board-light cards 0->5 containers) but FRAGMENTED solid dark-theme card
		// borders (board-dark 9 cards -> 0, edges 41->2). It needs contrast-
		// adaptive gating (apply only on measured-low-contrast/light images) and
		// gentler strength before it can be on by default. Available as opt-in.
		EdgeBoost: 0,
	}
}

// RegionBox is a detected non-text region in ORIGINAL image coordinates.
type RegionBox struct {
	BBox       ir.BoundingBox
	AreaFrac   float64 // fraction of the image area (original space)
	FillRatio  float64 // blob pixels / bbox area (working space); 1.0 = solid
}

// RegionComponents runs DetectRegions and converts the boxes into typed
// ir.Component values (Source "cv_region"). Typing is deterministic and derived
// from geometry, containment, OCR-emptiness, and a bar/axis pattern check:
//
//   - chart:     region with a regular bar/axis pattern
//   - icon:      small, compact, text-empty graphic
//   - logo:      text-empty graphic in the header band
//   - container: encloses two or more other regions
//   - card/panel/image/region: fallbacks by fill/size/text-emptiness
//
// toks are OCR tokens (global coords) used to measure text-emptiness; pass nil
// to skip OCR-aware typing. Text is not attached here — the reconciler merges
// these boxes with OCR-derived text components by overlap.
func RegionComponents(img image.Image, opts RegionOptions, toks []ir.OCRToken) []ir.Component {
	boxes := DetectRegions(img, opts)
	if len(boxes) == 0 {
		return nil
	}
	var imgW, imgH int
	if img != nil {
		b := img.Bounds()
		imgW, imgH = b.Dx(), b.Dy()
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
		sig := regionSignals(img, rb, encloses[i], imgW, imgH, toks)
		typ := classifyRegion(rb, sig)
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

// regionSignals computes the deterministic element-typing signals for a region.
func regionSignals(img image.Image, rb RegionBox, encloses, imgW, imgH int, toks []ir.OCRToken) RegionSignals {
	w := rb.BBox.Width()
	h := rb.BBox.Height()
	maxSide, minSide := w, h
	if h > w {
		maxSide, minSide = h, w
	}
	aspect := 1.0
	if minSide > 0 {
		aspect = float64(maxSide) / float64(minSide)
	}
	headerBand := false
	if imgH > 0 {
		// Header band = region's vertical center in the top 15% of the page.
		cy := (rb.BBox.Y0 + rb.BBox.Y1) / 2
		headerBand = float64(cy) <= 0.15*float64(imgH)
	}
	return RegionSignals{
		Encloses:    encloses,
		OCROverlap:  ocrOverlapFrac(rb.BBox, toks),
		MaxSidePx:   maxSide,
		MinSidePx:   minSide,
		AspectRatio: aspect,
		HeaderBand:  headerBand,
	}
}

// ocrOverlapFrac returns the fraction of the region's area covered by OCR
// token boxes (clamped to 1.0). Used to measure text-emptiness.
func ocrOverlapFrac(region ir.BoundingBox, toks []ir.OCRToken) float64 {
	area := region.Area()
	if area <= 0 || len(toks) == 0 {
		return 0
	}
	covered := 0
	for _, t := range toks {
		covered += intersectionAreaInt(region, t.BBoxGlobal)
	}
	f := float64(covered) / float64(area)
	if f > 1 {
		f = 1
	}
	return f
}

func intersectionAreaInt(a, b ir.BoundingBox) int {
	x0 := a.X0
	if b.X0 > x0 {
		x0 = b.X0
	}
	y0 := a.Y0
	if b.Y0 > y0 {
		y0 = b.Y0
	}
	x1 := a.X1
	if b.X1 < x1 {
		x1 = b.X1
	}
	y1 := a.Y1
	if b.Y1 < y1 {
		y1 = b.Y1
	}
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	return (x1 - x0) * (y1 - y0)
}


// RegionSignals carries the deterministic signals used to type a detected
// region into a visual element. All fields are computed from measured data
// (geometry + OCR overlap) so classification is testable without an image.
type RegionSignals struct {
	Encloses    int     // number of other regions this one contains
	OCROverlap  float64 // fraction of the region's area covered by OCR text (0..1)
	MaxSidePx   int     // longest side of the region in original pixels
	MinSidePx   int     // shortest side of the region in original pixels
	AspectRatio float64 // MaxSide/MinSide (>=1)
	HeaderBand  bool    // region sits in the top band of the page (logo-likely)
}

// classifyRegion assigns a visual-element type from deterministic signals.
// Order matters: more specific element types are checked before generic
// container/card fallbacks.
//
//   - icon:      small, compact, text-empty graphic.
//   - logo:      text-empty graphic in the header band (brand mark area).
//   - container: encloses two or more other regions.
//   - card:      solid, sizeable block.
//   - panel:     sizeable region.
//   - image:     any other text-empty graphic.
//   - region:    generic fallback.
//
// NOTE: deterministic chart typing (bar/axis projection) was evaluated and
// removed: real bar charts have short, sparse bars (low column ink) while large
// text produces tall high-ink columns, so a projection heuristic both misses
// real charts and false-positives on text cards. Chart identification is
// deferred to Tier-2 grounded VLM labeling, which can recognize a chart
// visually. See docs/roadmap/gaps-vs-vision-llm.md (Gap 6).
func classifyRegion(rb RegionBox, s RegionSignals) string {
	textEmpty := s.OCROverlap < 0.05
	// Icons are discriminated by geometry (small + compact + no children).
	// OCR-emptiness is intentionally NOT required here: OCR frequently misreads
	// an icon glyph as a phantom low-value character, which would otherwise
	// suppress a genuine icon. The small+square geometry already rejects wide
	// text-line contours, so it is a sufficient discriminator.
	iconShaped := s.MaxSidePx > 0 && s.MaxSidePx <= 48 && s.AspectRatio <= 1.6 && s.Encloses == 0
	switch {
	case iconShaped:
		return "icon"
	case textEmpty && s.HeaderBand && s.Encloses == 0 && rb.FillRatio < 0.85:
		return "logo"
	case s.Encloses >= 2:
		return "container"
	case rb.FillRatio >= 0.85 && rb.AreaFrac >= 0.02:
		return "card"
	case rb.AreaFrac >= 0.02:
		return "panel"
	case textEmpty && s.Encloses == 0:
		return "image"
	default:
		return "region"
	}
}

// regionType is the geometry-only fallback classifier retained for callers/tests
// that do not supply element signals.
func regionType(rb RegionBox, encloses int) string {
	return classifyRegion(rb, RegionSignals{Encloses: encloses})
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
	// Edge-amplification (owner steer): faint hairline card borders on light/flat
	// UIs produce too weak a Sobel gradient to threshold. Boost local contrast +
	// sharpen a SEPARATE edge-detection input so those borders become steep
	// enough to survive thresholding. The fill/background mask below keeps the
	// UNBOOSTED gray (it relies on absolute luminance differences that the boost
	// would distort); color sampling/coordinates use the untouched original.
	edgeInput := gray
	if opts.EdgeBoost > 0 {
		edgeInput = boostEdges(gray, opts.EdgeBoost)
	}
	edges := effect.Sobel(edgeInput)
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
// boostEdges is a placeholder for contrast/edge-amplification of the edge-
// detection input, intended to surface faint hairline card borders on light/flat
// UIs (owner steer, Milestone A2).
//
// NOT YET IMPLEMENTED CORRECTLY — kept as a no-op. The naive approach (bild
// global adjust.Contrast + UnsharpMask) was measured and REJECTED: global
// contrast expands values around mid-gray, so a faint light border (e.g. 235 on
// a 250 background) and the background BOTH saturate toward 255, ERASING the very
// difference we need. Global contrast is the wrong primitive for faint light
// borders. The correct fix is LOCAL contrast — CLAHE (tiled/adaptive histogram
// equalization) — which bild does not provide and which is a real implementation
// (tiled histogram + interpolation), not a one-liner. Until that lands, the boost
// is a no-op (EdgeBoost defaults to 0, so this is never called in the default
// path). See the Milestone A2 roadmap entry.
func boostEdges(gray image.Image, amount float64) *image.RGBA {
	if r, ok := gray.(*image.RGBA); ok {
		return r
	}
	b := gray.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x-b.Min.X, y-b.Min.Y, gray.At(x, y))
		}
	}
	return dst
}

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
