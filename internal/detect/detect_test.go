package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func tok(text string, x0, y0, x1, y1 int, conf float64) ir.OCRToken {
	return ir.OCRToken{Text: text, BBoxGlobal: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}, Confidence: conf}
}

func TestTextComponentsGroupsWordsOnALine(t *testing.T) {
	// Two adjacent words on the same line, small gap => one component.
	toks := []ir.OCRToken{
		tok("Sign", 100, 50, 140, 70, 0.95),
		tok("in", 145, 50, 165, 70, 0.95),
	}
	comps := TextComponentsFromOCR(toks, DefaultTextComponentOptions())
	if len(comps) != 1 {
		t.Fatalf("expected 1 merged text component, got %d", len(comps))
	}
	if comps[0].Text == nil || comps[0].Text.Value != "Sign in" {
		t.Fatalf("expected joined text 'Sign in', got %+v", comps[0].Text)
	}
	// Merged bbox must span both tokens.
	b := comps[0].BBox
	if b.X0 != 100 || b.X1 != 165 || b.Y0 != 50 || b.Y1 != 70 {
		t.Fatalf("unexpected merged bbox: %+v", b)
	}
}

func TestTextComponentsSplitsDistantRuns(t *testing.T) {
	// Two labels far apart on the same line (e.g. columns) => two components.
	toks := []ir.OCRToken{
		tok("Username", 20, 50, 120, 70, 0.9),
		tok("Password", 600, 50, 700, 70, 0.9),
	}
	comps := TextComponentsFromOCR(toks, DefaultTextComponentOptions())
	if len(comps) != 2 {
		t.Fatalf("expected 2 separate components for distant runs, got %d", len(comps))
	}
}

func TestTextComponentsSeparatesLines(t *testing.T) {
	toks := []ir.OCRToken{
		tok("Header", 20, 10, 120, 30, 0.9),
		tok("Body", 20, 100, 90, 120, 0.9),
	}
	comps := TextComponentsFromOCR(toks, DefaultTextComponentOptions())
	if len(comps) != 2 {
		t.Fatalf("expected 2 components on separate lines, got %d", len(comps))
	}
}

func TestTextComponentsDropsLowConfidence(t *testing.T) {
	toks := []ir.OCRToken{
		tok("good", 20, 10, 80, 30, 0.9),
		tok("noise", 20, 100, 80, 120, 0.05),
	}
	comps := TextComponentsFromOCR(toks, DefaultTextComponentOptions())
	if len(comps) != 1 {
		t.Fatalf("expected low-confidence token dropped, got %d comps", len(comps))
	}
	if comps[0].Text.Value != "good" {
		t.Fatalf("expected 'good', got %q", comps[0].Text.Value)
	}
}

func TestTextComponentsEmpty(t *testing.T) {
	if c := TextComponentsFromOCR(nil, DefaultTextComponentOptions()); c != nil {
		t.Fatalf("expected nil for no tokens, got %+v", c)
	}
}

func TestTextComponentsHaveOCRSource(t *testing.T) {
	toks := []ir.OCRToken{tok("X", 0, 0, 10, 10, 0.8)}
	comps := TextComponentsFromOCR(toks, DefaultTextComponentOptions())
	if len(comps) != 1 || comps[0].Source != "ocr" || comps[0].Type.Value != "text" {
		t.Fatalf("expected ocr-sourced text component, got %+v", comps)
	}
}
