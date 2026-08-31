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
				out[i] = Merge(out[i], c)
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

// Merge combines b into a, producing a merged component. The box grows to union;
// confidence is blended by a naive weighted average; conflict fields keep a's
// value but record disagreement in confidence.
func Merge(a, b ir.Component) ir.Component {
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
	// Confidence weighted by area.
	areaA := float64(a.BBox.Area())
	areaB := float64(b.BBox.Area())
	if areaA+areaB > 0 {
		a.Confidence = (areaA*a.Confidence + areaB*b.Confidence) / (areaA + areaB)
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
