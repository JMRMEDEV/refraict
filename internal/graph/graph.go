// Package graph builds the semantic UI graph (hierarchy + relationships)
// before any DOM inference.
package graph

import (
	"sort"

	"github.com/refraict/refraict/internal/ir"
)

// Graph is a semantic UI graph: nodes (components) plus explicit relationships.
type Graph struct {
	Nodes          []ir.Component    `json:"nodes"`
	Relationships  []ir.Relationship `json:"relationships"`
	RepeatedGroups []ir.RepeatedGroup `json:"repeated_groups,omitempty"`
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

// DetectRepeatingGroups finds sets of similarly-sized, same-typed components at
// regular spacing along one axis. Each group tells the agent "these N things are
// siblings in a list/grid/column set" — kanban columns, nav items, card rows.
// Pure geometry + type matching, no model.
func DetectRepeatingGroups(comps []ir.Component, sizeTol, axisTol, minMembers int) []ir.RepeatedGroup {
	if minMembers < 2 {
		minMembers = 2
	}
	byType := map[string][]ir.Component{}
	for _, c := range comps {
		t := c.Type.Value
		if t == "" || t == "text" || t == "label" {
			continue
		}
		byType[t] = append(byType[t], c)
	}
	var groups []ir.RepeatedGroup
	for typ, cs := range byType {
		if len(cs) < minMembers {
			continue
		}
		used := make([]bool, len(cs))
		for i := 0; i < len(cs); i++ {
			if used[i] {
				continue
			}
			cluster := []ir.Component{cs[i]}
			used[i] = true
			wi, hi := cs[i].BBox.Width(), cs[i].BBox.Height()
			for j := i + 1; j < len(cs); j++ {
				if used[j] {
					continue
				}
				wj, hj := cs[j].BBox.Width(), cs[j].BBox.Height()
				if intAbs(wi-wj) <= sizeTol && intAbs(hi-hj) <= sizeTol {
					cluster = append(cluster, cs[j])
					used[j] = true
				}
			}
			if len(cluster) < minMembers {
				continue
			}
			groups = append(groups, axisClusters(cluster, "x", axisTol, minMembers, typ)...)
			groups = append(groups, axisClusters(cluster, "y", axisTol, minMembers, typ)...)
		}
	}
	return groups
}

func axisClusters(cs []ir.Component, axis string, axisTol, minMembers int, typ string) []ir.RepeatedGroup {
	sorted := make([]ir.Component, len(cs))
	copy(sorted, cs)
	sort.Slice(sorted, func(i, j int) bool {
		return centerAxis(sorted[i], axis) < centerAxis(sorted[j], axis)
	})
	var groups []ir.RepeatedGroup
	cluster := []ir.Component{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		gap := centerAxis(sorted[i], axis) - centerAxis(sorted[i-1], axis)
		if gap > axisTol {
			if g, ok := makeGroup(cluster, axis, typ, minMembers); ok {
				groups = append(groups, g)
			}
			cluster = nil
		}
		cluster = append(cluster, sorted[i])
	}
	if g, ok := makeGroup(cluster, axis, typ, minMembers); ok {
		groups = append(groups, g)
	}
	return groups
}

func makeGroup(cs []ir.Component, axis, typ string, minMembers int) (ir.RepeatedGroup, bool) {
	if len(cs) < minMembers {
		return ir.RepeatedGroup{}, false
	}
	crossAxis := "y"
	if axis == "y" {
		crossAxis = "x"
	}
	sort.Slice(cs, func(i, j int) bool {
		return centerAxis(cs[i], crossAxis) < centerAxis(cs[j], crossAxis)
	})
	spacing := 0
	if len(cs) > 1 {
		total := centerAxis(cs[len(cs)-1], crossAxis) - centerAxis(cs[0], crossAxis)
		spacing = total / (len(cs) - 1)
	}
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	// Suppress degenerate groups: same-position (spacing ~ 0) is not a repeating
	// pattern — it's overlapping components that happen to be same-typed/sized.
	if spacing < 20 {
		return ir.RepeatedGroup{}, false
	}
	return ir.RepeatedGroup{Axis: axis, Spacing: spacing, Type: typ, MemberIDs: ids}, true
}

func centerAxis(c ir.Component, axis string) int {
	if axis == "x" {
		return (c.BBox.X0 + c.BBox.X1) / 2
	}
	return (c.BBox.Y0 + c.BBox.Y1) / 2
}

func intAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
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
