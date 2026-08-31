// Package graph builds the semantic UI graph (hierarchy + relationships)
// before any DOM inference.
package graph

import (
	"sort"

	"github.com/refraict/refraict/internal/ir"
)

// Graph is a semantic UI graph: nodes (components) plus explicit relationships.
type Graph struct {
	Nodes         []ir.Component    `json:"nodes"`
	Relationships []ir.Relationship `json:"relationships"`
}

// Build derives relationships from geometry: containment (inside), alignment
// (same_row, same_column), and spatial ordering (left_of, below, above).
func Build(comps []ir.Component) *Graph {
	g := &Graph{Nodes: comps}
	for i, a := range comps {
		for j, b := range comps {
			if i == j {
				continue
			}
			if rel := relationship(a, b); rel != nil {
				g.Relationships = append(g.Relationships, *rel)
			}
		}
	}
	return g
}

// relationship determines a directional relationship a->b when clearly
// derivable from geometry, otherwise nil.
func relationship(a, b ir.Component) *ir.Relationship {
	if a.BBox.Contains(b.BBox) && a.BBox.Area() > b.BBox.Area()*3 {
		return &ir.Relationship{A: a.ID, Relation: "contains", B: b.ID, Confidence: 0.8, Source: "geometry"}
	}
	if vertOverlap(a.BBox, b.BBox) > 0.5 && horizGap(a.BBox, b.BBox) > 0 {
		return &ir.Relationship{A: a.ID, Relation: "same_row", B: b.ID, Confidence: 0.7, Source: "geometry"}
	}
	if a.BBox.X1 <= b.BBox.X0 && vertOverlap(a.BBox, b.BBox) > 0.3 {
		return &ir.Relationship{A: a.ID, Relation: "left_of", B: b.ID, Confidence: 0.7, Source: "geometry"}
	}
	if a.BBox.Y1 <= b.BBox.Y0 && horizOverlap(a.BBox, b.BBox) > 0.3 {
		return &ir.Relationship{A: a.ID, Relation: "below", B: b.ID, Confidence: 0.7, Source: "geometry"}
	}
	return nil
}

func vertOverlap(a, b ir.BoundingBox) float64 {
	over := min(a.Y1, b.Y1) - max(a.Y0, b.Y0)
	if over <= 0 {
		return 0
	}
	minH := a.Height()
	if b.Height() < minH {
		minH = b.Height()
	}
	if minH <= 0 {
		return 0
	}
	return float64(over) / float64(minH)
}

func horizOverlap(a, b ir.BoundingBox) float64 {
	over := min(a.X1, b.X1) - max(a.X0, b.X0)
	if over <= 0 {
		return 0
	}
	minW := a.Width()
	if b.Width() < minW {
		minW = b.Width()
	}
	if minW <= 0 {
		return 0
	}
	return float64(over) / float64(minW)
}

func horizGap(a, b ir.BoundingBox) float64 {
	if a.X1 <= b.X0 {
		return float64(b.X0 - a.X1)
	}
	if b.X1 <= a.X0 {
		return float64(a.X0 - b.X1)
	}
	return 0
}

// SortByPosition orders components top-to-bottom then left-to-right.
func SortByPosition(comps []ir.Component) {
	sort.SliceStable(comps, func(i, j int) bool {
		if comps[i].BBox.Y0 != comps[j].BBox.Y0 {
			return comps[i].BBox.Y0 < comps[j].BBox.Y0
		}
		return comps[i].BBox.X0 < comps[j].BBox.X0
	})
}
