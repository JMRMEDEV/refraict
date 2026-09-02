// Package detect derives UI components deterministically from measurable
// evidence (OCR tokens today; connected-components/edges later) rather than
// relying on a small vision model to emit precise bounding boxes — which small
// local VLMs do unreliably. The vision model is reserved for semantic labeling
// (role/description), not geometry.
package detect

import (
	"fmt"
	"sort"

	"github.com/refraict/refraict/internal/ir"
)

// TextComponentOptions tunes OCR-token grouping into text components.
type TextComponentOptions struct {
	// MinConfidence drops OCR tokens below this confidence (0..1).
	MinConfidence float64
	// LineOverlapRatio: two tokens are on the same line if their vertical
	// overlap exceeds this fraction of the smaller token height.
	LineOverlapRatio float64
	// WordGapFactor: tokens on the same line join into one text run when the
	// horizontal gap between them is at most this multiple of the median token
	// height (approximates a space vs. a column break).
	WordGapFactor float64
}

// DefaultTextComponentOptions returns sensible defaults.
func DefaultTextComponentOptions() TextComponentOptions {
	return TextComponentOptions{
		MinConfidence:    0.30,
		LineOverlapRatio: 0.4,
		WordGapFactor:    1.5,
	}
}

// TextComponentsFromOCR groups OCR tokens into line-level text components with
// merged bounding boxes and concatenated text. Each component is emitted in
// global coordinates with source "ocr" so downstream color sampling, graph
// building, and DOM inference all operate on real, measured geometry.
func TextComponentsFromOCR(toks []ir.OCRToken, opts TextComponentOptions) []ir.Component {
	if len(toks) == 0 {
		return nil
	}
	// Filter by confidence and drop empties.
	filtered := make([]ir.OCRToken, 0, len(toks))
	for _, t := range toks {
		if t.Text == "" || t.BBoxGlobal.Empty() {
			continue
		}
		if t.Confidence > 0 && t.Confidence < opts.MinConfidence {
			continue
		}
		filtered = append(filtered, t)
	}
	if len(filtered) == 0 {
		return nil
	}

	medH := medianHeight(filtered)
	lines := groupIntoLines(filtered, opts.LineOverlapRatio)

	var comps []ir.Component
	id := 0
	for _, line := range lines {
		// Within a line, split into runs where the horizontal gap exceeds the
		// word-gap threshold (keeps distinct labels/columns separate).
		runs := splitLineIntoRuns(line, medH*opts.WordGapFactor)
		for _, run := range runs {
			bbox, text, conf := mergeRun(run)
			if bbox.Empty() || text == "" {
				continue
			}
			id++
			comps = append(comps, ir.Component{
				ID:   fmt.Sprintf("t%04d", id),
				Type: ir.ConstString{Value: "text", Source: "ocr", Confidence: conf},
				BBox: bbox,
				Text: &ir.ConstString{Value: text, Source: "ocr", Confidence: conf},
				Confidence: conf,
				Source:     "ocr",
			})
		}
	}
	return comps
}

// groupIntoLines clusters tokens whose vertical spans overlap into text lines,
// sorted top-to-bottom then left-to-right.
func groupIntoLines(toks []ir.OCRToken, overlapRatio float64) [][]ir.OCRToken {
	sorted := append([]ir.OCRToken(nil), toks...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].BBoxGlobal.Y0 != sorted[j].BBoxGlobal.Y0 {
			return sorted[i].BBoxGlobal.Y0 < sorted[j].BBoxGlobal.Y0
		}
		return sorted[i].BBoxGlobal.X0 < sorted[j].BBoxGlobal.X0
	})
	var lines [][]ir.OCRToken
	for _, t := range sorted {
		placed := false
		for i := range lines {
			if lineContainsVerticalOverlap(lines[i], t.BBoxGlobal, overlapRatio) {
				lines[i] = append(lines[i], t)
				placed = true
				break
			}
		}
		if !placed {
			lines = append(lines, []ir.OCRToken{t})
		}
	}
	// Sort tokens within each line left-to-right.
	for i := range lines {
		sort.Slice(lines[i], func(a, b int) bool {
			return lines[i][a].BBoxGlobal.X0 < lines[i][b].BBoxGlobal.X0
		})
	}
	return lines
}

func lineContainsVerticalOverlap(line []ir.OCRToken, b ir.BoundingBox, ratio float64) bool {
	for _, t := range line {
		if verticalOverlapRatio(t.BBoxGlobal, b) >= ratio {
			return true
		}
	}
	return false
}

// verticalOverlapRatio returns the vertical overlap of two boxes as a fraction
// of the smaller box height.
func verticalOverlapRatio(a, b ir.BoundingBox) float64 {
	top := a.Y0
	if b.Y0 > top {
		top = b.Y0
	}
	bottom := a.Y1
	if b.Y1 < bottom {
		bottom = b.Y1
	}
	overlap := bottom - top
	if overlap <= 0 {
		return 0
	}
	minH := a.Height()
	if b.Height() < minH {
		minH = b.Height()
	}
	if minH <= 0 {
		return 0
	}
	return float64(overlap) / float64(minH)
}

// splitLineIntoRuns splits a left-to-right ordered line into runs separated by
// horizontal gaps larger than maxGap.
func splitLineIntoRuns(line []ir.OCRToken, maxGap float64) [][]ir.OCRToken {
	if len(line) == 0 {
		return nil
	}
	var runs [][]ir.OCRToken
	cur := []ir.OCRToken{line[0]}
	for i := 1; i < len(line); i++ {
		gap := line[i].BBoxGlobal.X0 - line[i-1].BBoxGlobal.X1
		if float64(gap) > maxGap {
			runs = append(runs, cur)
			cur = []ir.OCRToken{line[i]}
			continue
		}
		cur = append(cur, line[i])
	}
	runs = append(runs, cur)
	return runs
}

// mergeRun unions the bounding boxes and joins the text of a run of tokens.
func mergeRun(run []ir.OCRToken) (ir.BoundingBox, string, float64) {
	if len(run) == 0 {
		return ir.BoundingBox{}, "", 0
	}
	bbox := run[0].BBoxGlobal
	text := run[0].Text
	confSum := run[0].Confidence
	for i := 1; i < len(run); i++ {
		b := run[i].BBoxGlobal
		if b.X0 < bbox.X0 {
			bbox.X0 = b.X0
		}
		if b.Y0 < bbox.Y0 {
			bbox.Y0 = b.Y0
		}
		if b.X1 > bbox.X1 {
			bbox.X1 = b.X1
		}
		if b.Y1 > bbox.Y1 {
			bbox.Y1 = b.Y1
		}
		text += " " + run[i].Text
		confSum += run[i].Confidence
	}
	conf := confSum / float64(len(run))
	if conf <= 0 {
		conf = 0.5
	}
	return bbox, text, conf
}

func medianHeight(toks []ir.OCRToken) float64 {
	if len(toks) == 0 {
		return 0
	}
	hs := make([]float64, 0, len(toks))
	for _, t := range toks {
		hs = append(hs, float64(t.BBoxGlobal.Height()))
	}
	sort.Float64s(hs)
	m := hs[len(hs)/2]
	if m <= 0 {
		m = 1
	}
	return m
}
