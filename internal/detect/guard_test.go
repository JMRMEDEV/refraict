package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func colorFact(hex string, r, g, b int) ir.ColorFact {
	return ir.ColorFact{Value: hex, RGB: [3]int{r, g, b}, Source: "pixel_sampler"}
}

func TestGuardFlagsBehaviorClaims(t *testing.T) {
	summary := "The nav has animated hover effects and the background changes with each page load."
	rep := CheckGrounding(summary, nil, nil)
	if rep.Grounded {
		t.Fatal("expected behavior claims to be flagged as unsupported")
	}
	kinds := map[string]int{}
	for _, c := range rep.UnsupportedClaims {
		kinds[c.Kind]++
	}
	if kinds["behavior"] < 1 {
		t.Fatalf("expected behavior violations, got %+v", rep.UnsupportedClaims)
	}
}

func TestGuardFlagsUnsupportedColor(t *testing.T) {
	// Measured palette is dark; summary claims "bright" white -> flag "white".
	colors := []ir.ColorFact{colorFact("#252829", 37, 40, 41), colorFact("#131415", 19, 20, 21)}
	summary := "A bright white background with high contrast."
	rep := CheckGrounding(summary, colors, nil)
	found := false
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "color" && c.Claim == "white" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'white' flagged against dark palette, got %+v", rep.UnsupportedClaims)
	}
}

func TestGuardPassesSupportedColor(t *testing.T) {
	// Measured palette includes near-black; summary says "black" -> supported.
	colors := []ir.ColorFact{colorFact("#131415", 19, 20, 21)}
	summary := "The panel uses a black background."
	rep := CheckGrounding(summary, colors, nil)
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "color" && c.Claim == "black" {
			t.Fatalf("'black' should be supported by near-black measured color")
		}
	}
}

func TestGuardFlagsUnsupportedHex(t *testing.T) {
	colors := []ir.ColorFact{colorFact("#131415", 19, 20, 21)}
	summary := "Primary color is #FF00FF."
	rep := CheckGrounding(summary, colors, nil)
	found := false
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "color" && c.Claim == "#FF00FF" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unsupported hex flagged, got %+v", rep.UnsupportedClaims)
	}
	if rep.ColorSupport != 0 {
		t.Fatalf("expected 0 color support, got %v", rep.ColorSupport)
	}
}
func TestGuardFlagsUnsupportedText(t *testing.T) {
	toks := []ir.OCRToken{tok("Cost", 0, 0, 40, 20, 0.9), tok("USD", 45, 0, 80, 20, 0.9)}
	// "Sales Growth" is NOT in OCR -> flagged; "Cost USD" IS -> not flagged.
	summary := `The chart is labeled "Sales Growth" next to "Cost USD".`
	rep := CheckGrounding(summary, nil, toks)
	var flaggedSales, flaggedCost bool
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "text" && c.Claim == "Sales Growth" {
			flaggedSales = true
		}
		if c.Kind == "text" && c.Claim == "Cost USD" {
			flaggedCost = true
		}
	}
	if !flaggedSales {
		t.Fatalf("expected 'Sales Growth' flagged as unsupported text, got %+v", rep.UnsupportedClaims)
	}
	if flaggedCost {
		t.Fatalf("'Cost USD' is in OCR and must NOT be flagged")
	}
}

func TestGuardStructuralQuotedWordsExempt(t *testing.T) {
	// A quoted structural term with no OCR should not be flagged.
	summary := `It has a "header" and a "footer".`
	rep := CheckGrounding(summary, nil, nil)
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "text" {
			t.Fatalf("structural quoted words must not be flagged, got %+v", c)
		}
	}
}

func TestGuardTextSupportRatio(t *testing.T) {
	toks := []ir.OCRToken{tok("Cost", 0, 0, 40, 20, 0.9)}
	summary := `Shows "Cost" and "Nonexistent".`
	rep := CheckGrounding(summary, nil, toks)
	if rep.TextSupport != 0.5 {
		t.Fatalf("expected text_support 0.5 (1 of 2 quotes supported), got %v", rep.TextSupport)
	}
}


func TestGuardFlagsBrownAgainstGray(t *testing.T) {
	// Dark neutral gray must NOT be accepted as "brown" (Gap 4a).
	colors := []ir.ColorFact{colorFact("#3B3D3E", 59, 61, 62), colorFact("#161718", 22, 23, 24)}
	summary := "The panel has a brown background."
	rep := CheckGrounding(summary, colors, nil)
	found := false
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "color" && c.Claim == "brown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'brown' flagged against neutral gray palette, got %+v", rep.UnsupportedClaims)
	}
}

func TestGuardPassesGrayForGrayPalette(t *testing.T) {
	colors := []ir.ColorFact{colorFact("#3B3D3E", 59, 61, 62)}
	summary := "A gray panel."
	rep := CheckGrounding(summary, colors, nil)
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "color" && c.Claim == "gray" {
			t.Fatalf("'gray' should be supported by a neutral gray measured color")
		}
	}
}

func TestGuardHexCodesNotFlaggedAsNumbers(t *testing.T) {
	// A summary citing measured hex colors must not flag them as numbers.
	colors := []ir.ColorFact{colorFact("#252829", 37, 40, 41), colorFact("#161718", 22, 23, 24)}
	summary := "Dark gray background (e.g., #252829, #161718, and #1D2020)."
	rep := CheckGrounding(summary, colors, nil)
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "number" {
			t.Fatalf("hex color code should not be flagged as a number, got %+v", c)
		}
	}
}

func TestGuardFlagsUnsupportedNumber(t *testing.T) {
	// Summary claims "$0.8 per month" but OCR only has 0.4 and 1.58 (Gap 4b:
	// the classic chart y-axis "0.8" misread as a monthly cost).
	toks := []ir.OCRToken{tok("$1.58", 0, 0, 40, 20, 0.9), tok("0.4", 0, 30, 40, 50, 0.9)}
	summary := "Billing cost is $0.8 per month; total is $1.58."
	rep := CheckGrounding(summary, nil, toks)
	var flagged08, flagged158 bool
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "number" && normalizeNumber(c.Claim) == "0.8" {
			flagged08 = true
		}
		if c.Kind == "number" && normalizeNumber(c.Claim) == "1.58" {
			flagged158 = true
		}
	}
	if !flagged08 {
		t.Fatalf("expected '$0.8' flagged as unsupported number, got %+v", rep.UnsupportedClaims)
	}
	if flagged158 {
		t.Fatalf("'$1.58' is in OCR and must NOT be flagged")
	}
}

func TestGuardNumberNormalizationMatches(t *testing.T) {
	// "$1,591" in summary should match "1591" in OCR.
	toks := []ir.OCRToken{tok("1591", 0, 0, 40, 20, 0.9)}
	summary := "API requests: $1,591."
	rep := CheckGrounding(summary, nil, toks)
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "number" {
			t.Fatalf("normalized number should match OCR, got %+v", c)
		}
	}
}

func TestGuardIgnoresTrivialSmallIntegers(t *testing.T) {
	// Single-digit integers (e.g. 2 columns) are not flagged.
	summary := "There are 2 columns and 3 sections."
	rep := CheckGrounding(summary, nil, nil)
	for _, c := range rep.UnsupportedClaims {
		if c.Kind == "number" {
			t.Fatalf("trivial small integers must not be flagged, got %+v", c)
		}
	}
}

func TestGuardEmptySummaryIsGrounded(t *testing.T) {
	rep := CheckGrounding("", nil, nil)
	if !rep.Grounded {
		t.Fatal("empty summary should be trivially grounded")
	}
}

func TestGuardCleanSummaryPasses(t *testing.T) {
	colors := []ir.ColorFact{colorFact("#131415", 19, 20, 21)}
	summary := "A dark panel containing the text Cost(USD) and a value."
	rep := CheckGrounding(summary, colors, nil)
	if !rep.Grounded {
		t.Fatalf("expected clean summary grounded, got %+v", rep.UnsupportedClaims)
	}
}
