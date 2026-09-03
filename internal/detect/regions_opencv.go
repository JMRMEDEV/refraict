// Package detect — OpenCV-backed region detector (opt-in via `-tags opencv`).
//
// This implementation targets the hard case that pure-Go bild + single-
// threshold Sobel cannot handle: very low-contrast flat UIs where card borders
// are faint and broken. OpenCV's Canny uses hysteresis (two thresholds linking
// weak edges to strong ones), which connects those faint borders into closed
// contours that findContours can turn into bounding boxes.
//
// It is built only when the `opencv` tag is set, so the default build stays
// pure-Go and statically linkable.
package detect

import (
	"image"

	"github.com/refraict/refraict/internal/ir"
	"gocv.io/x/gocv"
)

// OpenCVRegionOptions tunes the OpenCV detector.
type OpenCVRegionOptions struct {
	DownscaleLongSide int
	CannyLow          float32 // hysteresis low threshold
	CannyHigh         float32 // hysteresis high threshold
	DilateRadius      int     // seal broken edges into closed contours
	MinAreaFrac       float64
	MaxAreaFrac       float64
	MinSidePx         int
	// MinRectangularity: contourArea / boundingRectArea; higher keeps only
	// blocky (card/panel) shapes and rejects ragged/organic blobs.
	MinRectangularity float64
	// Icon band: small compact contours are kept even though they fall below
	// the main size band, so icons/logos surface. Constrained to be roughly
	// square (IconMaxAspect) and within a size range (original px), which
	// admits icon glyphs while rejecting wide text-line contours.
	IconMinSidePx int
	IconMaxSidePx int
	IconMaxAspect float64
	// CLAHEClip enables contrast-limited adaptive histogram equalization on the
	// grayscale edge-detection input when > 0. This recovers cards on light/flat
	// UIs whose interior luminance equals the page background and whose border is
	// only a few luminance units — a case no Canny threshold can close, because
	// the local step is too faint (see roadmap Milestone A2). CLAHE stretches
	// that local step into a strong edge. Clip limit ~2.0 is conservative (caps
	// noise amplification in flat regions).
	CLAHEClip float64
	// CLAHETile is the CLAHE tile grid size (e.g. 8 => 8x8 tiles). 0 => 8.
	CLAHETile int
	// CLAHEMaxStdDev gates CLAHE to LOW-CONTRAST images only: apply CLAHE only
	// when the grayscale std-dev is at/below this (light/flat UIs). High-contrast
	// (dark-theme) images already have detectable borders and CLAHE would amplify
	// their texture into noise. 0 => always apply when CLAHEClip>0 (no gate).
	CLAHEMaxStdDev float64
}

// DefaultOpenCVRegionOptions returns defaults tuned for low-contrast flat UIs.
func DefaultOpenCVRegionOptions() OpenCVRegionOptions {
	return OpenCVRegionOptions{
		DownscaleLongSide: 1400,
		CannyLow:          20,
		CannyHigh:         60,
		DilateRadius:      3,
		MinAreaFrac:       0.004,
		MaxAreaFrac:       0.60,
		MinSidePx:         24,
		MinRectangularity: 0.75,
		IconMinSidePx:     12,
		IconMaxSidePx:     44,
		IconMaxAspect:     1.6,
		CLAHEClip:         8.0,
		CLAHETile:         8,
		CLAHEMaxStdDev:    0, // unused: dual-pass unions clean + CLAHE passes
	}
}

// DetectRegionsOpenCV finds card/panel/container/icon boxes using OpenCV Canny +
// findContours. It runs TWO passes and unions the results (IoU-deduped):
//
//	pass 1 — no CLAHE: detects icons and high-contrast regions cleanly. CLAHE's
//	         local amplification can merge/erase small icon-band contours, so the
//	         icon-reliable read comes from the un-enhanced pass.
//	pass 2 — CLAHE (clip=CLAHEClip): lifts faint card borders on light/flat UIs
//	         (interior luminance == background, a few-unit border step) into
//	         closable contours — cards that no Canny threshold recovers unenhanced.
//
// This dual-pass gets cards AND icons without the single-pass tradeoff (measured:
// single-pass CLAHE recovered board-light cards 0→3 but wiped task-detail icons
// 8→0). Boxes are returned in ORIGINAL image coordinates.
func DetectRegionsOpenCV(img image.Image, opts OpenCVRegionOptions) ([]RegionBox, error) {
	// Pass 1: no enhancement (icons + high-contrast regions).
	base, err := detectRegionsOnceOpenCV(img, opts, 0)
	if err != nil {
		return nil, err
	}
	if opts.CLAHEClip <= 0 {
		return base, nil
	}
	// Pass 2: CLAHE-enhanced (faint cards on flat UIs).
	enhanced, err := detectRegionsOnceOpenCV(img, opts, opts.CLAHEClip)
	if err != nil {
		return base, nil // pass-1 result is still useful
	}
	return unionRegionBoxes(base, enhanced, 0.6), nil
}

// unionRegionBoxes merges two box sets, dropping a box from `add` when it
// overlaps an existing box (from `keep` or an earlier-added box) by >= iouThresh.
// `keep` boxes are all retained; `add` boxes only fill gaps. This preserves the
// icon-reliable pass-1 boxes while adding the CLAHE-only card boxes.
func unionRegionBoxes(keep, add []RegionBox, iouThresh float64) []RegionBox {
	out := make([]RegionBox, len(keep))
	copy(out, keep)
	for _, a := range add {
		dup := false
		for _, k := range out {
			if boxIoU(a.BBox, k.BBox) >= iouThresh {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}

// boxIoU is the intersection-over-union of two boxes.
func boxIoU(a, b ir.BoundingBox) float64 {
	ix0, iy0 := maxInt(a.X0, b.X0), maxInt(a.Y0, b.Y0)
	ix1, iy1 := minInt(a.X1, b.X1), minInt(a.Y1, b.Y1)
	if ix1 <= ix0 || iy1 <= iy0 {
		return 0
	}
	inter := float64((ix1 - ix0) * (iy1 - iy0))
	ua := float64(a.Width()*a.Height() + b.Width()*b.Height())
	if ua-inter <= 0 {
		return 0
	}
	return inter / (ua - inter)
}

// detectRegionsOnceOpenCV is a single Canny+contour detection pass. claheClip>0
// applies CLAHE to the grayscale edge input (0 = none). Returns boxes in
// ORIGINAL image coordinates.
func detectRegionsOnceOpenCV(img image.Image, opts OpenCVRegionOptions, claheClip float64) ([]RegionBox, error) {
	ob := img.Bounds()
	ow, oh := ob.Dx(), ob.Dy()
	if ow <= 0 || oh <= 0 {
		return nil, nil
	}

	src, err := gocv.ImageToMatRGB(img)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	// Downscale for speed; track scale to map boxes back.
	scale := 1.0
	work := src.Clone()
	defer work.Close()
	long := ow
	if oh > ow {
		long = oh
	}
	if opts.DownscaleLongSide > 0 && long > opts.DownscaleLongSide {
		scale = float64(opts.DownscaleLongSide) / float64(long)
		nw := int(float64(ow) * scale)
		nh := int(float64(oh) * scale)
		resized := gocv.NewMat()
		gocv.Resize(work, &resized, image.Pt(nw, nh), 0, 0, gocv.InterpolationArea)
		work.Close()
		work = resized
	}
	ww := work.Cols()
	wh := work.Rows()

	// Grayscale.
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(work, &gray, gocv.ColorBGRToGray)

	// CLAHE on the edge-detection input (pass 2 only): lifts faint local card
	// borders into closable contours. Native OpenCV CLAHE via gocv.
	if claheClip > 0 {
		tile := opts.CLAHETile
		if tile <= 0 {
			tile = 8
		}
		clahe := gocv.NewCLAHEWithParams(claheClip, image.Pt(tile, tile))
		eq := gocv.NewMat()
		clahe.Apply(gray, &eq)
		clahe.Close()
		gray.Close()
		gray = eq
	}

	// Slight blur to stabilize Canny on compression noise.
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Pt(3, 3), 0, 0, gocv.BorderDefault)

	// Canny edges (hysteresis) — the key step for faint borders.
	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, opts.CannyLow, opts.CannyHigh)

	// Dilate to seal broken edges into closed contours.
	if opts.DilateRadius > 0 {
		k := gocv.GetStructuringElement(gocv.MorphRect,
			image.Pt(opts.DilateRadius*2+1, opts.DilateRadius*2+1))
		defer k.Close()
		gocv.Dilate(edges, &edges, k)
	}

	// Find external + nested contours.
	contours := gocv.FindContours(edges, gocv.RetrievalTree, gocv.ChainApproxSimple)
	defer contours.Close()

	workArea := float64(ww * wh)
	// Icon-band thresholds are given in original px; convert to working px.
	iconMinW := int(float64(opts.IconMinSidePx) * scale)
	iconMaxW := int(float64(opts.IconMaxSidePx) * scale)
	if iconMinW < 1 {
		iconMinW = 1
	}
	var out []RegionBox
	for i := 0; i < contours.Size(); i++ {
		c := contours.At(i)
		rect := gocv.BoundingRect(c)
		bw, bh := rect.Dx(), rect.Dy()

		// A contour is kept if it passes the main size band OR the icon band.
		mainBand := bw >= opts.MinSidePx && bh >= opts.MinSidePx
		af := float64(bw*bh) / workArea
		if mainBand {
			if af < opts.MinAreaFrac || af > opts.MaxAreaFrac {
				mainBand = false
			}
		}

		iconBand := false
		if opts.IconMaxSidePx > 0 {
			mx, mn := bw, bh
			if bh > bw {
				mx, mn = bh, bw
			}
			aspect := 1.0
			if mn > 0 {
				aspect = float64(mx) / float64(mn)
			}
			iconBand = mn >= iconMinW && mx <= iconMaxW && aspect <= opts.IconMaxAspect
		}

		if !mainBand && !iconBand {
			continue
		}

		rectArea := float64(bw * bh)
		ca := gocv.ContourArea(c)
		fill := 0.0
		if rectArea > 0 {
			fill = ca / rectArea
		}
		// Rectangularity is required for main-band regions (cards/panels) but
		// not for icon-band glyphs, which are legitimately non-rectangular.
		if mainBand && !iconBand && fill < opts.MinRectangularity {
			continue
		}
		// Scale back to original coordinates.
		ox0 := int(float64(rect.Min.X) / scale)
		oy0 := int(float64(rect.Min.Y) / scale)
		ox1 := int(float64(rect.Max.X) / scale)
		oy1 := int(float64(rect.Max.Y) / scale)
		if ox1 > ow {
			ox1 = ow
		}
		if oy1 > oh {
			oy1 = oh
		}
		out = append(out, RegionBox{
			BBox:      ir.BoundingBox{X0: ox0, Y0: oy0, X1: ox1, Y1: oy1},
			AreaFrac:  float64((ox1-ox0)*(oy1-oy0)) / float64(ow*oh),
			FillRatio: fill,
		})
	}
	return filterNested(dedupeBoxesIoU(out)), nil
}

// dedupeBoxesIoU removes near-duplicate boxes. Contour trees commonly yield an
// outer and inner contour for the same rectangle (offset by the border stroke
// plus dilation); these have high IoU (~0.8+). For any pair with IoU >= 0.80 we
// keep the LARGER (outer) box, which is the true card boundary.
func dedupeBoxesIoU(boxes []RegionBox) []RegionBox {
	// Sort largest-first so we keep outer boxes and drop their inner twins.
	sorted := append([]RegionBox(nil), boxes...)
	sortByAreaDesc(sorted)
	var kept []RegionBox
	for _, b := range sorted {
		dup := false
		for _, k := range kept {
			if b.BBox.IoU(k.BBox) >= 0.80 {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, b)
		}
	}
	return kept
}

// filterNested drops boxes that are almost entirely contained in a larger box.
//
// For the NARROW case we want top-level regions (cards, panels, chart
// containers), not their internal elements. Any box that is >= 90% contained in
// a larger box is therefore treated as an internal/artifact box and dropped —
// this removes both inner-contour twins (that survived dedup) and spurious
// plot-interior boxes (e.g. chart gridline rectangles). Detecting genuine
// distinct child elements is deferred to the medium-difficulty pass.
func filterNested(boxes []RegionBox) []RegionBox {
	var out []RegionBox
	for i, b := range boxes {
		drop := false
		for j, p := range boxes {
			if i == j {
				continue
			}
			if p.BBox.Area() <= b.BBox.Area() {
				continue
			}
			containedFrac := intersectionArea(b.BBox, p.BBox) / float64(maxInt(b.BBox.Area(), 1))
			if containedFrac >= 0.9 {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, b)
		}
	}
	return out
}

func sortByAreaDesc(b []RegionBox) {
	for i := 1; i < len(b); i++ {
		for j := i; j > 0 && b[j].BBox.Area() > b[j-1].BBox.Area(); j-- {
			b[j], b[j-1] = b[j-1], b[j]
		}
	}
}

func intersectionArea(a, b ir.BoundingBox) float64 {
	x0 := maxInt(a.X0, b.X0)
	y0 := maxInt(a.Y0, b.Y0)
	x1 := minInt(a.X1, b.X1)
	y1 := minInt(a.Y1, b.Y1)
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	return float64((x1 - x0) * (y1 - y0))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// RegionComponentsOpenCV runs the OpenCV detector and types the boxes using the
// same rules as the pure-Go path, including OCR-aware element typing
// (icon/logo/chart). Pass nil toks to skip OCR-aware typing.
func RegionComponentsOpenCV(img image.Image, opts OpenCVRegionOptions, toks []ir.OCRToken) ([]ir.Component, error) {
	boxes, err := DetectRegionsOpenCV(img, opts)
	if err != nil {
		return nil, err
	}
	if len(boxes) == 0 {
		return nil, nil
	}
	var imgW, imgH int
	if img != nil {
		b := img.Bounds()
		imgW, imgH = b.Dx(), b.Dy()
	}
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
			ID:         regionID(i),
			Type:       ir.ConstString{Value: typ, Source: "cv_region_opencv", Confidence: conf},
			BBox:       rb.BBox,
			Confidence: conf,
			Source:     "cv_region_opencv",
		})
	}
	return comps, nil
}
