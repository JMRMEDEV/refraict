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
