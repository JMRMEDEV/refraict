// Package detect provides deterministic non-text region detection and typing.
//
// Region DETECTION is OpenCV-backed (see regions_opencv.go); OpenCV 4.x is a
// hard dependency of refraict. This file holds the shared, image-library-free
// region TYPES and TYPING helpers (RegionBox + the deterministic signal/
// classification functions) that turn detected boxes into typed ir.Components.
package detect

import (
	"image"

	"github.com/refraict/refraict/internal/ir"
)

// RegionBox is a detected non-text region in ORIGINAL image coordinates plus
// deterministic shape signals used for typing.
type RegionBox struct {
	BBox      ir.BoundingBox
	AreaFrac  float64 // fraction of the image area (original space)
	FillRatio float64 // blob pixels / bbox area (working space); 1.0 = solid
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

// ocrOverlapFrac returns the fraction of the region's area covered by OCR token
// boxes (clamped to 1.0). Used to measure text-emptiness.
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
//   - icon:      small, compact graphic.
//   - logo:      text-empty graphic in the header band (brand mark area).
//   - container: encloses two or more other regions.
//   - card:      solid, sizeable block.
//   - panel:     sizeable region.
//   - image:     any other text-empty graphic.
//   - region:    generic fallback.
//
// NOTE: deterministic chart typing was evaluated and removed (unreliable);
// chart identification is deferred to Tier-2 grounded VLM labeling. See
// docs/roadmap/gaps-vs-vision-llm.md (Gap 6).
func classifyRegion(rb RegionBox, s RegionSignals) string {
	textEmpty := s.OCROverlap < 0.05
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
