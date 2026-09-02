// Coordinate repair and component normalization for vision output.
//
// Vision models frequently return components without usable IDs or with
// placeholder / zero bounding boxes. These helpers repair such components so
// the canonical IR does not collapse: empty boxes fall back to OCR token boxes
// (when available and text-matched) and finally to the enclosing crop's global
// box; IDs are synthesized deterministically; boxes are clamped to the crop.
package cli

import (
	"strings"

	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
)

// repairOutcome describes what happened to a single component observation.
type repairOutcome struct {
	// BoxesFlagged is true when the VLM provided a box that was empty/zero, so
	// it had to be repaired, or the component was dropped for lack of any box.
	BoxesFlagged bool
	// Repaired is true when a non-empty box was produced for a flagged one.
	Repaired bool
	// Dropped is true when no usable box could be derived and the component
	// was excluded from the pipeline.
	Dropped bool
}

// repairComponent turns a raw vision component reference into a canonical IR
// component, repairing missing IDs and empty/out-of-range boxes.
//
// cropBox is the crop's global bounding box; ocrTokens are the OCR tokens known
// to fall within this crop (used as a coordinate fallback). imgW/imgH bound the
// global image and constrain clamping. If the component cannot be given a valid
// (non-empty, in-bounds) box it is dropped, with outcome.Dropped set.
func repairComponent(cmp model.VisionCompRef, cropID string, idx int, cropBox ir.BoundingBox, ocrTokens []ir.OCRToken, imgW, imgH int) (ir.Component, repairOutcome) {
	out := repairOutcome{}

	// --- ID synthesis (C2) ---
	id := cmp.ID
	if strings.TrimSpace(id) == "" {
		id = cropID + "-" + itoaR(idx)
	}

	// --- Coordinate repair (G1) ---
	box := cmp.BBoxGlobal
	if box.Empty() {
		out.BoxesFlagged = true
		// Fallback 1: text-matched OCR token box.
		if b, ok := matchOCRBox(cmp.Text, ocrTokens); ok {
			box = b
			out.Repaired = true
		} else if !cropBox.Empty() {
			// Fallback 2: the enclosing crop's global box. This is a coarse
			// stand-in but keeps the component in global space and prevents the
			// pipeline from collapsing when the model omits coordinates.
			box = cropBox
			out.Repaired = true
		}
	}

	// Clamp to the crop region then to the image, so an out-of-bounds box does
	// not produce bogus relationships or pixel samples.
	box = clampBox(box, cropBox, imgW, imgH)
	if box.Empty() {
		out.BoxesFlagged = true
		out.Dropped = true
		return ir.Component{}, out
	}

	return ir.Component{
		ID:         id,
		Type:       ir.ConstString{Value: cmp.Type, Source: "crop-vision", Confidence: cmp.Confidence},
		BBox:       box,
		Text:       optionalTextValue(cmp.Text),
		Semantic:   optionalTextValue(cmp.Role),
		Confidence: cmp.Confidence,
		Source:     "crop-vision",
	}, out
}

// matchOCRBox finds a non-empty OCR token whose text is contained in (or
// equals) the given component text. Returns the token's global box.
func matchOCRBox(compText string, ocrTokens []ir.OCRToken) (ir.BoundingBox, bool) {
	t := strings.ToLower(strings.TrimSpace(compText))
	if t == "" {
		return ir.BoundingBox{}, false
	}
	for _, tok := range ocrTokens {
		tk := strings.ToLower(strings.TrimSpace(tok.Text))
		if tk == "" || tok.BBoxGlobal.Empty() {
			continue
		}
		if strings.Contains(t, tk) || strings.Contains(tk, t) {
			return tok.BBoxGlobal, true
		}
	}
	return ir.BoundingBox{}, false
}

// clampBox constrains box within cropBox (if cropBox is valid) and within the
// image bounds [0,imgW)x[0,imgH). Returns the clamped box.
func clampBox(box, cropBox ir.BoundingBox, imgW, imgH int) ir.BoundingBox {
	// Clamp to image bounds first.
	box.X0 = clampInt(box.X0, 0, imgW)
	box.Y0 = clampInt(box.Y0, 0, imgH)
	box.X1 = clampInt(box.X1, 0, imgW)
	box.Y1 = clampInt(box.Y1, 0, imgH)
	// If the crop box is valid, intersect with it so global coordinates stay in
	// the analyzed region.
	if !cropBox.Empty() {
		if box.X0 < cropBox.X0 {
			box.X0 = cropBox.X0
		}
		if box.Y0 < cropBox.Y0 {
			box.Y0 = cropBox.Y0
		}
		if box.X1 > cropBox.X1 {
			box.X1 = cropBox.X1
		}
		if box.Y1 > cropBox.Y1 {
			box.Y1 = cropBox.Y1
		}
	}
	return box
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func itoaR(n int) string {
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
