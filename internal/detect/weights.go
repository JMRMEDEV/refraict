package detect

import (
	"image"
	"sort"

	"github.com/refraict/refraict/internal/ir"
	"gocv.io/x/gocv"
	"golang.org/x/image/draw"
)

// Bold / font-weight detection (Milestone I).
//
// Tesseract exposes no font weight, and small VLMs cannot perceive it, so
// refraict measures stroke thickness deterministically from pixels and compares
// each text token to the PAGE'S OWN regular-body baseline (self-calibrating — no
// synthetic reference font, which fails on cross-font calibration). Validated
// recipe (see docs/roadmap Milestone I):
//
//  1. Candidates: text components only (icons/logos/charts are typed by the
//     region detector and excluded for free), passing a letter-ratio "wordy"
//     gate that rejects symbol soup OCR hallucinates from inline glyphs.
//  2. Per-token stroke width via distance-transform SWT on an UPSCALED crop with
//     an AA-aware grayscale UNSHARP before Otsu — the unsharp thins anti-aliased
//     regular strokes more than solid bold cores, widening the class gap.
//  3. Classify vs the page body baseline (median stroke of body-tier tokens):
//     heavy if stroke >= baseline*HeavyFactor; withhold (no attachment) in an
//     uncertain band just below; else regular. A stroke-sanity ceiling drops
//     icon-contaminated tokens (stroke >> baseline).

// WeightOptions tunes bold/weight detection.
type WeightOptions struct {
	// Upscale factor for each token crop before measuring (screenshot glyphs are
	// only a few px of stroke; measuring needs more pixels).
	Upscale int
	// Sharpen is the AA-aware unsharp amount applied to the grayscale before
	// Otsu. The key separator (0 disables it, collapsing the class gap).
	Sharpen float64
	// MinHeight skips tokens shorter than this (px) — too little stroke to measure.
	MinHeight int
	// HeavyFactor: a token is heavy when its stroke >= baseline * HeavyFactor.
	HeavyFactor float64
	// UncertainBand: tokens within this fraction below the heavy line are withheld
	// (no attachment) rather than called regular.
	UncertainBand float64
	// SanityFactor: tokens with stroke > baseline * SanityFactor are treated as
	// non-text (icon contamination) and skipped — physically impossible for text.
	SanityFactor float64
	// MinLetterRatio: a candidate must have >= 2 letters AND at least this
	// fraction of non-space chars be letters (rejects "@", "£63", "(C3)").
	MinLetterRatio float64
}

// DefaultWeightOptions returns options validated on the hermes-25 stress set.
func DefaultWeightOptions() WeightOptions {
	return WeightOptions{
		Upscale:        6,
		Sharpen:        3.0,
		MinHeight:      10,
		HeavyFactor:    1.45,
		UncertainBand:  0.12,
		SanityFactor:   3.0,
		MinLetterRatio: 0.55,
	}
}

// AttachTextWeights classifies the font weight (regular|heavy) of text
// components by measuring stroke thickness relative to the page's own regular-
// body baseline, attaching ir.TextWeight to confidently-classified tokens.
// Deterministic (OpenCV distance transform, no model). Returns the number of
// components that received a weight (regular + heavy; uncertain ones are
// withheld). img is the original screenshot; comps are the reconciled components.
func AttachTextWeights(img image.Image, comps []ir.Component, opts WeightOptions) int {
	if img == nil {
		return 0
	}
	if opts.Upscale < 1 {
		opts.Upscale = 1
	}

	// Pass 1: measure stroke for eligible text candidates.
	type cand struct {
		idx    int
		h      int
		stroke float64
	}
	var cands []cand
	var heights []float64
	for i := range comps {
		c := &comps[i]
		if c.Weight != nil {
			continue
		}
		// Icons/logos/charts are typed by the region detector — exclude them.
		if c.Type.Value != "text" || c.Text == nil {
			continue
		}
		if c.BBox.Height() < opts.MinHeight || c.BBox.Width() < 6 {
			continue
		}
		// Wordy gate: reject symbol soup OCR hallucinated from inline glyphs.
		if !isWordy(c.Text.Value, opts.MinLetterRatio) {
			continue
		}
		sw := measureStroke(cropUpscaled(img, c.BBox, opts.Upscale), opts.Sharpen)
		if sw <= 0 {
			continue
		}
		cands = append(cands, cand{i, c.BBox.Height(), sw})
		heights = append(heights, float64(c.BBox.Height()))
	}
	if len(cands) < 4 {
		return 0
	}

	// Page body baseline: median stroke over body-tier tokens (height near/below
	// the median height — body text is overwhelmingly regular).
	medH := medianF(heights)
	var body []float64
	for _, c := range cands {
		if float64(c.h) <= 1.3*medH {
			body = append(body, c.stroke)
		}
	}
	base := medianF(body)
	if base <= 0 {
		return 0
	}
	heavyLine := base * opts.HeavyFactor
	uncLine := heavyLine * (1 - opts.UncertainBand)
	ceiling := base * opts.SanityFactor

	n := 0
	for _, c := range cands {
		if c.stroke > ceiling {
			continue // icon contamination — not real text weight
		}
		switch {
		case c.stroke >= heavyLine:
			comps[c.idx].Weight = &ir.TextWeight{
				Weight:     "heavy",
				StrokePx:   round2(c.stroke),
				BaselinePx: round2(base),
				Confidence: weightConfidence(c.stroke, heavyLine, base),
			}
			n++
		case c.stroke >= uncLine:
			// uncertain band — withhold (no attachment).
		default:
			comps[c.idx].Weight = &ir.TextWeight{
				Weight:     "regular",
				StrokePx:   round2(c.stroke),
				BaselinePx: round2(base),
				Confidence: weightConfidence(c.stroke, heavyLine, base),
			}
			n++
		}
	}
	return n
}

// weightConfidence scores how far the stroke sits from the decision line
// (heavyLine), normalized by the baseline, clamped to [0,1]. A token right on
// the line scores ~0; far from it scores toward 1.
func weightConfidence(stroke, heavyLine, base float64) float64 {
	if base <= 0 {
		return 0
	}
	m := (stroke - heavyLine) / base
	if m < 0 {
		m = -m
	}
	if m > 1 {
		m = 1
	}
	return round2(m)
}

// isWordy reports whether a token looks like real text: >= 2 letters and at
// least minRatio of its non-space chars are letters. Rejects symbol soup that
// OCR hallucinates from inline icons/glyphs ("@", "£63", "(C3)", "@®").
func isWordy(s string, minRatio float64) bool {
	letters, nonspace := 0, 0
	for _, r := range s {
		if r == ' ' {
			continue
		}
		nonspace++
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	if letters < 2 || nonspace == 0 {
		return false
	}
	return float64(letters)/float64(nonspace) >= minRatio
}

// cropUpscaled extracts the bbox and upscales it by up using a smooth filter.
func cropUpscaled(img image.Image, b ir.BoundingBox, up int) image.Image {
	ib := img.Bounds()
	x0, y0, x1, y1 := b.X0, b.Y0, b.X1, b.Y1
	if x0 < ib.Min.X {
		x0 = ib.Min.X
	}
	if y0 < ib.Min.Y {
		y0 = ib.Min.Y
	}
	if x1 > ib.Max.X {
		x1 = ib.Max.X
	}
	if y1 > ib.Max.Y {
		y1 = ib.Max.Y
	}
	if x1 <= x0 || y1 <= y0 {
		return nil
	}
	sub := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	draw.Draw(sub, sub.Bounds(), img, image.Pt(x0, y0), draw.Src)
	if up <= 1 {
		return sub
	}
	sb := sub.Bounds()
	scaled := image.NewRGBA(image.Rect(0, 0, sb.Dx()*up, sb.Dy()*up))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), sub, sb, draw.Over, nil)
	return scaled
}

// measureStroke returns the SWT stroke width (px) of the ink in a crop: an
// AA-aware grayscale unsharp, Otsu binarize (auto-polarity), distance transform,
// then 2 * mean(top-20% ridge distances). Returns 0 on failure/empty ink.
func measureStroke(img image.Image, sharpen float64) float64 {
	if img == nil {
		return 0
	}
	mat, err := gocv.ImageToMatRGB(img)
	if err != nil || mat.Empty() {
		return 0
	}
	defer mat.Close()
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(mat, &gray, gocv.ColorBGRToGray)

	if sharpen > 0 {
		blur := gocv.NewMat()
		gocv.GaussianBlur(gray, &blur, image.Pt(0, 0), 3.0, 3.0, gocv.BorderDefault)
		sharp := gocv.NewMat()
		gocv.AddWeighted(gray, 1.0+sharpen, blur, -sharpen, 0, &sharp)
		blur.Close()
		gray.Close()
		gray = sharp
	}

	bin := gocv.NewMat()
	defer bin.Close()
	gocv.Threshold(gray, &bin, 0, 255, gocv.ThresholdBinary+gocv.ThresholdOtsu)
	// Ensure ink (minority class) is white for DistanceTransform (distance to bg).
	if gocv.CountNonZero(bin) > bin.Rows()*bin.Cols()/2 {
		inv := gocv.NewMat()
		gocv.BitwiseNot(bin, &inv)
		bin.Close()
		bin = inv
	}

	dt := gocv.NewMat()
	defer dt.Close()
	labels := gocv.NewMat()
	defer labels.Close()
	gocv.DistanceTransform(bin, &dt, &labels, gocv.DistL2, gocv.DistanceMask5, gocv.DistanceLabelCComp)

	var vals []float64
	for y := 0; y < dt.Rows(); y++ {
		for x := 0; x < dt.Cols(); x++ {
			if v := float64(dt.GetFloatAt(y, x)); v > 0 {
				vals = append(vals, v)
			}
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	start := int(0.80 * float64(len(vals)))
	if start >= len(vals) {
		start = len(vals) - 1
	}
	var sum float64
	for _, v := range vals[start:] {
		sum += v
	}
	return 2 * sum / float64(len(vals)-start)
}

func medianF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
