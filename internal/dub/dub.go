// Package dub reconciles duplicate observations across overlapping crops into
// one coherent canonical UI IR.
package dub

import (
	"strings"

	"github.com/refraict/refraict/internal/ir"
)

// Options configures reconciliation heuristics.
type Options struct {
	IoUThreshold float64
	// ConfidenceMerge controls how the merged component's confidence is
	// computed from two overlapping observations (see Merge). 0 selects the
	// legacy area-weighted average; a value in (0,1] blends between the
	// higher-confidence observation (weight 1-w) and the area-weighted average
	// (weight w), so the threshold operators can tune how much to trust a
	// confident reading. Config default is 0.5.
	ConfidenceMerge float64
}

// Reconcile merges raw crop component observations into a normalized set of
// components. It converts global boxes to canonical IR components, merges
// near-duplicates (IoU > threshold and similar type and/or shared text),
// preserving provenance via confidence-weighted combination.
func Reconcile(raw []ir.Component, o Options) []ir.Component {
	var out []ir.Component
	for _, c := range raw {
		if c.BBox.Empty() {
			continue
		}
		merged := false
		for i := range out {
			if mergeable(out[i], c, o) {
				out[i] = MergeOpts(out[i], c, o.ConfidenceMerge)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, c)
		}
	}
	return out
}

func mergeable(a, b ir.Component, o Options) bool {
	iou := a.BBox.IoU(b.BBox)
	if iou <= o.IoUThreshold {
		return false
	}
	if sameText(a, b) {
		return true
	}
	if a.Type.Value != "" && b.Type.Value != "" && a.Type.Value == b.Type.Value {
		return true
	}
	// Same general class if both covered.
	return false
}

func sameText(a, b ir.Component) bool {
	if (a.Text == nil) || (b.Text == nil) {
		return false
	}
	ta := normalizeText(a.Text.Value)
	tb := normalizeText(b.Text.Value)
	return ta != "" && ta == tb
}

func normalizeText(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Merge combines b into a using the legacy area-weighted confidence blend
// (ConfidenceMerge == 0). See MergeOpts for the weighted variant.
func Merge(a, b ir.Component) ir.Component {
	return MergeOpts(a, b, 0)
}

// MergeOpts combines b into a, producing a merged component. The box grows to
// union; conflict fields keep a's value but record higher-confidence values.
// When weight > 0 the merged confidence blends between the higher-confidence
// observation and the area-weighted average (see Options.ConfidenceMerge);
// otherwise it defaults to the area-weighted average.
func MergeOpts(a, b ir.Component, weight float64) ir.Component {
	// Union box.
	if b.BBox.X0 < a.BBox.X0 {
		a.BBox.X0 = b.BBox.X0
	}
	if b.BBox.Y0 < a.BBox.Y0 {
		a.BBox.Y0 = b.BBox.Y0
	}
	if b.BBox.X1 > a.BBox.X1 {
		a.BBox.X1 = b.BBox.X1
	}
	if b.BBox.Y1 > a.BBox.Y1 {
		a.BBox.Y1 = b.BBox.Y1
	}
	// Confidence is the area-weighted average by default.
	areaA := float64(a.BBox.Area())
	areaB := float64(b.BBox.Area())
	sum := areaA + areaB
	if sum > 0 {
		avg := (areaA*a.Confidence + areaB*b.Confidence) / sum
		if weight <= 0 {
			a.Confidence = avg
		} else {
			// Merge between the area-weighted average and the higher-confidence
			// observation. Higher w keeps the conservative average; lower w
			// trusts the stronger reading.
			hi := a.Confidence
			if b.Confidence > hi {
				hi = b.Confidence
			}
			w := weight
			if w > 1 {
				w = 1
			}
			a.Confidence = w*avg + (1-w)*hi
		}
	}
	// Type preference: measured/ocr higher priority per provenance rules.
	// If types conflict record lower confidence.
	if a.Type.Value != "" && b.Type.Value != "" && a.Type.Value != b.Type.Value {
		// Conflict: keep the higher-confidence value.
		if b.Type.Confidence > a.Type.Confidence {
			a.Type = b.Type
		}
	}
	if a.Text == nil && b.Text != nil {
		a.Text = b.Text
	}
	if a.Semantic == nil && b.Semantic != nil {
		a.Semantic = b.Semantic
	}
	return a
}
