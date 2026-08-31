package ir

// ORCToken is a single OCR-detected text piece with its bounding box (global
// coordinates), confidence, and optional orientation.
type ORCToken struct {
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
