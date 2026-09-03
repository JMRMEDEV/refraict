package graph

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func comp(id string, x0, y0, x1, y1 int) ir.Component {
	return ir.Component{ID: id, BBox: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}}
}

func TestBuildContains(t *testing.T) {
	comps := []ir.Component{
		comp("outer", 0, 0, 200, 200),
		comp("inner", 20, 20, 100, 100),
	}
	g := Build(comps)
	found := false
	for _, r := range g.Relationships {
		if r.Relation == "contains" && r.A == "outer" && r.B == "inner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected outer contains inner, got %+v", g.Relationships)
	}
}

func TestBuildBelow(t *testing.T) {
	comps := []ir.Component{
		comp("top", 0, 0, 100, 50),
		comp("bottom", 0, 100, 100, 150),
	}
	g := Build(comps)
	found := false
	for _, r := range g.Relationships {
		if r.Relation == "below" && r.A == "top" && r.B == "bottom" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected top below bottom, got %+v", g.Relationships)
	}
}

func TestSortByPositionOrder(t *testing.T) {
	comps := []ir.Component{
		comp("c", 0, 200, 10, 210),
		comp("a", 50, 0, 60, 10),
		comp("b", 0, 100, 10, 110),
	}
	SortByPosition(comps)
	if comps[0].ID != "a" || comps[1].ID != "b" || comps[2].ID != "c" {
		t.Fatalf("unexpected sort order: %s, %s, %s", comps[0].ID, comps[1].ID, comps[2].ID)
	}
}

func TestDetectRepeatingGroups(t *testing.T) {
	// 3 cards in a column (same x-center, spaced along y).
	cs := []ir.Component{
		{ID: "c1", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X0: 100, Y0: 100, X1: 660, Y1: 400}},
		{ID: "c2", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X0: 100, Y0: 450, X1: 660, Y1: 750}},
		{ID: "c3", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X0: 100, Y0: 800, X1: 660, Y1: 1100}},
		{ID: "t1", Type: ir.ConstString{Value: "text"}, BBox: ir.BoundingBox{X0: 110, Y0: 110, X1: 300, Y1: 130}},
	}
	groups := DetectRepeatingGroups(cs, 50, 80, 2)
	// Should find at least one group of 3 cards along the x axis (same xc).
	found := false
	for _, g := range groups {
		if g.Type == "card" && len(g.MemberIDs) == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 3-card group, got %+v", groups)
	}
	// Text components must not form groups (skipped).
	for _, g := range groups {
		if g.Type == "text" {
			t.Fatal("text components must not form repeating groups")
		}
	}
}

func TestAssociateHeaders(t *testing.T) {
	comps := []ir.Component{
		// Column header token above the cards.
		{ID: "h1", Type: ir.ConstString{Value: "text"}, Text: &ir.ConstString{Value: "TO DO (4)"}, BBox: ir.BoundingBox{X0: 100, Y0: 40, X1: 300, Y1: 70}},
		// Two stacked cards forming an x-axis (same x-center) group.
		{ID: "c1", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X0: 100, Y0: 100, X1: 400, Y1: 300}},
		{ID: "c2", Type: ir.ConstString{Value: "card"}, BBox: ir.BoundingBox{X0: 100, Y0: 350, X1: 400, Y1: 550}},
	}
	groups := []ir.RepeatedGroup{
		{Axis: "x", Type: "card", MemberIDs: []string{"c1", "c2"}},
		{Axis: "y", Type: "card", MemberIDs: []string{"c1", "c2"}},
	}
	AssociateHeaders(groups, comps)
	if groups[0].Header != "TO DO (4)" || groups[0].HeaderID != "h1" {
		t.Fatalf("x-axis group should be headed 'TO DO (4)', got %q/%q", groups[0].Header, groups[0].HeaderID)
	}
	if groups[1].Header != "" {
		t.Fatalf("y-axis group must not be headed, got %q", groups[1].Header)
	}
}
