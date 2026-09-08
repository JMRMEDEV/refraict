package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

// textComp builds a text component with a given height (y from 0..h) for tiering
// tests. Width is fixed; only height drives tiering.
func textComp(id string, h int) ir.Component {
	return ir.Component{
		ID:   id,
		Type: ir.ConstString{Value: "text"},
		BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: 100, Y1: h},
		Text: &ir.ConstString{Value: id},
	}
}

func tierOf(comps []ir.Component, id string) *ir.TextTier {
	for i := range comps {
		if comps[i].ID == id {
			return comps[i].Tier
		}
	}
	return nil
}

func TestAttachTextTiers_ThreeBands(t *testing.T) {
	// A clean 3-band page: heading ~54px, body ~22px, caption ~12px.
	comps := []ir.Component{
		textComp("h1", 54),
		textComp("b1", 22), textComp("b2", 21), textComp("b3", 23),
		textComp("c1", 12), textComp("c2", 11),
	}
	n := AttachTextTiers(comps, DefaultTierOptions())
	if n != len(comps) {
		t.Fatalf("expected all %d text comps tiered, got %d", len(comps), n)
	}
	if got := tierOf(comps, "h1"); got == nil || got.Tier != "heading" || got.Level != 0 {
		t.Errorf("h1: want heading level 0, got %+v", got)
	}
	if got := tierOf(comps, "b1"); got == nil || got.Tier != "body" {
		t.Errorf("b1: want body, got %+v", got)
	}
	if got := tierOf(comps, "c1"); got == nil || got.Tier != "caption" {
		t.Errorf("c1: want caption, got %+v", got)
	}
	if got := tierOf(comps, "h1"); got != nil && got.HeightPx != 54 {
		t.Errorf("h1 height: want 54, got %d", got.HeightPx)
	}
}

func TestAttachTextTiers_TwoBands(t *testing.T) {
	// Title vs labels, no caption band (login-light POC: 34 vs 18).
	comps := []ir.Component{
		textComp("title", 34),
		textComp("l1", 18), textComp("l2", 18), textComp("l3", 19), textComp("l4", 17),
	}
	AttachTextTiers(comps, DefaultTierOptions())
	if got := tierOf(comps, "title"); got == nil || got.Tier != "heading" {
		t.Errorf("title: want heading, got %+v", got)
	}
	if got := tierOf(comps, "l1"); got == nil || got.Tier != "body" {
		t.Errorf("l1: want body, got %+v", got)
	}
}

func TestAttachTextTiers_UniformCollapsesToBody(t *testing.T) {
	// All same size -> no spurious heading; everything body at level 0.
	comps := []ir.Component{
		textComp("a", 18), textComp("b", 18), textComp("c", 18),
		textComp("d", 19), textComp("e", 17),
	}
	AttachTextTiers(comps, DefaultTierOptions())
	for i := range comps {
		tr := comps[i].Tier
		if tr == nil || tr.Tier != "body" || tr.Level != 0 {
			t.Errorf("%s: want body level 0 (uniform), got %+v", comps[i].ID, tr)
		}
	}
}

func TestAttachTextTiers_TooFewSkips(t *testing.T) {
	// Below MinComponents: still labeled, but never invents multiple bands.
	comps := []ir.Component{textComp("a", 40), textComp("b", 12)}
	AttachTextTiers(comps, DefaultTierOptions())
	// With 2 distinct heights well separated, a 2-band split is allowed; the
	// contract is only that every text comp gets SOME tier.
	for i := range comps {
		if comps[i].Tier == nil {
			t.Errorf("%s: expected a tier", comps[i].ID)
		}
	}
}

func TestAttachTextTiers_IgnoresNonText(t *testing.T) {
	comps := []ir.Component{
		textComp("t", 30),
		{ID: "card", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X1: 100, Y1: 200}},
	}
	AttachTextTiers(comps, DefaultTierOptions())
	if tierOf(comps, "card") != nil {
		t.Error("non-text card should not be tiered")
	}
	if tierOf(comps, "t") == nil {
		t.Error("text component should be tiered")
	}
}

func TestAttachTextTiers_DoesNotOverwrite(t *testing.T) {
	comps := []ir.Component{textComp("a", 30), textComp("b", 12)}
	comps[0].Tier = &ir.TextTier{Tier: "heading", Level: 0}
	n := AttachTextTiers(comps, DefaultTierOptions())
	if n != 1 {
		t.Errorf("want 1 newly tiered (b only), got %d", n)
	}
	if comps[0].Tier.Tier != "heading" {
		t.Error("pre-set tier must not be overwritten")
	}
}

func TestAttachTextTiers_DegenerateTinyTokensSkipped(t *testing.T) {
	// Median ~11px; the 2-3px tokens are OCR mis-boxes and must be left untiered
	// so they don't drag the caption band's floor down to 2px.
	comps := []ir.Component{
		textComp("noise1", 3), textComp("noise2", 2),
		textComp("cap1", 9), textComp("cap2", 10),
		textComp("body1", 15), textComp("body2", 16),
		textComp("head1", 40),
	}
	n := AttachTextTiers(comps, DefaultTierOptions())
	if tierOf(comps, "noise1") != nil || tierOf(comps, "noise2") != nil {
		t.Errorf("degenerate tiny tokens must be left untiered")
	}
	if n != 5 {
		t.Errorf("want 5 real components tiered, got %d", n)
	}
	cap1 := tierOf(comps, "cap1")
	if cap1 == nil || cap1.Tier != "caption" {
		t.Errorf("cap1: want caption, got %+v", cap1)
	}
	if cap1 != nil && cap1.HeightPx < 5 {
		t.Errorf("caption band polluted by tiny token: floor %dpx", cap1.HeightPx)
	}
}

func TestAttachTextTiers_AllTinyStillTiers(t *testing.T) {
	// If everything is tiny (uniformly small UI), the filter must not strip the
	// whole page — it falls back to tiering all.
	comps := []ir.Component{textComp("a", 3), textComp("b", 3), textComp("c", 3)}
	n := AttachTextTiers(comps, DefaultTierOptions())
	if n != 3 {
		t.Errorf("all-tiny page: want 3 tiered (fallback), got %d", n)
	}
}

func TestAttachTextTiers_Empty(t *testing.T) {
	if n := AttachTextTiers(nil, DefaultTierOptions()); n != 0 {
		t.Errorf("nil input: want 0, got %d", n)
	}
}

func TestBandConfidence(t *testing.T) {
	centers := []float64{50, 20, 10}
	// A value right on center 0 should score high.
	if c := bandConfidence(50, centers, 0); c < 0.9 {
		t.Errorf("on-center confidence too low: %v", c)
	}
	// A value midway between center 1 (20) and center 2 (10) is ambiguous.
	if c := bandConfidence(15, centers, 1); c > 0.2 {
		t.Errorf("midpoint confidence too high: %v", c)
	}
}
