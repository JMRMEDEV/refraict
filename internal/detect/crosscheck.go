package detect

import (
	"regexp"
	"strings"

	"github.com/refraict/refraict/internal/ir"
)

// CrossCheckReport records how well the whole-image (overview) VLM read agrees
// with the deterministic measured evidence (OCR text, measured colors, detected
// components). It is the grounding guard generalized from a single summary to a
// SECOND, independent read: gemma's overview description is a distinct reasoning
// pass from the per-crop pipeline, so where its claims match measured evidence
// we gain confidence, and where they diverge we flag it.
//
// The report is passive/diagnostic: refraict emits the signal; the calling agent
// decides whether to trust page.md or fall back to a paid vision read. No model
// call is involved — extraction and matching are deterministic.
type CrossCheckReport struct {
	// Consistent is true when no unsupported overview claims were found.
	Consistent bool `json:"consistent"`
	// Agreement is the fraction of checkable overview claims supported by
	// measured evidence (1.0 when the overview makes no checkable claims).
	Agreement float64 `json:"agreement"`
	// Unsupported lists overview claims not backed by measured evidence.
	Unsupported []UnsupportedClaim `json:"unsupported_claims"`
	// Counts of checkable claims by kind, for diagnostics.
	TextChecked  int `json:"text_checked"`
	TextOK       int `json:"text_supported"`
	ColorChecked int `json:"color_checked"`
	ColorOK      int `json:"color_supported"`
	CountChecked int `json:"count_checked"`
	CountOK      int `json:"count_supported"`
}

// CrossCheck compares an overview description against measured evidence.
//
//   - color: every hex/named color claimed in the overview must be close to (or
//     the nearest name of) a measured color.
//   - text:  every quoted span in the overview must appear in the OCR corpus.
//   - count: an explicit small count claim (e.g. "4 columns", "3 cards") is
//     checked against the number of detected components of a plausibly-matching
//     kind; a count with no measurable basis is flagged. Counts are advisory
//     (component detection is approximate), so they are weighted the same as one
//     claim but never hard-fail beyond appearing in the list.
//
// It deliberately reuses the grounding-guard primitives (hexRe, quoteRe,
// wordRe, nearAnyColor, supportedColorNames, isStructuralWord) so the two
// reports stay behaviorally consistent.
func CrossCheck(overview string, colors []ir.ColorFact, toks []ir.OCRToken, comps []ir.Component) CrossCheckReport {
	r := CrossCheckReport{Consistent: true, Agreement: 1.0}
	ov := strings.TrimSpace(overview)
	if ov == "" {
		return r
	}
	low := strings.ToLower(ov)

	var measured [][3]int
	for _, c := range colors {
		measured = append(measured, c.RGB)
	}

	// --- Color claims ---
	for _, hx := range hexRe.FindAllString(ov, -1) {
		r.ColorChecked++
		rgb, ok := hexToRGB(hx)
		if ok && nearAnyColor(rgb, measured, 24) {
			r.ColorOK++
		} else {
			r.Unsupported = append(r.Unsupported, UnsupportedClaim{
				Kind: "color", Claim: hx,
				Reason: "overview hex color not close to any measured color",
			})
		}
	}
	supportedNames := supportedColorNames(measured)
	seenColor := map[string]bool{}
	for _, w := range wordRe.FindAllString(low, -1) {
		if _, known := namedColorHex[w]; !known || seenColor[w] {
			continue
		}
		seenColor[w] = true
		r.ColorChecked++
		if supportedNames[canonColorName(w)] {
			r.ColorOK++
		} else {
			r.Unsupported = append(r.Unsupported, UnsupportedClaim{
				Kind: "color", Claim: w,
				Reason: "overview named color is not the nearest match to any measured color",
			})
		}
	}

	// --- Text claims (quoted spans) ---
	ocrWords := map[string]bool{}
	for _, t := range toks {
		for _, w := range wordRe.FindAllString(strings.ToLower(t.Text), -1) {
			ocrWords[w] = true
		}
	}
	for _, m := range quoteRe.FindAllStringSubmatch(ov, -1) {
		q := firstNonEmpty(m[1], m[2])
		if strings.TrimSpace(q) == "" {
			continue
		}
		words := wordRe.FindAllString(strings.ToLower(q), -1)
		if len(words) == 0 {
			continue
		}
		r.TextChecked++
		allPresent := true
		for _, w := range words {
			if !ocrWords[w] && !isStructuralWord(w) {
				allPresent = false
				break
			}
		}
		if allPresent {
			r.TextOK++
		} else {
			r.Unsupported = append(r.Unsupported, UnsupportedClaim{
				Kind: "text", Claim: q,
				Reason: "overview quoted text not found in OCR corpus",
			})
		}
	}

	// --- Count claims (advisory) ---
	// Match patterns like "4 columns", "3 cards", "two panels". Only nouns we
	// can actually verify against detected components are checked; any other
	// noun is IGNORED (not flagged), which suppresses OCR/parse artifacts such
	// as "123 is" or "1 jd" that a bare <number> <word> regex would otherwise
	// treat as bogus count claims.
	for _, cc := range countPhraseRe.FindAllStringSubmatch(low, -1) {
		nStr, noun := cc[1], cc[2]
		if !isCountableNoun(noun) {
			continue
		}
		n := wordOrDigitToInt(nStr)
		if n <= 0 {
			continue
		}
		r.CountChecked++
		measuredN := countComponentsMatching(comps, noun, toks)
		// Allow a small tolerance: detection is approximate, and a claim within
		// +/-1 of the measured count is treated as supported.
		if abs(measuredN-n) <= 1 {
			r.CountOK++
		} else {
			r.Unsupported = append(r.Unsupported, UnsupportedClaim{
				Kind: "count", Claim: nStr + " " + noun,
				Reason: "overview count disagrees with detected components",
			})
		}
	}

	checked := r.TextChecked + r.ColorChecked + r.CountChecked
	ok := r.TextOK + r.ColorOK + r.CountOK
	if checked > 0 {
		r.Agreement = float64(ok) / float64(checked)
	}
	r.Consistent = len(r.Unsupported) == 0
	return r
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// countPhraseRe matches "<number|word> <noun>" count claims, e.g. "4 columns",
// "3 cards", "two panels". Captures the count and the (singular/plural) noun.
var countPhraseRe = regexp.MustCompile(`\b(\d{1,3}|one|two|three|four|five|six|seven|eight|nine|ten)\s+([a-z]+)\b`)

var wordNums = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

// wordOrDigitToInt parses "4" or "four" into 4; 0 on failure.
func wordOrDigitToInt(s string) int {
	if v, ok := wordNums[s]; ok {
		return v
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// containerNouns and elementNouns are the ONLY count nouns the cross-check will
// verify (singular form). Restricting to these suppresses false count claims
// from OCR/parse noise and keeps the check to nouns countComponentsMatching can
// actually map to detected components.
var containerNouns = map[string]bool{
	"card": true, "column": true, "panel": true, "section": true,
	"container": true, "region": true, "tile": true, "box": true,
}

var elementNouns = map[string]bool{
	"icon": true, "button": true, "logo": true, "image": true, "chart": true, "avatar": true,
}

// isCountableNoun reports whether a (possibly plural) noun is one the cross-check
// can verify against detected components.
func isCountableNoun(noun string) bool {
	s := strings.TrimSuffix(noun, "s")
	return containerNouns[s] || elementNouns[s]
}

// countComponentsMatching returns how many detected components plausibly match a
// count-claim noun. The mapping is intentionally loose (component detection is
// approximate): container nouns (card/column/panel/section) count non-text
// structural regions; element nouns (icon/button/logo/image/chart) count that
// element type. Callers restrict input to isCountableNoun.
func countComponentsMatching(comps []ir.Component, noun string, toks []ir.OCRToken) int {
	singular := strings.TrimSuffix(noun, "s")
	n := 0
	for _, c := range comps {
		t := strings.ToLower(c.Type.Value)
		switch {
		case containerNouns[singular]:
			// Count non-text structural regions.
			if t != "text" && t != "label" {
				n++
			}
		case elementNouns[singular]:
			if strings.Contains(t, singular) || (singular == "image" && t == "image") {
				n++
			}
		}
	}
	return n
}
