package graph

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func lcomp(id, typ string, x0, y0, x1, y1 int) ir.Component {
	return ir.Component{
		ID:   id,
		Type: ir.ConstString{Value: typ},
		BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1},
	}
}

func findNode(roots []ir.LayoutNode, id string) *ir.LayoutNode {
	for i := range roots {
		if roots[i].ComponentID == id {
			return &roots[i]
		}
		if n := findNode(roots[i].Children, id); n != nil {
			return n
		}
	}
	return nil
}

func TestBuildLayoutTree_BorderedNesting(t *testing.T) {
	// A big panel containing two stacked children (header + body).
	comps := []ir.Component{
		lcomp("panel", "panel", 0, 0, 300, 400),
		lcomp("header", "text", 10, 10, 290, 50),  // ~10% of 400
		lcomp("body", "card", 10, 60, 290, 390),   // ~90%
	}
	tree := BuildLayoutTree(comps, nil, DefaultLayoutOptions())
	panel := findNode(tree.Roots, "panel")
	if panel == nil {
		t.Fatal("panel should be a root")
	}
	if len(panel.Children) != 2 {
		t.Fatalf("panel should have 2 children, got %d", len(panel.Children))
	}
	// Shares along vertical axis: header small, body large.
	h := findNode(tree.Roots, "header")
	b := findNode(tree.Roots, "body")
	if h.Share == nil || b.Share == nil {
		t.Fatal("children should have shares")
	}
	if h.Share.Axis != "y" {
		t.Errorf("expected vertical axis, got %s", h.Share.Axis)
	}
	if !(b.Share.Fraction > h.Share.Fraction) {
		t.Errorf("body (%.2f) should occupy more than header (%.2f)", b.Share.Fraction, h.Share.Fraction)
	}
}

func TestBuildLayoutTree_InferredColumnFromGroup(t *testing.T) {
	// Three cards stacked vertically in a column, as a regular group.
	comps := []ir.Component{
		lcomp("c1", "card", 20, 10, 120, 60),
		lcomp("c2", "card", 20, 70, 120, 120),
		lcomp("c3", "card", 20, 130, 120, 180),
	}
	groups := []ir.RepeatedGroup{{
		Axis: "y", Type: "card", MemberIDs: []string{"c1", "c2", "c3"},
		GapMedian: 10, GapSpread: 0, Header: "TO DO (3)",
	}}
	tree := BuildLayoutTree(comps, groups, DefaultLayoutOptions())
	// Find the inferred container.
	var inf *ir.LayoutNode
	var walk func(ns []ir.LayoutNode)
	walk = func(ns []ir.LayoutNode) {
		for i := range ns {
			if ns[i].Inferred {
				inf = &ns[i]
			}
			walk(ns[i].Children)
		}
	}
	walk(tree.Roots)
	if inf == nil {
		t.Fatal("expected an inferred container")
	}
	if inf.Kind != "column" {
		t.Errorf("expected column, got %s", inf.Kind)
	}
	if inf.Label != "TO DO (3)" {
		t.Errorf("expected header label, got %q", inf.Label)
	}
	if len(inf.Children) != 3 {
		t.Errorf("column should have 3 members, got %d", len(inf.Children))
	}
	if inf.Confidence < 0.99 {
		t.Errorf("even spacing should give conf ~1, got %v", inf.Confidence)
	}
}

func TestBuildLayoutTree_WithholdsIrregularGroup(t *testing.T) {
	comps := []ir.Component{
		lcomp("c1", "card", 20, 10, 120, 60),
		lcomp("c2", "card", 20, 70, 120, 120),
		lcomp("c3", "card", 20, 400, 120, 450),
	}
	// Very irregular spacing -> low confidence -> withheld.
	groups := []ir.RepeatedGroup{{
		Axis: "y", Type: "card", MemberIDs: []string{"c1", "c2", "c3"},
		GapMedian: 10, GapSpread: 300,
	}}
	tree := BuildLayoutTree(comps, groups, DefaultLayoutOptions())
	for _, r := range tree.Roots {
		if r.Inferred {
			t.Error("irregular group should be withheld (no inferred container)")
		}
	}
}

func TestBuildLayoutTree_Empty(t *testing.T) {
	tree := BuildLayoutTree(nil, nil, DefaultLayoutOptions())
	if len(tree.Roots) != 0 {
		t.Errorf("empty input: want 0 roots, got %d", len(tree.Roots))
	}
}

func TestBuildLayoutTree_FlatWhenNoContainment(t *testing.T) {
	// Two non-overlapping, non-containing components -> both roots, no nesting.
	comps := []ir.Component{
		lcomp("a", "text", 0, 0, 50, 20),
		lcomp("b", "text", 0, 100, 50, 120),
	}
	tree := BuildLayoutTree(comps, nil, DefaultLayoutOptions())
	if len(tree.Roots) != 2 {
		t.Errorf("want 2 flat roots, got %d", len(tree.Roots))
	}
	for _, r := range tree.Roots {
		if len(r.Children) != 0 {
			t.Error("no nesting expected")
		}
	}
}
