//go:build opencv

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
	}
}

// DetectRegionsOpenCV finds card/panel/container boxes using OpenCV Canny +
// findContours. Returns boxes in ORIGINAL image coordinates.
func DetectRegionsOpenCV(img image.Image, opts OpenCVRegionOptions) ([]RegionBox, error) {
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
	var out []RegionBox
	for i := 0; i < contours.Size(); i++ {
		c := contours.At(i)
		rect := gocv.BoundingRect(c)
		bw, bh := rect.Dx(), rect.Dy()
		if bw < opts.MinSidePx || bh < opts.MinSidePx {
			continue
		}
		rectArea := float64(bw * bh)
		af := rectArea / workArea
		if af < opts.MinAreaFrac || af > opts.MaxAreaFrac {
			continue
		}
		ca := gocv.ContourArea(c)
		fill := 0.0
		if rectArea > 0 {
			fill = ca / rectArea
		}
		if fill < opts.MinRectangularity {
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
// same conservative rules as the pure-Go path.
func RegionComponentsOpenCV(img image.Image, opts OpenCVRegionOptions) ([]ir.Component, error) {
	boxes, err := DetectRegionsOpenCV(img, opts)
	if err != nil {
		return nil, err
	}
	if len(boxes) == 0 {
		return nil, nil
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
		typ := regionType(rb, encloses[i])
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
