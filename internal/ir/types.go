// Package ir defines Refraict's canonical UI Intermediate Representation (UI IR).
//
// The UI IR is the contract of the pipeline. Every model backend is an adapter
// into this representation rather than an authority over its schema. Each
// significant value carries provenance (source) and confidence so downstream
// stages and consumers can weigh evidence rather than trusting a single guess.
package ir

import "encoding/json"

// BoundingBox is a rectangle in a canonical global coordinate system (pixels
// of the original screenshot). Coordinates are [x0, y0, x1, y1] (left, top,
// right, bottom).
//
// It deserializes from either the canonical object form ("x0","y0","x1","y1")
// or a positional array [x0, y0, x1, y1] — the latter being what vision-model
// prompts ask backends to emit.
type BoundingBox struct {
	X0 int `json:"x0"`
	Y0 int `json:"y0"`
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
}

// Width returns the width of the box.
func (b BoundingBox) Width() int { return b.X1 - b.X0 }

// Height returns the height of the box.
func (b BoundingBox) Height() int { return b.Y1 - b.Y0 }

// Area returns the area of the box.
func (b BoundingBox) Area() int { return b.Width() * b.Height() }

// Empty reports whether the box has no area.
func (b BoundingBox) Empty() bool { return b.X0 >= b.X1 || b.Y0 >= b.Y1 }

// UnmarshalJSON accepts both the canonical object form and a positional
// [x0, y0, x1, y1] array (the form vision models are prompted to emit).
func (b *BoundingBox) UnmarshalJSON(data []byte) error {
	// Try array form first: [x0, y0, x1, y1]
	var arr []int
	if err := json.Unmarshal(data, &arr); err == nil {
		if len(arr) == 4 {
			b.X0, b.Y0, b.X1, b.Y1 = arr[0], arr[1], arr[2], arr[3]
			return nil
		}
	}
	// Fall back to canonical object form.
	var obj struct {
		X0 int `json:"x0"`
		Y0 int `json:"y0"`
		X1 int `json:"x1"`
		Y1 int `json:"y1"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	b.X0, b.Y0, b.X1, b.Y1 = obj.X0, obj.Y0, obj.X1, obj.Y1
	return nil
}

// IoU computes the Intersection-over-Union of two boxes.
func (b BoundingBox) IoU(o BoundingBox) float64 {
	ix0 := max(b.X0, o.X0)
	iy0 := max(b.Y0, o.Y0)
	ix1 := min(b.X1, o.X1)
	iy1 := min(b.Y1, o.Y1)
	if ix1 <= ix0 || iy1 <= iy0 {
		return 0
	}
	inter := (ix1 - ix0) * (iy1 - iy0)
	union := b.Area() + o.Area() - inter
	if union <= 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// Contains reports whether b fully contains o.
func (b BoundingBox) Contains(o BoundingBox) bool {
	return b.X0 <= o.X0 && b.Y0 <= o.Y0 && b.X1 >= o.X1 && b.Y1 >= o.Y1
}

// Overlaps reports whether the two boxes share any area.
func (b BoundingBox) Overlaps(o BoundingBox) bool {
	return b.X0 < o.X1 && o.X0 < b.X1 && b.Y0 < o.Y1 && o.Y0 < b.Y1
}

// Value represents a typed value with provenance and confidence.
type Value[T any] struct {
	Value      T       `json:"value"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

// ConstString is a Value[string] for semantic string fields, e.g. type or role.
type ConstString = Value[string]

// Component is a single normalized UI element in the canonical representation.
type Component struct {
	ID         string         `json:"id"`
	Type       ConstString    `json:"type"`
	BBox       BoundingBox    `json:"bbox"`
	BBoxLocal  *BoundingBox   `json:"bbox_local,omitempty"`
	Text       *ConstString   `json:"text,omitempty"`
	Appearance *Appearance    `json:"appearance,omitempty"`
	Semantic   *ConstString   `json:"semantic,omitempty"`
	// SemanticHint is a deterministic interpretation of the component's text via
	// known UI patterns (e.g. overdue_deadline, completion_ratio, email,
	// git_branch_ref). Kept distinct from Semantic (VLM-voted labels): this is
	// pattern-matched, not model-inferred, so the agent knows it's rule-based.
	SemanticHint *SemanticHint  `json:"semantic_hint,omitempty"`
	// CornerStyle is a deterministic rounded|square classification of a
	// card/region/panel's corners, measured from pixels (Milestone F). Empty when
	// not applicable or low-confidence. Lets an agent settle "rounded vs square"
	// visual-verification disputes without a paid vision read.
	CornerStyle *CornerStyle   `json:"corner_style,omitempty"`
	Children   []string       `json:"children,omitempty"`
	Role       *ConstString   `json:"role,omitempty"`
	Confidence float64        `json:"confidence"`
	Source     string         `json:"source,omitempty"`
	Provenance *RunProvenance `json:"provenance,omitempty"`
}

// Appearance captures measured or inferred visual properties of a component.
type Appearance struct {
	Background  *Value[string] `json:"background,omitempty"`
	Foreground  *Value[string] `json:"foreground,omitempty"`
	RadiusPx    *Value[int]    `json:"radius_px,omitempty"`
	BorderColor *Value[string] `json:"border_color,omitempty"`
}

// Relationship describes a pairwise relationship between two components.
type Relationship struct {
	A          string  `json:"a"`
	Relation   string  `json:"relation"` // e.g. inside, left_of, same_row, below
	B          string  `json:"b"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

// RunProvenance records which model/prompt/schema produced a value.
type RunProvenance struct {
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	Provider      string `json:"provider,omitempty"`
}

// RepeatedGroup is a set of similarly-sized, same-typed components at regular
// spacing along one axis — kanban columns, nav items, card rows, settings rows.
// The agent uses these to infer sibling relationships ("these 4 cards are in a
// grid/list") without refraict assembling the container. Deterministic geometry.
type RepeatedGroup struct {
	Axis      string   `json:"axis"`       // "x" (column-like) or "y" (row-like)
	Spacing   int      `json:"spacing"`    // average center-to-center spacing (px)
	Type      string   `json:"type"`       // shared component type (e.g. "card")
	MemberIDs []string `json:"member_ids"` // component IDs in axis order
	// Header is the text of a section/column header sitting directly above the
	// group (Milestone E), e.g. "TO DO (4)" for a kanban column of cards. Empty
	// when no header is confidently associated. HeaderID is the header
	// component's ID.
	Header   string `json:"header,omitempty"`
	HeaderID string `json:"header_id,omitempty"`
}

// SemanticHint is a deterministic, pattern-matched interpretation of a text
// component — e.g. {Kind: "completion_ratio", Value: "2/5"} or
// {Kind: "overdue_deadline"}. It tells the agent what a token MEANS without the
// agent re-parsing raw text. Rule-based (no model); Kind is the pattern name and
// Value optionally carries the extracted datum.
type SemanticHint struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// CornerStyle is a deterministic classification of a region's corners as
// "rounded" or "square", measured from pixels (Milestone F). Confidence reflects
// how cleanly the corner pixels separate the region fill from the page
// background; RoundedCorners is the count (0-4) of corners that read as
// background (rounded).
type CornerStyle struct {
	Style          string  `json:"style"` // "rounded" | "square"
	Confidence     float64 `json:"confidence"`
	RoundedCorners int     `json:"rounded_corners"` // 0..4
}
