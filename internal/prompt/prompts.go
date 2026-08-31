// Package prompt holds versioned prompt templates. Prompts are treated as
// software artifacts: changing a version bumps the cache key and makes
// evaluation reproducible.
package prompt

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/refraict/refraict/internal/ir"
)

// Prompt versions. Bump when instructions/schema change.
const (
	CropAnalysisV1 = "crop-analysis-v1"
	RegionSummaryV1 = "region-summary-v1"
	PageSummaryV1   = "page-summary-v1"
	UIGraphV1       = "ui-graph-v1"
)

// SchemaVersion identifies the canonical UI IR schema.
const SchemaVersion = "ui-ir-v1"

// BuildCropPrompt constructs the analysis instructions + OCR context for a crop.
// bbox is the crop's global box; ocrCtx are OCR tokens scoped to the crop.
func BuildCropPrompt(bbox ir.BoundingBox, ocrCtx []ir.ORCToken) string {
	prompt := `You are Refraict, a UI screenshot analyzer. Analyze the provided web/app interface crop image and return ONLY valid JSON matching this schema:

{
  "role_guess": "semantic role of this crop's main content, e.g. header|sidebar|hero|content_grid|chart_area|table|footer|kpi_section|form|navigation",
  "layout": {"type": "grid|stack|row|column|freeform", "columns": 0, "gap_px_approx": 0},
  "components": [
    {
      "id": "unique-id",
      "type": "card|button|input|label|navigation|tab|icon|chart|table|link|image|list-item|breadcrumb|badge|...",
      "bbox_global": [x0, y0, x1, y1],
      "confidence": 0.0-1.0,
      "text": "visible text if any",
      "role": "semantic role if inferable"
    }
  ],
  "description": "A concise natural-language interpretation of THIS crop region, explaining meaning beyond restating coordinates.",
  "confidence": 0.0-1.0
}

Rules:
- Coordinates must be in the ORIGINAL screenshot global pixel space. This crop is at global box ` + fmt.Sprintf("%v", bbox) + `.
- Do not invent text not visible. If unsure, omit text.
- Only report clearly visible components.
- Keep the description human-readable and useful for another AI model that cannot see the image.

`
	if len(ocrCtx) > 0 {
		var buf bytes.Buffer
		buf.WriteString("\nOCR text detected in this crop (text, global bbox, confidence):\n")
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		_ = enc.Encode(ocrCtx)
		prompt += buf.String()
	}
	return prompt
}

// BuildRegionSummaryPrompt condenses raw crop observations into a region summary.
func BuildRegionSummaryPrompt(crops []string) string {
	p := `You are Refraict summarizing a UI region from its crop analysis observations. Produce a concise Markdown summary with sections: REGION ROLE, STRUCTURE, CONTENT, VISUAL STYLE, SPATIAL RELATIONSHIPS, SEMANTIC INTERPRETATION, UNCERTAINTIES. Explain meaning, do not just restate fields.

CROP ANALYSES:
`
	for _, c := range crops {
		p += "\n--- crop ---\n" + c + "\n"
	}
	return p
}

// BuildPageSummaryPrompt condenses region summaries into a page summary.
func BuildPageSummaryPrompt(regions []string, pageType string) string {
	p := `You are Refraict writing a page-level summary of a UI screenshot for consumption by another AI system. Describe the page layout, main sections, repeated components, primary CTA, and visual style. Be concise but complete. Output Markdown.

Page type guess: ` + pageType + `

REGION SUMMARIES:
`
	for _, r := range regions {
		p += "\n--- region ---\n" + r + "\n"
	}
	return p
}

// BuildGraphPrompt constructs a prompt to infer a UI hierarchy graph from
// normalized components.
func BuildGraphPrompt(components []ir.Component, relationships []ir.Relationship) string {
	p := `You are Refraict inferring a probable semantic UI hierarchy/DOM structure from detected components. Produce an indented tree (Markdown nested list) of the major regions and component nesting. Mark relationships between sibling/parent nodes. Base inference ONLY on the components and their coordinates/relationships. Mark any uncertain grouping.

COMPONENTS:
`
	for _, c := range components {
		line := fmt.Sprintf("- [%s] %s %s", c.ID, c.Type.Value, fbox(c.BBox))
		if c.Text != nil {
			line += " text=" + c.Text.Value
		}
		p += line + "\n"
	}
	if len(relationships) > 0 {
		p += "\nRELATIONSHIPS:\n"
		for _, r := range relationships {
			p += fmt.Sprintf("- %s %s %s\n", r.A, r.Relation, r.B)
		}
	}
	return p
}

func fbox(b ir.BoundingBox) string {
	return fmt.Sprintf("[%d,%d,%d,%d]", b.X0, b.Y0, b.X1, b.Y1)
}
