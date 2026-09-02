// Package ocr provides a pluggable OCR backend abstraction. OCR runs
// independently of the vision model and produces text with global bounding
// boxes and confidence.
package ocr

import (
	"context"

	"github.com/refraict/refraict/internal/ir"
)

// Engine is the abstraction over an OCR backend.
type Engine interface {
	// Recognize returns OCR tokens in global image coordinates.
	Recognize(ctx context.Context, input Input) ([]ir.OCRToken, error)
}

// Input describes an image to be OCR'd.
type Input struct {
	// ImagePath is the path to a PNG/JPEG file.
	ImagePath string
	// Data is optional raw image bytes; if non-empty it is used instead.
	Data []byte
}

// Result is a convenience container.
type Result struct {
	Tokens []ir.OCRToken
}
