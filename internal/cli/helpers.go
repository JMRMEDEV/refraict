package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/crop"
	"github.com/refraict/refraict/internal/graph"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/ocr"
	"github.com/refraict/refraict/internal/summarize"
)

// applyOverrides overlays CLI flags onto the loaded config.
func applyOverrides(cfg *config.Config, o *analysisOptions) {
	if o.visionModel != "" {
		cfg.Vision.Model = o.visionModel
	}
	if o.summaryModel != "" {
		cfg.Summary.Model = o.summaryModel
	}
	if o.aggregatorModel != "" {
		cfg.Aggregator.Model = o.aggregatorModel
	}
	if o.visionProvider != "" {
		cfg.Vision.Provider = o.visionProvider
	}
	if o.summaryProvider != "" {
		cfg.Summary.Provider = o.summaryProvider
	}
	if o.aggregatorProvider != "" {
		cfg.Aggregator.Provider = o.aggregatorProvider
	}
	if o.cropSize > 0 {
		cfg.Image.CropLongSide = o.cropSize
	}
	if o.cropOverlap > 0 {
		cfg.Image.CropOverlap = o.cropOverlap
	}
	if o.minTextHeight > 0 {
		cfg.Image.MinimumTextHeightAfter = o.minTextHeight
	}
	if o.batchSize > 0 {
		cfg.Vision.BatchSize = o.batchSize
	}
	if o.workers > 0 {
		cfg.Vision.Workers = o.workers
	}
	if o.cloudFallback {
		cfg.Cloud.Enabled = true
		cfg.Cloud.AllowCloud = true
		cfg.Cloud.LocalOnly = false
	}
}

// buildOCREngine returns the configured OCR backend, or nil if none configured.
//
// OCR is optional and degrades gracefully: if the REFRAICT_OCR_CMD environment
// variable is not set, no OCR engine is available and the pipeline continues
// purely with vision-model analysis. REFRAICT_OCR_ARGS (space-separated, shell
// tokenized) may be used to pass extra fixed arguments to the OCR command.
func buildOCREngine() (ocr.Engine, error) {
	cmd := os.Getenv("REFRAICT_OCR_CMD")
	if cmd == "" {
		return nil, ocr.ErrUnavailable
	}
	var args []string
	if raw := os.Getenv("REFRAICT_OCR_ARGS"); strings.TrimSpace(raw) != "" {
		args = strings.Fields(raw)
	}
	return &ocr.ExternalEngine{Command: cmd, Args: args}, nil
}

// buildVisionBackend constructs the vision adapter for the configured provider.
func buildVisionBackend(cfg *config.Config, o *analysisOptions) (model.VisionBackend, error) {
	provider := cfg.Vision.Provider
	if provider == "" || provider == "ollama" {
		return model.NewOllama(cfg.Vision.Endpoint, cfg.Vision.Model), nil
	}
	return nil, fmt.Errorf("unsupported vision provider %q", provider)
}

// buildTextBackend constructs the text adapter.
func buildTextBackend(cfg *config.Config, o *analysisOptions) model.TextBackend {
	if cfg.Summary.Provider == "" || cfg.Summary.Provider == "ollama" {
		return model.NewOllama(cfg.Summary.Endpoint, cfg.Summary.Model)
	}
	return nil
}

// cropBytes renders a crop PNG for the given region. The crop is extracted
// from the source image at global coordinates and scaled so its longest side
// is capped (matching the vision model's preferred input size).
func cropBytes(img *imageproc.Image, cp crop.Crop) []byte {
	b := cp.BBox
	if b.Empty() {
		return imgResizedPNG(img)
	}
	sub := img.CropRegion(b.X0, b.Y0, b.X1, b.Y1, maxCropPx)
	if sub == nil {
		return imgResizedPNG(img)
	}
	data, err := imageproc.EncodePNG(sub)
	if err != nil {
		return nil
	}
	return data
}

// maxCropPx caps the longest side of a crop passed to the vision model.
const maxCropPx = 1024

// imgResizedPNG encodes a downscaled copy of the whole image as PNG bytes,
// used as a stand-in for a crop (the crop region is centered/resized).
func imgResizedPNG(img *imageproc.Image) []byte {
	resized := img.Resize(maxCropPx)
	data, err := imageproc.EncodePNG(resized)
	if err != nil {
		return nil
	}
	return data
}

// tokensIn returns OCR tokens intersecting the given box.
func tokensIn(b ir.BoundingBox, toks []ir.ORCToken) []ir.ORCToken {
	var out []ir.ORCToken
	for _, t := range toks {
		if t.BBoxGlobal.Overlaps(b) {
			out = append(out, t)
		}
	}
	return out
}

// sampleColors measures interior colors for each component region.
func sampleColors(img *imageproc.Image, comps []ir.Component) []ir.ColorFact {
	var facts []ir.ColorFact
	for i, c := range comps {
		b := c.BBox
		if b.Empty() {
			continue
		}
		hex, r, g, bl, ok := imageproc.SampleRegion(img.AsImage(), b.X0, b.Y0, b.X1, b.Y1, 0.15)
		if !ok {
			continue
		}
		facts = append(facts, ir.ColorFact{
			Name:       "background",
			Value:      hex,
			RGB:        [3]int{r, g, bl},
			BBoxGlobal: b,
			Source:     "pixel_sampler",
			Confidence: 0.997,
		})
		_ = i
	}
	return facts
}


// inferPageType does a lightweight page-type classification from geometry/text.
func inferPageType(comps []ir.Component, toks []ir.ORCToken) string {
	text := ""
	for _, t := range toks {
		text += " " + t.Text
	}
	lt := strings.ToLower(text)
	types := map[string][]string{
		"login":         {"sign in", "signin", "login", "password", "email", "username"},
		"pricing":       {"pricing", "plan", "per month", "billing"},
		"dashboard":     {"dashboard", "metrics", "analytics", "revenue", "kpi"},
		"ecommerce":     {"cart", "add to cart", "checkout", "shop", "products"},
		"settings":      {"settings", "preferences", "profile", "account"},
	}
	score := map[string]int{}
	for t, kw := range types {
		for _, k := range kw {
			if strings.Contains(lt, k) {
				score[t]++
			}
		}
	}
	best, bs := "generic", 0
	for t, s := range score {
		if s > bs {
			best, bs = t, s
		}
	}
	return best
}

// probableDOM produces a simplified inferred DOM tree from the component graph.
func probableDOM(comps []ir.Component) string {
	var b strings.Builder
	b.WriteString("<!-- This DOM is INFERRED from screenshot analysis, not observed HTML. -->\n")
	graph.SortByPosition(comps)
	b.WriteString("<div class=\"page\">\n")
	writeComponents(&b, comps)
	b.WriteString("</div>\n")
	return b.String()
}

func writeComponents(b *strings.Builder, comps []ir.Component) {
	// Group by y-band for ordering.
	sorted := append([]ir.Component(nil), comps...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].BBox.Y0 != sorted[j].BBox.Y0 {
			return sorted[i].BBox.Y0 < sorted[j].BBox.Y0
		}
		return sorted[i].BBox.X0 < sorted[j].BBox.X0
	})
	tag := func(t string) string {
		switch t {
		case "button", "button_primary":
			return "button"
		case "input", "text_field":
			return "input"
		case "navigation":
			return "nav"
		case "card":
			return "section"
		case "label":
			return "label"
		case "image":
			return "img"
		default:
			return "div"
		}
	}
	for _, c := range sorted {
		t := tag(c.Type.Value)
		fmt.Fprintf(b, "  <%s class=\"%s\" data-conf=\"%0.2f\">\n", t, c.Type.Value, c.Confidence)
		if c.Text != nil {
			fmt.Fprintf(b, "    %s\n", c.Text.Value)
		}
		fmt.Fprintf(b, "  </%s>\n", t)
	}
}

// crossRegionSummary condenses a single crop's raw description into a region
// summary. It acts as a thin adapter so region files carry the same
// "REGION ROLE / CONTENT" structure regardless of which condition produced
// them.
func crossRegionSummary(s *summarize.Summarizer, ctx context.Context, cr *model.VisionResult) string {
	out, _ := s.RegionSummary(ctx, []string{cr.Description})
	return out
}
