package graph

import (
	"sort"

	"github.com/refraict/refraict/internal/ir"
)

// Layout hierarchy (Milestone J) — ADDITIVE layer.
//
// BuildLayoutTree derives a nesting hierarchy + per-child occupancy shares from
// the ALREADY-RECONCILED components and the detected RepeatedGroups. It is
// purely additive: it references component IDs and never mutates the flat
// component set or the relationship list, so no per-element measurement feature
// (colors, corner-style, tier, weight, crosscheck) is affected. Two structure
// sources are combined:
//
//   - Bordered nesting: parent = the smallest component that strictly contains a
//     component (cards inside panels inside regions that survived reconciliation).
//   - Invisible containers: RepeatedGroups (same-type, regularly-spaced siblings)
//     become synthetic column/row nodes wrapping their members, gated by spacing
//     regularity so noise clusters are withheld.
//
// Occupancy shares (LayoutShare) are attached per direct child along the parent's
// dominant stacking axis — the "how much does this child take" signal.

// LayoutOptions tunes the layout-tree builder.
type LayoutOptions struct {
	// MinContainerConfidence: an inferred (invisible) container is emitted only
	// when its member-spacing regularity confidence is at least this.
	MinContainerConfidence float64
	// ContainAreaRatio: a is a parent of b only when a strictly contains b and
	// a.Area > b.Area * this (mirrors graph.relationship's containment guard, so
	// the tree agrees with the existing "contains" relation).
	ContainAreaRatio float64
}

// DefaultLayoutOptions returns tuned defaults (regularity gate validated in the
// dev/invcont2poc POC). ContainAreaRatio is 1.05 (not the flat relationship
// set's 3x noise-guard): layout nesting wants the smallest STRICT container even
// when a child legitimately fills ~90% of its parent (a body inside a panel);
// picking the smallest container already avoids over-nesting.
func DefaultLayoutOptions() LayoutOptions {
	return LayoutOptions{MinContainerConfidence: 0.5, ContainAreaRatio: 1.05}
}

// BuildLayoutTree builds the additive layout hierarchy. comps is the canonical
// (unmodified) component set; groups are the detected RepeatedGroups (with
// headers + gaps already attached). Returns a tree referencing component IDs.
func BuildLayoutTree(comps []ir.Component, groups []ir.RepeatedGroup, opts LayoutOptions) ir.LayoutTree {
	if len(comps) == 0 {
		return ir.LayoutTree{}
	}
	idx := map[string]int{}
	for i := range comps {
		idx[comps[i].ID] = i
	}

	// 1. Synthesize inferred containers from regular RepeatedGroups (gated).
	var inf []inferredContainer
	memberOwner := map[string]string{} // component ID -> inferred container ID
	for gi, g := range groups {
		if len(g.MemberIDs) < 2 {
			continue
		}
		conf := 1.0
		if g.GapMedian > 0 {
			conf = 1.0 - float64(g.GapSpread)/float64(g.GapMedian)
		}
		if conf < 0 {
			conf = 0
		}
		if conf < opts.MinContainerConfidence {
			continue
		}
		bb, ok := boundOf(comps, idx, g.MemberIDs)
		if !ok {
			continue
		}
		// Kind: members sharing an x-band (vary in y) are a column; else a row.
		kind := "row"
		if groupIsColumn(comps, idx, g.MemberIDs) {
			kind = "column"
		}
		id := "L" + itoa(gi)
		mset := map[string]bool{}
		for _, m := range g.MemberIDs {
			if _, seen := memberOwner[m]; seen {
				continue // a component belongs to at most one inferred container
			}
			mset[m] = true
			memberOwner[m] = id
		}
		if len(mset) < 2 {
			continue
		}
		inf = append(inf, inferredContainer{id: id, kind: kind, label: g.Header, conf: round2(conf), bbox: bb, members: mset})
	}

	// 2. Parent assignment for each REAL component: the smallest strict container
	// among (other real components) — this is the bordered-nesting tree. A
	// component owned by an inferred container is parented to it instead.
	parent := make([]string, len(comps)) // parent ID ("" = root candidate)
	for i := range comps {
		if owner, ok := memberOwner[comps[i].ID]; ok {
			parent[i] = owner
			continue
		}
		best := -1
		bestArea := 1 << 62
		for j := range comps {
			if i == j {
				continue
			}
			if comps[j].BBox.Contains(comps[i].BBox) &&
				float64(comps[j].BBox.Area()) > float64(comps[i].BBox.Area())*opts.ContainAreaRatio &&
				comps[j].BBox.Area() < bestArea {
				// skip if j is itself an inferred member (its container is the parent)
				best = j
				bestArea = comps[j].BBox.Area()
			}
		}
		if best >= 0 {
			parent[i] = comps[best].ID
		}
	}

	// 3. Assemble nodes. Real-component nodes keyed by ID; inferred nodes too.
	childrenOf := map[string][]string{} // parent ID -> child component IDs (real)
	for i := range comps {
		if parent[i] != "" {
			childrenOf[parent[i]] = append(childrenOf[parent[i]], comps[i].ID)
		}
	}
	// Inferred containers: parent them under the smallest real container that
	// contains the inferred bbox (else root).
	infParent := map[string]string{}
	for _, c := range inf {
		best := ""
		bestArea := 1 << 62
		for j := range comps {
			if memberOwner[comps[j].ID] == c.id {
				continue // don't parent a container under its own member
			}
			if comps[j].BBox.Contains(c.bbox) && comps[j].BBox.Area() > area(c.bbox) && comps[j].BBox.Area() < bestArea {
				best = comps[j].ID
				bestArea = comps[j].BBox.Area()
			}
		}
		infParent[c.id] = best
	}

	infByID := map[string]inferredContainer{}
	for _, c := range inf {
		infByID[c.id] = c
	}

	var build func(compID string) ir.LayoutNode
	build = func(compID string) ir.LayoutNode {
		i := idx[compID]
		n := ir.LayoutNode{ComponentID: compID, BBox: comps[i].BBox}
		for _, kid := range childrenOf[compID] {
			n.Children = append(n.Children, build(kid))
		}
		// inferred containers parented here
		for _, c := range inf {
			if infParent[c.id] == compID {
				n.Children = append(n.Children, buildInferred(c.id, infByID, comps, idx, childrenOfMembers(c.members)))
			}
		}
		attachShares(&n)
		return n
	}

	// roots: real comps with no parent + inferred containers with no real parent.
	var tree ir.LayoutTree
	for i := range comps {
		if parent[i] == "" && memberOwner[comps[i].ID] == "" {
			tree.Roots = append(tree.Roots, build(comps[i].ID))
		}
	}
	for _, c := range inf {
		if infParent[c.id] == "" {
			tree.Roots = append(tree.Roots, buildInferred(c.id, infByID, comps, idx, childrenOfMembers(c.members)))
		}
	}
	// Stable order: by top, then left.
	sortNodes(tree.Roots)
	return tree
}

// childrenOfMembers returns the member IDs of an inferred container as a slice.
func childrenOfMembers(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func buildInferred(id string, infByID map[string]inferredContainer, comps []ir.Component, idx map[string]int, memberIDs []string) ir.LayoutNode {
	c := infByID[id]
	n := ir.LayoutNode{
		Inferred:   true,
		Kind:       c.kind,
		BBox:       c.bbox,
		Label:      c.label,
		Confidence: c.conf,
	}
	for _, m := range memberIDs {
		if i, ok := idx[m]; ok {
			child := ir.LayoutNode{ComponentID: m, BBox: comps[i].BBox}
			n.Children = append(n.Children, child)
		}
	}
	attachShares(&n)
	return n
}

// inferredContainer is a synthesized invisible container (column/row) from a
// gated RepeatedGroup: it references member component IDs and never owns them.
type inferredContainer struct {
	id      string
	kind    string
	label   string
	conf    float64
	bbox    ir.BoundingBox
	members map[string]bool
}

// attachShares computes each direct child's occupancy fraction along the node's
// dominant stacking axis + a tiling coverage score, and sets LayoutShare.
func attachShares(n *ir.LayoutNode) {
	if len(n.Children) < 2 {
		return
	}
	minY, maxY, minX, maxX := 1<<30, -(1 << 30), 1<<30, -(1 << 30)
	for _, c := range n.Children {
		if c.BBox.Y0 < minY {
			minY = c.BBox.Y0
		}
		if c.BBox.Y1 > maxY {
			maxY = c.BBox.Y1
		}
		if c.BBox.X0 < minX {
			minX = c.BBox.X0
		}
		if c.BBox.X1 > maxX {
			maxX = c.BBox.X1
		}
	}
	vertical := (maxY - minY) >= (maxX - minX)
	axis := "y"
	span := maxY - minY
	parentExtent := n.BBox.Height()
	if !vertical {
		axis, span, parentExtent = "x", maxX-minX, n.BBox.Width()
	}
	if span <= 0 {
		return
	}
	tiling := 0.0
	if parentExtent > 0 {
		tiling = round2(float64(span) / float64(parentExtent))
	}
	// order children along axis
	sort.Slice(n.Children, func(i, j int) bool {
		if vertical {
			return n.Children[i].BBox.Y0 < n.Children[j].BBox.Y0
		}
		return n.Children[i].BBox.X0 < n.Children[j].BBox.X0
	})
	prevEnd := -1
	for k := range n.Children {
		var ext, start, end int
		if vertical {
			ext, start, end = n.Children[k].BBox.Height(), n.Children[k].BBox.Y0, n.Children[k].BBox.Y1
		} else {
			ext, start, end = n.Children[k].BBox.Width(), n.Children[k].BBox.X0, n.Children[k].BBox.X1
		}
		gapFrac := 0.0
		if prevEnd >= 0 && start > prevEnd {
			gapFrac = round2(float64(start-prevEnd) / float64(span))
		}
		n.Children[k].Share = &ir.LayoutShare{
			Axis:          axis,
			Fraction:      round2(float64(ext) / float64(span)),
			GapBeforeFrac: gapFrac,
			Tiling:        tiling,
		}
		prevEnd = end
	}
}

func groupIsColumn(comps []ir.Component, idx map[string]int, ids []string) bool {
	// column = members share x extent (overlap horizontally), stack vertically.
	if len(ids) < 2 {
		return false
	}
	a, okA := idx[ids[0]]
	b, okB := idx[ids[1]]
	if !okA || !okB {
		return false
	}
	xo := overlap(comps[a].BBox.X0, comps[a].BBox.X1, comps[b].BBox.X0, comps[b].BBox.X1)
	yo := overlap(comps[a].BBox.Y0, comps[a].BBox.Y1, comps[b].BBox.Y0, comps[b].BBox.Y1)
	return xo > yo
}

func overlap(a0, a1, b0, b1 int) int {
	o := minI(a1, b1) - maxI(a0, b0)
	if o < 0 {
		return 0
	}
	return o
}

func boundOf(comps []ir.Component, idx map[string]int, ids []string) (ir.BoundingBox, bool) {
	var out ir.BoundingBox
	first := true
	for _, id := range ids {
		i, ok := idx[id]
		if !ok {
			continue
		}
		b := comps[i].BBox
		if first {
			out, first = b, false
			continue
		}
		if b.X0 < out.X0 {
			out.X0 = b.X0
		}
		if b.Y0 < out.Y0 {
			out.Y0 = b.Y0
		}
		if b.X1 > out.X1 {
			out.X1 = b.X1
		}
		if b.Y1 > out.Y1 {
			out.Y1 = b.Y1
		}
	}
	return out, !first
}

func sortNodes(ns []ir.LayoutNode) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].BBox.Y0 != ns[j].BBox.Y0 {
			return ns[i].BBox.Y0 < ns[j].BBox.Y0
		}
		return ns[i].BBox.X0 < ns[j].BBox.X0
	})
}

func area(b ir.BoundingBox) int { return b.Width() * b.Height() }

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
