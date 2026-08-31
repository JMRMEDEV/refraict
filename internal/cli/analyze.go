package cli

import (
	"image"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/refraict/refraict/internal/cache"
	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/crop"
	"github.com/refraict/refraict/internal/dub"
	"github.com/refraict/refraict/internal/graph"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/ocr"
	"github.com/refraict/refraict/internal/prompt"
	"github.com/refraict/refraict/internal/summarize"
	"github.com/refraict/refraict/internal/workdir"
	"github.com/spf13/cobra"
)

// analysisOptions captures command-line runtime flags.
type analysisOptions struct {
	visionModel    string
	summaryModel   string
	aggregatorModel string
	visionProvider string
	summaryProvider string
	aggregatorProvider string
	cropSize       int
	cropOverlap    float64
	minTextHeight  int
	batchSize      int
	workers        int
	noOCR          bool
	noSummary      bool
	noDOM          bool
	cloudFallback  bool
	output         string
	adaptive       bool
}

func newAnalyzeCmd() *cobra.Command {
	o := &analysisOptions{}
	cmd := &cobra.Command{
		Use:   "analyze <image>",
		Short: "Run the end-to-end analysis pipeline on a screenshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.output == "" {
				o.output = outputDir
			}
			if o.output == "" {
				base := strings.TrimSuffix(filepath.Base(args[0]), filepath.Ext(args[0]))
				o.output = "./analysis-" + base
			}
			return runAnalyze(cmd, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.visionModel, "vision-model", "", "vision model name")
	cmd.Flags().StringVar(&o.summaryModel, "summary-model", "", "summary (text) model name")
	cmd.Flags().StringVar(&o.aggregatorModel, "aggregator-model", "", "aggregator model name")
	cmd.Flags().StringVar(&o.visionProvider, "vision-provider", "", "vision provider (ollama)")
	cmd.Flags().StringVar(&o.summaryProvider, "summary-provider", "", "summary provider")
	cmd.Flags().StringVar(&o.aggregatorProvider, "aggregator-provider", "", "aggregator provider")
	cmd.Flags().IntVar(&o.cropSize, "crop-size", 0, "crop longest side (px)")
	cmd.Flags().Float64Var(&o.cropOverlap, "crop-overlap", 0, "crop overlap (0-1)")
	cmd.Flags().IntVar(&o.minTextHeight, "min-text-height", 0, "minimum text height after resize (px)")
	cmd.Flags().IntVar(&o.batchSize, "batch-size", 0, "inference batch size")
	cmd.Flags().IntVar(&o.workers, "workers", 0, "inference workers")
	cmd.Flags().BoolVar(&o.noOCR, "no-ocr", false, "skip OCR stage")
	cmd.Flags().BoolVar(&o.noSummary, "no-summary", false, "skip summaries")
	cmd.Flags().BoolVar(&o.noDOM, "no-dom", false, "skip DOM guess")
	cmd.Flags().BoolVar(&o.cloudFallback, "cloud-fallback", false, "allow cloud escalation")
	cmd.Flags().StringVarP(&o.output, "output", "o", "", "output directory")
	cmd.Flags().BoolVar(&o.adaptive, "adaptive", true, "use adaptive crop planning")
	return cmd
}

// runAnalyze executes the full pipeline.
func runAnalyze(cmd *cobra.Command, imagePath string, o *analysisOptions) error {
	ctx := cmd.Context()
	start := time.Now()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	cfg := config.Default()
	if configPath != "" {
		c, err := config.Load(configPath)
		if err != nil {
			return err
		}
		cfg = c
	}
	applyOverrides(cfg, o)

	// Image ingest (immutable original + hash).
	img, err := imageproc.Load(imagePath)
	if err != nil {
		return fail("image ingest: %w", err)
	}
	W, H := img.Bounds()
	stage("image", start)
	slog.Info("loaded image", "path", imagePath, "w", W, "h", H, "sha", shortHash(img.Sha256))

	ws, err := workdir.New(o.output)
	if err != nil {
		return err
	}

	// Cache. The cache lives at the configured database directory so it is
	// shared across analyses of the same image hash.
	cacheDB := resolveCacheDB(cfg.Cache.Database)
	c, err := cache.New(cacheDir(cacheDB), cfg.Cache.Enabled)
	if err != nil {
		return err
	}

	// manifest.
	manifest := map[string]any{
		"image":          imagePath,
		"image_sha256":   img.Sha256,
		"width":          W,
		"height":         H,
		"started_at":     time.Now(),
	}
	_ = ws.WriteJSON("manifest.json", manifest)

	// Overview image.
	overview := img.Resize(cfg.Image.OverviewWidth)
	if err := imageproc.WritePNG(ws.Path("overview.png"), overview); err != nil {
		return err
	}
	stage("overview", start)

	// OCR.
	var toks []ir.ORCToken
	if !o.noOCR && !cfg.Analysis.NoOCR {
		ocrKey := cache.Key(img.Sha256, "ocr-v1")
		ocrHit := false
		if c.Has(ocrKey) {
			if _, err := c.Get(ocrKey, &toks); err == nil {
				ocrHit = true
			}
		}
		if !ocrHit {
			eng, err := buildOCREngine()
			if err != nil || eng == nil {
				slog.Warn("ocr engine unavailable; continuing VLM-only", "err", err)
			} else {
				res, err := eng.Recognize(ctx, ocr.Input{ImagePath: imagePath})
				if err != nil {
					slog.Warn("ocr failed; continuing without", "err", err)
				} else {
					toks = res
					_ = c.Set(ocrKey, toks, nil)
				}
			}
		}
	}
	_ = ws.WriteJSON("evidence/ocr.json", map[string]any{"tokens": toks, "count": len(toks)})
	stage("ocr", start)

	// Crop plan.
	planCfg := crop.CropPlanConfig{
		CropLongSide: cfg.Image.CropLongSide,
		Overlap:      cfg.Image.CropOverlap,
		Rect:         rct(0, 0, W, H),
	}
	var plan *crop.Plan
	if o.adaptive {
		plan = crop.BuildPlan(img, toks, planCfg)
	} else {
		plan = &crop.Plan{Crops: []crop.Crop{{ID: "ov", BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: W, Y1: H}, Level: 0}}}
		plan.Crops = append(plan.Crops, crop.PlanFixed(W, H, cfg.Image.CropLongSide, cfg.Image.CropOverlap)...)
	}
	_ = ws.WriteJSON("evidence/regions.json", plan.Crops)
	stage("crop-plan", start)

	// Vision backend.
	vision, err := buildVisionBackend(cfg, o)
	if err != nil {
		slog.Warn("vision backend unavailable", "err", err)
	}

	// Analyze crops.
	var cropResults []*model.VisionResult
	var cropComponents []ir.Component
	schemaFailures := 0
	debugDir := ws.Path("crops")
	_ = debugDir
	for i, cp := range plan.Crops {
		if cp.ID == "ov" {
			// Overview handled separately; still record a stub result.
			continue
		}
		vKey := cache.Key(img.Sha256, cp.ID, "vision-v1", cfg.Vision.Model)
		var vr *model.VisionResult
		ok := false
		if c.Has(vKey) {
			ok, _ = c.Get(vKey, &vr)
		}
		if !ok || vr == nil {
			if vision == nil {
				slog.Warn("no vision backend; skipping crop", "crop", cp.ID)
				continue
			}
			vr, err = vision.Analyze(ctx, model.VisionRequest{
				ImageData:     cropBytes(img, cp),
				ImageMIME:     "image/png",
				CropID:        cp.ID,
				BBoxGlobal:    cp.BBox,
				OCRContext:    tokensIn(cp.BBox, toks),
				PromptVersion: prompt.CropAnalysisV1,
				SchemaVersion: prompt.SchemaVersion,
				Prompt:        prompt.BuildCropPrompt(cp.BBox, tokensIn(cp.BBox, toks)),
			})
			if err != nil {
				slog.Warn("crop analyze failed", "crop", cp.ID, "err", err)
				continue
			}
			_ = c.Set(vKey, vr, nil)
		}
		cropResults = append(cropResults, vr)
		if vr.SchemaFailed {
			schemaFailures++
		}
		// Persist per-crop outputs.
		_ = ws.WriteJSON("crops/"+cp.ID+".json", vr)
		if vr.Description != "" {
			_ = ws.WriteText("crops/"+cp.ID+".md", vr.Description)
		}
		for _, comp := range vr.Components {
			cropComponents = append(cropComponents, ir.Component{
				ID:         comp.ID,
				Type:       ir.ConstString{Value: comp.Type, Source: cfg.Vision.Provider, Confidence: comp.Confidence},
				BBox:       comp.BBoxGlobal,
				Text:       optionalTextValue(comp.Text),
				Semantic:   optionalRoleValue(comp.Role),
				Confidence: comp.Confidence,
				Source:     "crop-vision",
			})
		}
		_ = i
	}
	stage("vision", start)

	// Normalize + dedupe overlapping observations.
	merged := dub.Reconcile(cropComponents, dub.Options{IoUThreshold: cfg.Recon.IoUThreshold})
	graph.SortByPosition(merged)
	_ = ws.WriteJSON("evidence/merged_components.json", merged)

	// Pixel color sampling for each merged component (measured colors).
	colors := sampleColors(img, merged)
	_ = ws.WriteJSON("evidence/colors.json", colors)

	// Build canonical UI IR page.json.
	uiGraph := graph.Build(merged)
	_ = ws.WriteJSON("graph.json", uiGraph)
	stage("merge+graph", start)

	// Summaries. Each crop becomes a region with a natural-language summary
	// (regions/<id>.md); the region summaries are then condensed into the
	// page-level summary (page.md).
	pageSummary := ""
	if !o.noSummary && !cfg.Analysis.NoSummary {
		sum := summarize.New(buildTextBackend(cfg, o))
		regionMds := []string{}
		for _, cp := range plan.Crops {
			if cp.ID == "ov" {
				continue
			}
			var cr *model.VisionResult
			for _, r := range cropResults {
				if r != nil && r.CropID == cp.ID {
					cr = r
					break
				}
			}
			if cr == nil || cr.Description == "" {
				continue
			}
			regionSummary := crossRegionSummary(sum, ctx, cr)
			_ = ws.WriteText("regions/"+cp.ID+".md", regionSummary)
			regionMds = append(regionMds, regionSummary)
		}
		pageType := inferPageType(merged, toks)
		ps, sErr := sum.PageSummary(ctx, regionMds, pageType)
		if sErr == nil {
			pageSummary = ps
		}
		if pageSummary != "" {
			_ = ws.WriteText("page.md", pageSummary)
		}
	}
	stage("summary", start)

	// DOM guess (probable DOM, clearly inferred).
	dom := ""
	if !o.noDOM && cfg.Analysis.GenerateDOMGuess {
		dom = probableDOM(merged)
		_ = ws.WriteText("dom.md", dom)
		_ = ws.WriteJSON("dom.json", map[string]any{"inferred": true, "tree": dom, "note": "inferred probable DOM, not observed"})
	}
	stage("dom", start)

	// page.json
	pageJSON := map[string]any{
		"image_sha256":   img.Sha256,
		"width":          W,
		"height":         H,
		"schema_version": prompt.SchemaVersion,
		"components":     merged,
		"colors":         colors,
		"relationships_elements": uiGraph.Relationships,
		"summary":        pageSummary,
		"provenance": map[string]any{
			"vision":     cfg.Vision,
			"summary":    cfg.Summary,
			"aggregator": cfg.Aggregator,
		},
		"escalation": map[string]any{
			"schema_failures": schemaFailures,
			"unresolved":      countUnresolved(merged),
		},
		"meta": map[string]any{
			"stages": map[string]int64{
				"ocr":   msSince(start),
				"vision": msSince(start),
			},
			"crop_count": len(plan.Crops),
		},
	}
	_ = ws.WriteJSON("page.json", pageJSON)

	if flagJSON {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]any{"output": strings.TrimSuffix(o.output, "/")})
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("Analysis complete: %s (%s), %d crops, %d components.\n", strings.TrimSuffix(o.output, "/"), elapsed, len(plan.Crops), len(merged))
	return nil
}

func resolveCacheDB(path string) string {
	if path == "" {
		return filepath.Join(outputDir, ".cache")
	}
	return path
}

func cacheDir(dbPath string) string {
	return filepath.Join(dbPath, "refraict-files")
}

func countUnresolved(comps []ir.Component) int {
	n := 0
	for _, c := range comps {
		if c.Confidence < 0.5 {
			n++
		}
	}
	return n
}

func optionalTextValue(s string) *ir.ConstString {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &ir.ConstString{Value: s, Source: "ocr_or_vlm", Confidence: 0.8}
}
func optionalRoleValue(s string) *ir.ConstString {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &ir.ConstString{Value: s, Source: "vlm", Confidence: 0.7}
}

func stage(name string, start time.Time) {
	if verbose {
		fmt.Fprintf(os.Stderr, "[stage] %-12s %s\n", name, time.Since(start).Round(time.Millisecond))
	}
}
func msSince(start time.Time) int64 { return time.Since(start).Milliseconds() }
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
func rct(x0, y0, x1, y1 int) image.Rectangle { return image.Rect(x0, y0, x1, y1) }
