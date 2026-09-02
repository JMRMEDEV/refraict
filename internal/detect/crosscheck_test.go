package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func txt(s string) ir.OCRToken { return ir.OCRToken{Text: s} }

func TestCrossCheckSupported(t *testing.T) {
	toks := []ir.OCRToken{txt("Sign"), txt("in"), txt("Sign up")}
	colors := []ir.ColorFact{{RGB: [3]int{42, 40, 37}}}
	overview := `The screen shows "Sign in" and "Sign up". Background is #2A2825.`
	r := CrossCheck(overview, colors, toks, nil)
	if !r.Consistent {
		t.Fatalf("expected consistent, got unsupported: %+v", r.Unsupported)
	}
	if r.Agreement < 0.99 {
		t.Fatalf("expected full agreement, got %f", r.Agreement)
	}
}

func TestCrossCheckFlagsUnsupportedColor(t *testing.T) {
	toks := []ir.OCRToken{txt("Hello")}
	colors := []ir.ColorFact{{RGB: [3]int{42, 40, 37}}} // near-black
	overview := `Says "Hello". The button is #FF0000.` // red, not measured
	r := CrossCheck(overview, colors, toks, nil)
	if r.Consistent {
		t.Fatal("expected inconsistent due to unmeasured red")
	}
	found := false
	for _, u := range r.Unsupported {
		if u.Kind == "color" && u.Claim == "#FF0000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected #FF0000 flagged, got %+v", r.Unsupported)
	}
}

func TestCrossCheckCount(t *testing.T) {
	// 4 non-text components; overview claims "4 cards" -> supported.
	comps := []ir.Component{
		{Type: ir.ConstString{Value: "card"}},
		{Type: ir.ConstString{Value: "card"}},
		{Type: ir.ConstString{Value: "image"}},
		{Type: ir.ConstString{Value: "icon"}},
		{Type: ir.ConstString{Value: "text"}}, // excluded
	}
	r := CrossCheck(`It has 4 cards.`, nil, nil, comps)
	if r.CountChecked != 1 || r.CountOK != 1 {
		t.Fatalf("expected 1/1 count supported, got %d/%d (%+v)", r.CountOK, r.CountChecked, r.Unsupported)
	}
	// Overclaim: "10 cards" vs 4 -> flagged.
	r2 := CrossCheck(`It has 10 cards.`, nil, nil, comps)
	if r2.Consistent {
		t.Fatal("expected 10 cards to be flagged against 4 detected")
	}
}

func TestCrossCheckEmpty(t *testing.T) {
	r := CrossCheck("", nil, nil, nil)
	if !r.Consistent || r.Agreement != 1.0 {
		t.Fatalf("empty overview should be consistent with agreement 1.0, got %+v", r)
	}
}

// TestCrossCheckIgnoresNonCountableNouns verifies OCR/parse artifacts like
// "123 is" or "1 jd" are ignored (not flagged as count claims).
func TestCrossCheckIgnoresNonCountableNouns(t *testing.T) {
	r := CrossCheck(`PH-123 is overdue and 1 jd assigned.`, nil, nil, nil)
	if r.CountChecked != 0 {
		t.Fatalf("expected 0 count claims checked, got %d (%+v)", r.CountChecked, r.Unsupported)
	}
	if !r.Consistent {
		t.Fatalf("non-countable nouns must not make it inconsistent: %+v", r.Unsupported)
	}
}
