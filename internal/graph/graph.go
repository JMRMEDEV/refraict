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

// AssociateHeaders (Milestone E) attaches a section/column header to each
// repeated group: a text token sitting directly ABOVE the group's top edge (a
// small vertical gap) whose x-range overlaps the group's x-range. This turns an
// anonymous sibling group into a NAMED one ("TO DO (4)" for a kanban column) so
// the agent gets group semantics for free. Deterministic; no model.
//
// Header candidates are ranked by: smallest vertical gap above the group, then
// greatest x-overlap. Only text components are considered; ones already used as
// a member are excluded. Fragile by nature (font styling varies), so it only
// attaches when a candidate clearly sits above and overlaps — otherwise the group
// is left unnamed.
func AssociateHeaders(groups []ir.RepeatedGroup, comps []ir.Component) {
	byID := map[string]ir.Component{}
	for _, c := range comps {
		byID[c.ID] = c
	}
	for gi := range groups {
		g := &groups[gi]
		// Header-above-group semantics apply to x-axis groups (a horizontal set
		// of siblings — e.g. a kanban column's cards, a card row — with a header
		// on top). A y-axis group is a vertical stack whose "top header" is either
		// redundant with the x-group's header or noise, so skip it.
		if g.Axis != "x" {
			continue
		}
		member := map[string]bool{}
		gx0, gy0, gx1 := 1<<30, 1<<30, -(1 << 30)
		for _, id := range g.MemberIDs {
			member[id] = true
			b := byID[id].BBox
			if b.X0 < gx0 {
				gx0 = b.X0
			}
			if b.X1 > gx1 {
				gx1 = b.X1
			}
			if b.Y0 < gy0 {
				gy0 = b.Y0
			}
		}
		if gx1 <= gx0 {
			continue
		}
		bestID, bestGap, bestOverlap := "", 1<<30, 0
		for _, c := range comps {
			if member[c.ID] || c.Type.Value != "text" || c.Text == nil {
				continue
			}
			b := c.BBox
			gap := gy0 - b.Y1 // header bottom to group top
			// Header must sit ABOVE the group within a bounded gap.
			if gap < 0 || gap > 120 {
				continue
			}
			// Horizontal overlap with the group's x-span.
			ox0, ox1 := maxI(b.X0, gx0), minI(b.X1, gx1)
			overlap := ox1 - ox0
			if overlap <= 0 {
				continue
			}
			// Prefer the closest header; break ties by larger overlap.
			if gap < bestGap || (gap == bestGap && overlap > bestOverlap) {
				bestID, bestGap, bestOverlap = c.ID, gap, overlap
			}
		}
		if bestID != "" {
			g.Header = byID[bestID].Text.Value
			g.HeaderID = bestID
		}
	}
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AttachPadding computes each container's inset to its nearest children
// (Milestone G) from the graph's "contains" relationships and attaches ir.Padding
// to the container component. Left/Top are reliable content insets; Right/Bottom
// are content-dependent slack, flagged via ContentFills (children span >=60% of
// the container both ways). Pure box arithmetic; no pixels/models.
func AttachPadding(comps []ir.Component, rels []ir.Relationship) int {
	idx := map[string]int{}
	for i := range comps {
		idx[comps[i].ID] = i
	}
	children := map[string][]string{}
	for _, r := range rels {
		if r.Relation == "contains" {
			children[r.A] = append(children[r.A], r.B)
		}
	}
	n := 0
	for pid, kids := range children {
		pi, ok := idx[pid]
		if !ok || len(kids) == 0 {
			continue
		}
		p := comps[pi].BBox
		cx0, cy0 := 1<<30, 1<<30
		cx1, cy1 := -(1 << 30), -(1 << 30)
		for _, kid := range kids {
			ki, ok := idx[kid]
			if !ok {
				continue
			}
			b := comps[ki].BBox
			if b.X0 < cx0 {
				cx0 = b.X0
			}
			if b.Y0 < cy0 {
				cy0 = b.Y0
			}
			if b.X1 > cx1 {
				cx1 = b.X1
			}
			if b.Y1 > cy1 {
				cy1 = b.Y1
			}
		}
		if cx1 < cx0 || cy1 < cy0 {
			continue
		}
		pad := &ir.Padding{
			Left:   cx0 - p.X0,
			Right:  p.X1 - cx1,
			Top:    cy0 - p.Y0,
			Bottom: p.Y1 - cy1,
		}
		pw, ph := p.Width(), p.Height()
		if pw > 0 && ph > 0 {
			pad.ContentFills = (cx1-cx0)*10 >= pw*6 && (cy1-cy0)*10 >= ph*6
		}
		comps[pi].Padding = pad
		n++
	}
	return n
}

// AttachGroupGaps fills GapMedian/GapSpread for each repeated group from the
// gaps between adjacent members along the group's spatial axis (Milestone G).
func AttachGroupGaps(groups []ir.RepeatedGroup, comps []ir.Component) {
	byID := map[string]ir.BoundingBox{}
	for _, c := range comps {
		byID[c.ID] = c.BBox
	}
	for gi := range groups {
		g := &groups[gi]
		if len(g.MemberIDs) < 2 {
			continue
		}
		// Members are stored in cross-axis spatial order (see makeGroup): an
		// x-axis group varies along y, a y-axis group varies along x.
		crossAxis := "y"
		if g.Axis == "y" {
			crossAxis = "x"
		}
		ids := append([]string(nil), g.MemberIDs...)
		sort.Slice(ids, func(i, j int) bool {
			bi, bj := byID[ids[i]], byID[ids[j]]
			if crossAxis == "x" {
				return bi.X0 < bj.X0
			}
			return bi.Y0 < bj.Y0
		})
		var gaps []int
		for i := 0; i+1 < len(ids); i++ {
			cur, nxt := byID[ids[i]], byID[ids[i+1]]
			var gap int
			if crossAxis == "x" {
				gap = nxt.X0 - cur.X1
			} else {
				gap = nxt.Y0 - cur.Y1
			}
			if gap < 0 {
				gap = 0
			}
			gaps = append(gaps, gap)
		}
		if len(gaps) == 0 {
			continue
		}
		sorted := append([]int(nil), gaps...)
		sort.Ints(sorted)
		g.GapMedian = sorted[len(sorted)/2]
		g.GapSpread = sorted[len(sorted)-1] - sorted[0]
	}
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
