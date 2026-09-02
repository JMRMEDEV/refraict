package cli

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"sort"
	"strings"

	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/crop"
	"github.com/refraict/refraict/internal/graph"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/iconlabel"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/ocr"
	"github.com/refraict/refraict/internal/prompt"
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
		return model.NewOllamaKeepAlive(cfg.Vision.Endpoint, cfg.Vision.Model, resolveKeepAlive(cfg, o)), nil
	}
	return nil, fmt.Errorf("unsupported vision provider %q", provider)
}

// buildVisionBackendKeepAlive builds the vision adapter with an explicit
// keep-alive override (empty = config/default). Used by commands without an
// analysisOptions.
func buildVisionBackendKeepAlive(cfg *config.Config, keepWarm string) (model.VisionBackend, error) {
	provider := cfg.Vision.Provider
	if provider == "" || provider == "ollama" {
		ka := keepWarm
		if ka == "" {
			ka = cfg.Models.KeepAlive
		}
		return model.NewOllamaKeepAlive(cfg.Vision.Endpoint, cfg.Vision.Model, ka), nil
	}
	return nil, fmt.Errorf("unsupported vision provider %q", provider)
}

// voteRawLabels samples the vision backend `runs` times on the given crop image
// with the element-label prompt, returning the raw description strings for
// canonicalization + voting. Shared by the analyze pipeline and the `icons`
// command.
func voteRawLabels(ctx context.Context, vision model.VisionBackend, cropData []byte, c ir.Component, runs int) []string {
	if vision == nil || len(cropData) == 0 || runs <= 0 {
		return nil
	}
	raw := make([]string, 0, runs)
	for r := 0; r < runs; r++ {
		res, err := vision.Analyze(ctx, model.VisionRequest{
			ImageData:     cropData,
			ImageMIME:     "image/png",
			CropID:        c.ID,
			BBoxGlobal:    c.BBox,
			PromptVersion: prompt.CropAnalysisV1,
			SchemaVersion: prompt.SchemaVersion,
			Prompt:        prompt.BuildElementLabelPrompt(c.Type.Value),
		})
		if err == nil && res != nil {
			raw = append(raw, res.Description)
		}
	}
	return raw
}

// buildTextBackend constructs the text adapter for the summary backend.
func buildTextBackend(cfg *config.Config, o *analysisOptions) model.TextBackend {
	return buildBackendFor(cfg.Summary.Provider, cfg.Summary.Endpoint, cfg.Summary.Model, resolveKeepAlive(cfg, o))
}

// buildAggregatorBackend constructs the text adapter used for the aggregator /
// escalation stage (M4). Aggregation runs over the already-produced region
// summaries with an (optionally stronger) model to synthesize the final page
// narrative and resolve low-confidence conditions.
func buildAggregatorBackend(cfg *config.Config) model.TextBackend {
	return buildBackendFor(cfg.Aggregator.Provider, cfg.Aggregator.Endpoint, cfg.Aggregator.Model, cfg.Models.KeepAlive)
}

// buildBackendFor resolves a provider string to a TextBackend, defaulting to
// the local Ollama adapter when unset.
func buildBackendFor(provider, endpoint, mdl, keepAlive string) model.TextBackend {
	if provider == "" || provider == "ollama" {
		return model.NewOllamaKeepAlive(endpoint, mdl, keepAlive)
	}
	return nil
}

// resolveKeepAlive returns the effective Ollama keep_alive: the --keep-warm
// flag override when set, otherwise the configured Models.KeepAlive (default
// "0" = free immediately).
func resolveKeepAlive(cfg *config.Config, o *analysisOptions) string {
	if o != nil && o.keepWarm != "" {
		return o.keepWarm
	}
	if cfg.Models.KeepAlive != "" {
		return cfg.Models.KeepAlive
	}
	return "0"
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
func tokensIn(b ir.BoundingBox, toks []ir.OCRToken) []ir.OCRToken {	var out []ir.OCRToken
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

// cropDominantColors measures the most frequent colors within a crop box by
// sampling a coarse grid of interior points. Returns up to ~6 distinct colors,
// most frequent first. Used to ground the per-crop VLM prompt so the model may
// only name colors that were actually measured.
func cropDominantColors(img *imageproc.Image, b ir.BoundingBox) []ir.ColorFact {
	if b.Empty() {
		return nil
	}
	const grid = 12
	counts := map[string][3]int{}
	freq := map[string]int{}
	w := b.Width()
	h := b.Height()
	stepX := w / grid
	stepY := h / grid
	if stepX < 1 {
		stepX = 1
	}
	if stepY < 1 {
		stepY = 1
	}
	for y := b.Y0; y < b.Y1; y += stepY {
		for x := b.X0; x < b.X1; x += stepX {
			c := img.At(x, y)
			if c.A < 128 {
				continue
			}
			hex := fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
			counts[hex] = [3]int{int(c.R), int(c.G), int(c.B)}
			freq[hex]++
		}
	}
	type kv struct {
		hex string
		n   int
	}
	arr := make([]kv, 0, len(freq))
	for k, n := range freq {
		arr = append(arr, kv{k, n})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].n > arr[j].n })
	var out []ir.ColorFact
	for i, e := range arr {
		if i >= 6 {
			break
		}
		rgb := counts[e.hex]
		out = append(out, ir.ColorFact{
			Name:       "measured",
			Value:      e.hex,
			RGB:        rgb,
			BBoxGlobal: b,
			Source:     "pixel_sampler",
			Confidence: 0.99,
		})
	}
	return out
}

// isGraphicType reports whether a component type is a graphic/structural element
// worth a Tier-2 VLM label (icon, logo, chart, image) — as opposed to text or
// generic containers/cards which are already described by OCR + crop summaries.
func isGraphicType(t string) bool {
	switch t {
	case "icon", "logo", "chart", "image":
		return true
	default:
		return false
	}
}

// labelGraphicElements attaches a short, voted, grounded label to each graphic
// component's Semantic field (marked inference). For each element it crops a
// padded region, queries the vision backend `runs` times, canonicalizes each
// output via the Lucide TF-IDF alias map, and votes. The label is only set when
// the vote agreement ratio meets `threshold` — otherwise the element is left
// typed but unlabeled (honest "detected, not confidently identified"). Bounded
// by max elements to keep model calls in check. A nil backend, nil canon,
// max<=0, or runs<=0 is a no-op.
func labelGraphicElements(ctx context.Context, vision model.VisionBackend, canon *iconlabel.Canonicalizer, img *imageproc.Image, comps []ir.Component, max, runs int, threshold, padFrac float64, provider, mdl string) int {
	if vision == nil || canon == nil || max <= 0 || runs <= 0 {
		return 0
	}
	labeled := 0
	for i := range comps {
		if labeled >= max {
			break
		}
		c := &comps[i]
		if !isGraphicType(c.Type.Value) {
			continue
		}
		data := elementCropBytes(img, c.BBox, padFrac)
		if len(data) == 0 {
			continue
		}
		raw := voteRawLabels(ctx, vision, data, *c, runs)
		vote := canon.Vote(raw)
		// Only accept a label with sufficient self-consistency. Low agreement
		// means the model has no stable answer for this element — withhold.
		if vote.Concept == "" || vote.Ratio < threshold {
			continue
		}
		c.Semantic = &ir.ConstString{Value: vote.Concept, Source: "vlm_element_vote", Confidence: vote.Ratio}
		c.Provenance = &ir.RunProvenance{Model: mdl, Provider: provider, PromptVersion: prompt.CropAnalysisV1, SchemaVersion: prompt.SchemaVersion}
		labeled++
	}
	return labeled
}


// paddedBBox expands a bbox by frac of its size on each side (clamped to image
// bounds), giving a small VLM more visual context around a tiny element.
func paddedBBox(b ir.BoundingBox, img *imageproc.Image, frac float64) ir.BoundingBox {
	w, h := img.Bounds()
	padX := int(float64(b.Width()) * frac)
	padY := int(float64(b.Height()) * frac)
	x0 := b.X0 - padX
	y0 := b.Y0 - padY
	x1 := b.X1 + padX
	y1 := b.Y1 + padY
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	return ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1}
}

// elementPadFrac returns the configured element-crop padding fraction, or a
// sensible default when unset.
func elementPadFrac(cfg *config.Config) float64 {
	if cfg.Analysis.ElementLabelPadFrac > 0 {
		return cfg.Analysis.ElementLabelPadFrac
	}
	return 0.15
}

// elementCropBytes renders a graphic element crop for the VLM: the region is
// padded for context, then UPSCALED so the element fills a fixed square canvas
// (via imageproc.ElementCropPNG). Filling the frame is what matters — an
// earlier version left tiny icons as a ~5%-of-canvas speck (CropRegion only
// downscaled), which the `refraict icons --dump-crops` inspection revealed. The
// fix (upscale small crops to the inner margin) lifted measured vote agreement
// materially, e.g. "search" 3/10 -> 7/10 and "x" 5/10 -> 8/10. The canvas
// background is the region's dominant color so the padding blends.
func elementCropBytes(img *imageproc.Image, b ir.BoundingBox, padFrac float64) []byte {
	if b.Empty() {
		return nil
	}
	const canvas = 512
	const inner = 448 // element occupies most of the canvas, small margin
	pb := paddedBBox(b, img, padFrac)
	bg := color.RGBA{0, 0, 0, 255}
	if cols := cropDominantColors(img, pb); len(cols) > 0 {
		bg = color.RGBA{uint8(cols[0].RGB[0]), uint8(cols[0].RGB[1]), uint8(cols[0].RGB[2]), 255}
	}
	return img.ElementCropPNG(pb.X0, pb.Y0, pb.X1, pb.Y1, canvas, inner, bg)
}

// inferPageType does a lightweight page-type classification from geometry/text.
func inferPageType(comps []ir.Component, toks []ir.OCRToken) string {
	text := ""
	for _, t := range toks {
		text += " " + t.Text
	}
	lt := strings.ToLower(text)
	types := map[string][]string{
		"login":     {"sign in", "signin", "login", "log in", "password", "username"},
		"pricing":   {"pricing", "per month", "per year", "/mo", "subscribe", "free trial"},
		"billing":   {"billing", "invoice", "payment", "top up", "balance", "total cost", "usd", "$"},
		"usage":     {"usage", "api requests", "tokens", "requests", "quota", "consumption", "rate limit"},
		"analytics": {"analytics", "metrics", "dashboard", "chart", "cost(usd)", "last 30 days", "trend"},
		"api":       {"api key", "api keys", "endpoint", "secret key", "access token", "docs"},
		"dashboard": {"dashboard", "overview", "kpi", "revenue", "summary"},
		"ecommerce": {"cart", "add to cart", "checkout", "shop", "products"},
		"settings":  {"settings", "preferences", "profile", "account"},
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

// inferPageGraph builds the semantic UI graph. It always starts from the
// deterministic geometric relationships, then augments them with model
// inferred relationships (tagged Source "model") when a text backend is
// available. If the backend is unavailable or returns nothing parseable, the
// geometric-only graph is returned intact (QA finding G7).
func inferPageGraph(ctx context.Context, comps []ir.Component, backend model.TextBackend) *graph.Graph {
	g := graph.Build(comps)
	if backend == nil || len(comps) == 0 {
		return g
	}
	seen := map[[2]string]bool{}
	for _, r := range g.Relationships {
		seen[[2]string{r.A, r.Relation + ">" + r.B}] = true
	}
	res, err := backend.Complete(ctx, model.TextRequest{
		Prompt:        prompt.BuildGraphPrompt(comps, g.Relationships),
		PromptVersion: prompt.UIGraphV1,
	})
	if err != nil || res == nil || res.Output == "" {
		return g
	}
	ids := map[string]bool{}
	for _, c := range comps {
		ids[c.ID] = true
	}
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		a, relation, b := parts[0], parts[1], parts[2]
		if !ids[a] || !ids[b] {
			continue
		}
		// Reject relationships already captured by geometric inference with an
		// identical (a, relation, b) triple.
		found := false
		for _, r := range g.Relationships {
			if r.A == a && r.Relation == relation && r.B == b {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if seen[[2]string{a, relation + ">" + b}] {
			continue
		}
		seen[[2]string{a, relation + ">" + b}] = true
		g.Relationships = append(g.Relationships, ir.Relationship{
			A: a, Relation: relation, B: b,
			Confidence: 0.6,
			Source:     "model",
		})
	}
	return g
}

// pageConfidence computes a coarse page-level confidence as the mean
// component confidence (0.5 when no components were measured).
func pageConfidence(comps []ir.Component) float64 {
	if len(comps) == 0 {
		return 0.5
	}
	var sum float64
	for _, c := range comps {
		sum += c.Confidence
	}
	return sum / float64(len(comps))
}

// unresolvedComponents counts components whose confidence sits below the
// configured threshold and are therefore candidates for re-resolution.
func unresolvedComponents(comps []ir.Component, threshold float64) int {
	n := 0
	for _, c := range comps {
		if c.Confidence < threshold {
			n++
		}
	}
	return n
}

// cropDisagreementRate is the fraction of analyzed crops whose box geometry
// needed repair or was dropped, a proxy for crop disagreement / low quality.
func cropDisagreementRate(boxFailures, boxRepairs, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(boxFailures) / float64(total)
}

// redactAll replaces visible text spans in a set of Markdown summaries with a
// placeholder so potentially sensitive content is not sent to a cloud backend.
func redactAll(texts []string) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = redactText(t)
	}
	return out
}

// redactText masks runs of displayable text while retaining structural
// punctuation so the summary shape survives redaction for layout reasoning.
func redactText(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune('█')
		case r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '.' || r == '!' || r == '?' || r == '-' || r == '(' || r == ')' || r == '[' || r == ']' || r == ':' || r == ';' || r == '/' || r == '@' || r == '#' || r == '_':
			b.WriteRune(r)
		default:
			// Other unicode letters/symbols are redacted too.
			b.WriteRune('█')
		}
	}
	return b.String()
}
