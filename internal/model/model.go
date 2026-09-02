// Package model defines the generic inference backend interfaces and
// provider-specific adapters. The rest of the pipeline depends only on these
// interfaces, never on provider response formats.
package model

import (
	"context"

	"github.com/refraict/refraict/internal/ir"
)

// VisionRequest is the input to a vision crop analysis.
type VisionRequest struct {
	// ImageData is the (resized) crop image bytes.
	ImageData []byte
	ImageMIME string
	// CropID identifies the crop being analyzed.
	CropID string
	// BBoxGlobal is the crop's bounding box in the original screenshot.
	BBoxGlobal ir.BoundingBox
	// OCRContext optionally provides OCR tokens scoped to this crop.
	OCRContext []ir.OCRToken
	// PromptVersion is the versioned analysis instructions.
	PromptVersion string
	// SchemaVersion is the output schema version.
	SchemaVersion string
	// Prompt is the fully-built analysis instructions (optional; if empty the
	// backend applies its own default). Kept provider-agnostic.
	Prompt string
}

// VisionResult is the structured + descriptive output of one crop analysis.
type VisionResult struct {
	CropID       string          `json:"crop_id"`
	BBoxGlobal   ir.BoundingBox  `json:"bbox_global"`
	RoleGuess    string          `json:"role_guess"`
	Layout       *Layout         `json:"layout,omitempty"`
	Components   []VisionCompRef `json:"components"`
	Description  string          `json:"description"`
	Confidence   float64         `json:"confidence"`
	RawOutput    string          `json:"raw_output,omitempty"`
	SchemaFailed bool            `json:"schema_failed,omitempty"`
}

// Layout describes the coarse layout of a crop.
type Layout struct {
	Type    string `json:"type"`
	Columns int    `json:"columns,omitempty"`
	GapPx   int    `json:"gap_px_approx,omitempty"`
}

// VisionCompRef is a component reference reported by a crop analysis.
type VisionCompRef struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	BBoxGlobal ir.BoundingBox `json:"bbox_global"`
	Confidence float64        `json:"confidence"`
	Text       string         `json:"text,omitempty"`
	Role       string         `json:"role,omitempty"`
}

// VisionBackend analyses a single crop image.
type VisionBackend interface {
	Analyze(ctx context.Context, req VisionRequest) (*VisionResult, error)
}

// TextRequest is input to a text-only model call.
type TextRequest struct {
	Prompt        string
	PromptVersion string
	// MaxTokens caps the generated token count (Ollama num_predict). 0 uses the
	// backend default. Bounding this prevents a small text model from running
	// away into a degenerate long/looping generation (see graph/summary calls),
	// which otherwise blocks the per-image pipeline until the transport timeout.
	MaxTokens int
}

// TextResult is a completion plus optional structured payload.
type TextResult struct {
	Output     string
	Confidence float64
	RawOutput  string
}

// TextBackend performs text-only completions.
type TextBackend interface {
	Complete(ctx context.Context, req TextRequest) (*TextResult, error)
}
