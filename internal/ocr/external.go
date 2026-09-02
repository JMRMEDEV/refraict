package ocr

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/refraict/refraict/internal/ir"
)

// ExternalEngine invokes an external OCR CLI (e.g. RapidOCR/PaddleOCR wrapper)
// that accepts an image path and emits a JSON array of tokens:
//
//	[{"text":"...","bbox":[x0,y0,x1,y1],"confidence":0.99}]
//
// If no binary is available the engine returns the sentinel ErrUnavailable so
// callers can continue with VLM-only analysis.
type ExternalEngine struct {
	// Command is the executable name/path.
	Command string
	// Args are appended after the image path.
	Args []string
}

// ErrUnavailable indicates no OCR engine is installed/configured.
var ErrUnavailable = fmt.Errorf("ocr engine unavailable")

// Recognize runs the external OCR command and parses tokens.
func (e *ExternalEngine) Recognize(ctx context.Context, in Input) ([]ir.OCRToken, error) {
	if e.Command == "" {
		return nil, ErrUnavailable
	}
	path := in.ImagePath
	if path == "" {
		// Write data to temp file.
		return nil, fmt.Errorf("external ocr requires an image file path")
	}
	args := append(append([]string{}, e.Args...), path)
	cmd := exec.CommandContext(ctx, e.Command, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, ErrUnavailable
	}
	var raw []struct {
		Text       string  `json:"text"`
		BBox       [4]int  `json:"bbox"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse ocr output: %w", err)
	}
	toks := make([]ir.OCRToken, 0, len(raw))
	for _, r := range raw {
		toks = append(toks, ir.OCRToken{
			Text: r.Text,
			BBoxGlobal: ir.BoundingBox{X0: r.BBox[0], Y0: r.BBox[1], X1: r.BBox[2], Y1: r.BBox[3]},
			Confidence: r.Confidence,
			Source:     "external_ocr",
		})
	}
	return toks, nil
}

var _ Engine = (*ExternalEngine)(nil)

// NoopEngine returns no tokens and no error; used when OCR is disabled.
type NoopEngine struct{}

// Recognize returns nothing.
func (NoopEngine) Recognize(_ context.Context, _ Input) ([]ir.OCRToken, error) {
	return nil, nil
}

var _ Engine = NoopEngine{}
