// Package summarize produces the multi-level textual summaries (L0 raw crop
// description, L1 region summary, L2 page summary).
package summarize

import (
	"context"

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
	res, err := s.Backend.Complete(ctx, model.TextRequest{Prompt: p, PromptVersion: prompt.RegionSummaryV1})
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
	res, err := s.Backend.Complete(ctx, model.TextRequest{Prompt: p, PromptVersion: prompt.PageSummaryV1})
	if err != nil {
		return fallbackPage(regionSummaries, pageType), err
	}
	if res == nil || res.Output == "" {
		return fallbackPage(regionSummaries, pageType), nil
	}
	return res.Output, nil
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
