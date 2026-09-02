package detect

import (
	"regexp"
	"strings"

	"github.com/refraict/refraict/internal/ir"
)

// UnsupportedClaim is a single grounding violation found in a generated
// summary: text that asserts something not supported by the deterministic
// evidence. It is machine-readable so a downstream agent can decide whether to
// trust the summary or fall back to a direct image read.
type UnsupportedClaim struct {
	// Kind categorizes the violation: "color" | "behavior" | "text" | "number".
	Kind string `json:"kind"`
	// Claim is the offending token/phrase found in the summary.
	Claim string `json:"claim"`
	// Reason explains why it is unsupported.
	Reason string `json:"reason"`
}

// GroundingReport is the result of checking a summary against evidence.
type GroundingReport struct {
	Grounded         bool               `json:"grounded"`
	UnsupportedClaims []UnsupportedClaim `json:"unsupported_claims"`
	// Coverage: fraction of summary color mentions that ARE supported (1.0 when
	// none are mentioned). Diagnostic only.
	ColorSupport float64 `json:"color_support"`
	// TextSupport: fraction of quoted text spans in the summary that ARE
	// supported by the OCR corpus (1.0 when none are quoted). Diagnostic only.
	TextSupport float64 `json:"text_support"`
}

// namedColorHex maps common color words to representative RGB so a summary that
// says "blue" can be checked against measured colors. Approximate on purpose.
var namedColorHex = map[string][3]int{
	"black":  {0, 0, 0},
	"white":  {255, 255, 255},
	"gray":   {128, 128, 128},
	"grey":   {128, 128, 128},
	"red":    {200, 40, 40},
	"green":  {40, 160, 60},
	"blue":   {40, 80, 200},
	"yellow": {230, 210, 40},
	"orange": {230, 140, 40},
	"purple": {130, 60, 180},
	"pink":   {230, 120, 170},
	"brown":  {120, 80, 50},
}

// behaviorWords are dynamic behaviors a static screenshot cannot evidence.
var behaviorWords = []string{
	"hover", "animation", "animated", "transition", "on click", "onclick",
	"page load", "loads", "scrolling animation", "fade", "slide in",
	"subtly change", "changes with each",
}

var hexRe = regexp.MustCompile(`#[0-9a-fA-F]{6}`)
var wordRe = regexp.MustCompile(`[a-zA-Z]+`)

// CheckGrounding scans summary text for claims unsupported by the measured
// evidence (OCR tokens + measured colors) and returns a structured report.
//
// It flags these kinds of violation, all deterministically:
//   - color:     a hex or named color that is not close to any measured color.
//   - behavior:  dynamic behavior words that a static screenshot cannot support.
//   - text:      a quoted string, or a content word, that does not appear in the
//     OCR corpus (structural/UI vocabulary and stopwords are exempt, since the
//     model must be able to describe layout without quoting text).
func CheckGrounding(summary string, colors []ir.ColorFact, toks []ir.OCRToken) GroundingReport {
	report := GroundingReport{Grounded: true, ColorSupport: 1.0, TextSupport: 1.0}
	if strings.TrimSpace(summary) == "" {
		return report
	}
	low := strings.ToLower(summary)

	// Behavior claims.
	for _, w := range behaviorWords {
		if strings.Contains(low, w) {
			report.UnsupportedClaims = append(report.UnsupportedClaims, UnsupportedClaim{
				Kind:   "behavior",
				Claim:  w,
				Reason: "dynamic behavior cannot be evidenced by a static screenshot",
			})
		}
	}

	// Color claims: measured palette.
	var measured [][3]int
	for _, c := range colors {
		measured = append(measured, c.RGB)
	}

	colorMentions := 0
	colorSupported := 0

	// Hex mentions.
	for _, hx := range hexRe.FindAllString(summary, -1) {
		colorMentions++
		rgb, ok := hexToRGB(hx)
		if ok && nearAnyColor(rgb, measured, 24) {
			colorSupported++
		} else {
			report.UnsupportedClaims = append(report.UnsupportedClaims, UnsupportedClaim{
				Kind:   "color",
				Claim:  hx,
				Reason: "hex color not close to any measured color",
			})
		}
	}

	// Named-color words: a measured color "supports" a color word only if that
	// word is the NEAREST named color to at least one measured color. This is
	// stricter than a loose tolerance and correctly rejects e.g. "brown" for a
	// near-black gray whose nearest name is "black"/"gray" (Gap 4a).
	supportedNames := supportedColorNames(measured)
	seen := map[string]bool{}
	for _, w := range wordRe.FindAllString(low, -1) {
		if _, known := namedColorHex[w]; !known || seen[w] {
			continue
		}
		seen[w] = true
		colorMentions++
		if supportedNames[canonColorName(w)] {
			colorSupported++
		} else {
			report.UnsupportedClaims = append(report.UnsupportedClaims, UnsupportedClaim{
				Kind:   "color",
				Claim:  w,
				Reason: "named color is not the nearest match to any measured color",
			})
		}
	}

	if colorMentions > 0 {
		report.ColorSupport = float64(colorSupported) / float64(colorMentions)
	}

	// Text claims. Build a normalized OCR corpus of tokens (word-level).
	ocrWords := map[string]bool{}
	for _, t := range toks {
		for _, w := range wordRe.FindAllString(strings.ToLower(t.Text), -1) {
			ocrWords[w] = true
		}
	}

	textMentions := 0
	textSupported := 0

	// Quoted strings in the summary are explicit content claims: every word in
	// a quoted span must appear in the OCR corpus.
	for _, m := range quoteRe.FindAllStringSubmatch(summary, -1) {
		q := m[1]
		if strings.TrimSpace(q) == "" {
			continue
		}
		words := wordRe.FindAllString(strings.ToLower(q), -1)
		if len(words) == 0 {
			continue
		}
		textMentions++
		allPresent := true
		for _, w := range words {
			if !ocrWords[w] && !isStructuralWord(w) {
				allPresent = false
				break
			}
		}
		if allPresent {
			textSupported++
		} else {
			report.UnsupportedClaims = append(report.UnsupportedClaims, UnsupportedClaim{
				Kind:   "text",
				Claim:  q,
				Reason: "quoted text not found in OCR corpus",
			})
		}
	}

	if textMentions > 0 {
		report.TextSupport = float64(textSupported) / float64(textMentions)
	}

	// Numeric claims (Gap 4b): numbers and currency amounts asserted in the
	// summary must appear in the OCR corpus. This catches misreadings such as a
	// chart y-axis "0.8" being reported as "$0.8 per month". Normalization
	// strips currency symbols and thousands separators before comparison.
	ocrNums := map[string]bool{}
	for _, t := range toks {
		for _, n := range numberRe.FindAllString(t.Text, -1) {
			ocrNums[normalizeNumber(n)] = true
		}
	}
	numSeen := map[string]bool{}
	for _, n := range numberRe.FindAllString(summary, -1) {
		norm := normalizeNumber(n)
		if norm == "" || numSeen[norm] {
			continue
		}
		numSeen[norm] = true
		// Ignore trivial small integers (0..9) — commonly structural (e.g.
		// "two regions", axis ticks) and noisy to flag.
		if isTrivialInt(norm) {
			continue
		}
		if !ocrNums[norm] {
			report.UnsupportedClaims = append(report.UnsupportedClaims, UnsupportedClaim{
				Kind:   "number",
				Claim:  n,
				Reason: "numeric/currency value not found in OCR corpus",
			})
		}
	}

	report.Grounded = len(report.UnsupportedClaims) == 0
	return report
}

// numberRe matches integers, decimals, and grouped numbers, with optional
// currency symbol and sign (e.g. "$1.58", "20,998,307", "0.8").
var numberRe = regexp.MustCompile(`\$?-?\d[\d,]*(?:\.\d+)?`)

// normalizeNumber strips currency symbols, thousands separators, and a trailing
// ".0" so "$1,591" and "1591" compare equal.
func normalizeNumber(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "+")
	// Drop a leading minus for comparison robustness.
	s = strings.TrimPrefix(s, "-")
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// isTrivialInt reports whether norm is a single-digit integer 0..9.
func isTrivialInt(norm string) bool {
	if len(norm) != 1 {
		return false
	}
	return norm[0] >= '0' && norm[0] <= '9'
}

// supportedColorNames returns the set of canonical color names that are the
// nearest named color to at least one measured color. Neutral (achromatic)
// measured colors support "gray" generically plus "black"/"white" by luminance
// band, so a mid-dark gray accepts both "gray" and "black" but never "brown".
func supportedColorNames(measured [][3]int) map[string]bool {
	out := map[string]bool{}
	for _, m := range measured {
		mx := max3(m[0], m[1], m[2])
		mn := min3(m[0], m[1], m[2])
		if mx-mn <= 20 {
			// Achromatic: always support the generic "gray".
			out["gray"] = true
			lum := (m[0] + m[1] + m[2]) / 3
			if lum <= 90 {
				out["black"] = true
			}
			if lum >= 170 {
				out["white"] = true
			}
			continue
		}
		if name := nearestColorName(m); name != "" {
			out[name] = true
		}
	}
	return out
}

// nearestColorName returns the canonical named color closest to rgb. Near-
// neutral colors (small spread between channels) are constrained to the
// achromatic names (black/gray/white) so a dark gray never maps to "brown".
func nearestColorName(rgb [3]int) string {
	mx := max3(rgb[0], rgb[1], rgb[2])
	mn := min3(rgb[0], rgb[1], rgb[2])
	neutral := (mx - mn) <= 20 // channel spread; low => achromatic

	best := ""
	bestDist := 1 << 30
	for name, ref := range namedColorHex {
		cn := canonColorName(name)
		if neutral && cn != "black" && cn != "gray" && cn != "white" {
			continue
		}
		d := abs(rgb[0]-ref[0]) + abs(rgb[1]-ref[1]) + abs(rgb[2]-ref[2])
		if d < bestDist {
			bestDist = d
			best = cn
		}
	}
	return best
}

func max3(a, b, c int) int {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// canonColorName folds synonyms (grey->gray) to a canonical name.
func canonColorName(w string) string {
	if w == "grey" {
		return "gray"
	}
	return w
}

// quoteRe matches double-quoted spans (straight or typographic).
var quoteRe = regexp.MustCompile(`"([^"]+)"|“([^”]+)”`)

// structuralWords are UI/layout vocabulary the model may use to describe
// structure without it being a content claim that must appear in OCR.
var structuralWords = map[string]bool{
	"header": true, "footer": true, "sidebar": true, "nav": true,
	"navigation": true, "menu": true, "button": true, "buttons": true,
	"panel": true, "card": true, "cards": true, "list": true, "row": true,
	"column": true, "columns": true, "grid": true, "section": true,
	"sections": true, "region": true, "regions": true, "chart": true,
	"charts": true, "table": true, "input": true, "field": true, "form": true,
	"icon": true, "icons": true, "image": true, "images": true, "logo": true,
	"text": true, "label": true, "labels": true, "title": true, "value": true,
	"background": true, "foreground": true, "layout": true, "page": true,
	"top": true, "bottom": true, "left": true, "right": true, "center": true,
	"content": true, "area": true, "bar": true, "link": true, "links": true,
	"summary": true, "displays": true, "contains": true, "shows": true,
}

// isStructuralWord reports whether a lowercase word is layout/UI vocabulary
// that need not appear in OCR to be considered grounded.
func isStructuralWord(w string) bool {
	if structuralWords[w] {
		return true
	}
	// Pure numbers/currency fragments are treated as structural to avoid noise;
	// numeric content is better checked by the agent against the component list.
	for _, r := range w {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hexToRGB(hx string) ([3]int, bool) {
	hx = strings.TrimPrefix(hx, "#")
	if len(hx) != 6 {
		return [3]int{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		n := 0
		for _, ch := range hx[i*2 : i*2+2] {
			n *= 16
			switch {
			case ch >= '0' && ch <= '9':
				n += int(ch - '0')
			case ch >= 'a' && ch <= 'f':
				n += int(ch-'a') + 10
			case ch >= 'A' && ch <= 'F':
				n += int(ch-'A') + 10
			default:
				return [3]int{}, false
			}
		}
		v[i] = n
	}
	return v, true
}

// nearAnyColor reports whether rgb is within tol (Euclidean-ish, per-channel
// sum) of any measured color.
func nearAnyColor(rgb [3]int, measured [][3]int, tol int) bool {
	for _, m := range measured {
		d := abs(rgb[0]-m[0]) + abs(rgb[1]-m[1]) + abs(rgb[2]-m[2])
		if d <= tol*3 {
			return true
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
