// Package summarize produces the multi-level textual summaries (L0 raw crop
// description, L1 region summary, L2 page summary).
package summarize

import (
	"context"
	"strings"

	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/prompt"
)

// Summarizer drives the text-model condensation pipeline.
type Summarizer struct {
	Backend model.TextBackend
}

// New creates a Summarizer.
func New(b model.TextBackend) *Summarizer { return &Summarizer{Backend: b} }

// RegionSummary condenses raw crop analysis descriptions into a region summary.
// If the backend is nil or fails, it concatenates crop descriptions as a
// degraded-but-useful fallback.
func (s *Summarizer) RegionSummary(ctx context.Context, cropDescriptions []string) (string, error) {
	if s == nil || s.Backend == nil {
		return fallbackJoin(cropDescriptions), nil
	}
	p := prompt.BuildRegionSummaryPrompt(cropDescriptions)
	res, err := s.Backend.Complete(ctx, model.TextRequest{Prompt: p, PromptVersion: prompt.RegionSummaryV1, MaxTokens: 512})
	if err != nil {
		return fallbackJoin(cropDescriptions), err
	}
	if res == nil || res.Output == "" {
		return fallbackJoin(cropDescriptions), nil
	}
	return res.Output, nil
}

// PageSummary condenses region summaries into a page-level Markdown summary.
func (s *Summarizer) PageSummary(ctx context.Context, regionSummaries []string, pageType string) (string, error) {
	if s == nil || s.Backend == nil {
		return fallbackPage(regionSummaries, pageType), nil
	}
	p := prompt.BuildPageSummaryPrompt(regionSummaries, pageType)
	res, err := s.Backend.Complete(ctx, model.TextRequest{Prompt: p, PromptVersion: prompt.PageSummaryV1, MaxTokens: 768})
	if err != nil {
		return fallbackPage(regionSummaries, pageType), err
	}
	if res == nil || res.Output == "" {
		return fallbackPage(regionSummaries, pageType), nil
	}
	return res.Output, nil
}

// Section is one region's gemma description plus its crop id, for deterministic
// page assembly (no text model).
type Section struct {
	ID          string
	Description string
}

// AssemblePage composes page.md deterministically from gemma's own descriptions:
// the whole-image overview description first (the "original summary"), then each
// focused section's description under a header. No text model is involved — this
// avoids the small text model's hallucination/latency and keeps gemma's grounded
// output verbatim. The text model's aggregation is only worth invoking when there
// is genuine cross-region synthesis to do; a straight concatenation does not need
// it. When overview is empty the assembly still lists the sections.
func AssemblePage(pageType, overview string, sections []Section) string {
	var b strings.Builder
	b.WriteString("# Page Summary\n\n")
	if pageType != "" && pageType != "generic" {
		b.WriteString("Page type: " + pageType + "\n\n")
	}
	if o := strings.TrimSpace(overview); o != "" {
		b.WriteString("## Overview (whole-image read)\n\n")
		b.WriteString(o + "\n\n")
	}
	if len(sections) > 0 {
		b.WriteString("## Sections\n")
		for _, s := range sections {
			if strings.TrimSpace(s.Description) == "" {
				continue
			}
			b.WriteString("\n### " + s.ID + "\n\n")
			b.WriteString(strings.TrimSpace(s.Description) + "\n")
		}
	}
	if strings.TrimSpace(overview) == "" && len(sections) == 0 {
		b.WriteString("_No descriptions were produced from available evidence._\n")
	}
	return b.String()
}

func fallbackJoin(ds []string) string {
	out := "REGION ROLE:\n(not summarized by model)\n\nCONTENT:\n"
	for _, d := range ds {
		out += "- " + d + "\n"
	}
	return out
}

func fallbackPage(regions []string, pageType string) string {
	out := "# Page Summary\n\n"
	if pageType != "" {
		out += "Page type guess: " + pageType + "\n\n"
	}
	out += "Detected regions:\n"
	for _, r := range regions {
		out += "- " + r + "\n"
	}
	return out
}
