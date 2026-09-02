// Package prompt holds versioned prompt templates. Prompts are treated as
// software artifacts: changing a version bumps the cache key and makes
// evaluation reproducible.
package prompt

import (
	"bytes"
	"fmt"

	"github.com/refraict/refraict/internal/ir"
)

// Prompt versions. Bump when instructions/schema change.
const (
	CropAnalysisV1  = "crop-analysis-v1"
	RegionSummaryV1 = "region-summary-v1"
	PageSummaryV1   = "page-summary-v2"
	UIGraphV1       = "ui-graph-v1"
)

// SchemaVersion identifies the canonical UI IR schema.
const SchemaVersion = "ui-ir-v1"

// BuildCropPrompt constructs a GROUNDED per-crop analysis prompt. Instead of
// demanding a strict JSON schema with bounding boxes (which small local VLMs
// cannot reliably produce), it gives the model the crop image plus the
// deterministic facts already measured for that crop — the OCR text actually
// present and the measured colors — and asks for a short, grounded Markdown
// description. The model's job is interpretation constrained to the evidence,
// not geometry or invention. This is what keeps a 3B-class model from
// free-associating a generic web page.
func BuildCropPrompt(bbox ir.BoundingBox, ocrCtx []ir.OCRToken) string {
	return BuildGroundedCropPrompt(bbox, ocrCtx, nil)
}

// BuildGroundedCropPrompt builds the grounded per-crop prompt with optional
// measured color facts. ocrCtx is the OCR text within this crop; colors are the
// measured colors within this crop. Both are the deterministic ground truth the
// model must stay within.
func BuildGroundedCropPrompt(bbox ir.BoundingBox, ocrCtx []ir.OCRToken, colors []ir.ColorFact) string {
	return BuildGroundedCropPromptTyped(bbox, ocrCtx, colors, "")
}

// BuildGroundedCropPromptTyped is BuildGroundedCropPrompt with an optional
// page-type hint. The hint frames the crop within the kind of page it belongs
// to so the model does not over-interpret quoted content (e.g. describing a
// task-detail crop that quotes "login form" as if the crop were a login page).
func BuildGroundedCropPromptTyped(bbox ir.BoundingBox, ocrCtx []ir.OCRToken, colors []ir.ColorFact, pageType string) string {
	var b bytes.Buffer
	b.WriteString(`You are Refraict, a UI screenshot analyzer. You are shown ONE crop of a larger interface, plus the deterministic facts already measured for this exact crop (the text detected by OCR and the colors measured from the pixels).

Write a SHORT Markdown description of THIS crop for another AI system that cannot see the image. Follow these rules strictly:

- Describe ONLY what is supported by the image together with the measured facts below.
- Use ONLY the colors listed in MEASURED COLORS. Do not name any other color.
- Refer ONLY to text that appears in DETECTED TEXT. Do not invent labels, headings, articles, footers, menu items, or button names that are not listed.
- Text may quote feature names or task titles (e.g. "Implement login screen"); report them as the text present, but do NOT infer the crop IS that thing (e.g. do not call this a login form just because the word "login" appears).
- Do NOT describe behavior you cannot see (hover effects, animations, page transitions, what happens on click).
- If the crop is ambiguous, say so briefly rather than guessing.
- Keep it to a few sentences. No JSON, no coordinates.

`)
	if pt := pageType; pt != "" && pt != "generic" {
		b.WriteString(fmt.Sprintf("Context: this crop is part of a %q screen.\n", pt))
	}
	b.WriteString(fmt.Sprintf("This crop covers global pixel box %s of the full screenshot.\n", fbox(bbox)))

	b.WriteString("\nDETECTED TEXT (OCR, verbatim — the only text you may reference):\n")
	if len(ocrCtx) == 0 {
		b.WriteString("(none detected in this crop)\n")
	} else {
		for _, t := range ocrCtx {
			if t.Text == "" {
				continue
			}
			b.WriteString("- \"" + t.Text + "\"\n")
		}
	}

	b.WriteString("\nMEASURED COLORS (hex — the only colors you may name):\n")
	if len(colors) == 0 {
		b.WriteString("(no colors measured for this crop)\n")
	} else {
		seen := map[string]bool{}
		for _, c := range colors {
			if seen[c.Value] {
				continue
			}
			seen[c.Value] = true
			b.WriteString("- " + c.Value + "\n")
		}
	}
	b.WriteString("\nNow write the grounded Markdown description:\n")
	return b.String()
}

// BuildElementLabelPrompt asks the VLM to name a single cropped graphic UI
// element (icon, logo, chart, or image) in a few words. The crop is tightly
// bounded to one element, so the model is constrained to describe only what is
// visible. It must not invent surrounding context or read data values.
func BuildElementLabelPrompt(elementType string) string {
	return `You are shown a single cropped ` + elementType + ` from a user interface.
In a few words, name what this element is or represents (e.g. "search icon",
"settings gear", "brand logo", "bar chart"). 

Rules:
- Describe ONLY this cropped element, not any surrounding UI.
- If it is an icon, name the glyph/function (e.g. "magnifier / search").
- If it is a chart, name the chart type only (e.g. "bar chart"); do NOT read or
  invent specific data values or axis numbers.
- Do not invent text. Reply with a short phrase, no punctuation-heavy sentences.`
}

// BuildRegionSummaryPrompt condenses raw crop observations into a region summary.
func BuildRegionSummaryPrompt(crops []string) string {
	p := `You are Refraict summarizing a UI region from its grounded crop descriptions. Produce a concise Markdown summary.

Rules (strict):
- Use ONLY information stated in the crop descriptions below.
- Do NOT invent text, labels, colors, components, or behavior not present in them.
- If the descriptions are sparse, keep the summary short and note what is uncertain.

CROP DESCRIPTIONS (the only source you may use):
`
	for _, c := range crops {
		p += "\n--- crop ---\n" + c + "\n"
	}
	return p
}

// BuildPageSummaryPrompt condenses region summaries into a page summary.
func BuildPageSummaryPrompt(regions []string, pageType string) string {
	pt := pageType
	if pt == "" {
		pt = "generic"
	}
	p := `You are Refraict writing a page-level Markdown summary of a UI screenshot for another AI system. You are given per-region descriptions that were themselves grounded in measured facts.

This page has been classified as a "` + pt + `" screen (from deterministic structural signals). Treat that classification as authoritative for describing WHAT KIND OF PAGE this is.

Rules (strict):
- Describe the page AS A "` + pt + `" screen. The region text may quote feature names, task titles, branch names, or descriptions (for example a task card titled "Implement login screen" or body text mentioning "login form" / "email/password"). Those are the CONTENT the page is ABOUT — they do NOT change what the page IS. Do NOT reclassify the page as a login page (or any other type) based on quoted content. Distinguish "a page ABOUT X" from "a page that IS X".
- Summarize ONLY what the region descriptions below state. Compress and organize; do not add new facts.
- Do NOT invent sections, articles, footers, menu items, CTAs, or component names not present in the region descriptions.
- Do NOT name colors that are not mentioned in the region descriptions.
- Do NOT describe behavior (hover, animation, transitions, click results) — a screenshot cannot show it.
- If the regions are sparse or unclear, produce a short summary and say what is uncertain rather than filling gaps.

REGION DESCRIPTIONS (the only source you may use):
`
	if len(regions) == 0 {
		p += "\n(no region descriptions were produced; state that the page could not be described from available evidence)\n"
	}
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
