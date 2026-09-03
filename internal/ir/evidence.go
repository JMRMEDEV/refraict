package ir

// OCRToken is a single OCR-detected text piece with its bounding box (global
// coordinates), confidence, and optional orientation.
type OCRToken struct {
	Text       string      `json:"text"`
	BBoxGlobal BoundingBox `json:"bbox_global"`
	Confidence float64     `json:"confidence"`
	Orientation string     `json:"orientation,omitempty"`
	Source     string      `json:"source,omitempty"`
	LineNumber int         `json:"line,omitempty"`
}

// ColorFact is a measured or inferred color for a region with provenance.
type ColorFact struct {
	Name       string      `json:"name"` // e.g. background, foreground, border
	Value      string      `json:"value"` // hex like #2563EB
	RGB        [3]int      `json:"rgb"`   // 0-255
	BBoxGlobal BoundingBox `json:"bbox_global"`
	Source     string      `json:"source"`
	Confidence float64     `json:"confidence"`
}

// PageType is the structured result of deterministic page classification.
// It carries the classified type, the evidence signals that drove the decision,
// and a coarse confidence so the calling agent knows WHY the classification was
// made and how much to trust it. All fields are deterministic (no model).
type PageType struct {
	Type       string   `json:"type"`                  // e.g. "task_detail", "kanban", "error_state"
	Signals    []string `json:"signals"`               // the keywords/tokens that matched
	Confidence float64  `json:"confidence"`             // coarse: 0.0–1.0
}
