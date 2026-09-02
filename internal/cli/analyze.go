package cli

import (
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/refraict/refraict/internal/cache"
	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/crop"
	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/dub"
	"github.com/refraict/refraict/internal/escalate"
	"github.com/refraict/refraict/internal/graph"
	"github.com/refraict/refraict/internal/iconlabel"
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
	visionModel        string
	summaryModel       string
	aggregatorModel    string
	visionProvider     string
	summaryProvider    string
	aggregatorProvider string
	cropSize           int
	cropOverlap        float64
	minTextHeight      int
	batchSize          int
	workers            int
	noOCR              bool
	noSummary          bool
	noDOM              bool
	cloudFallback      bool
	output             string
	adaptive           bool
	keepWarm           string
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
	cmd.Flags().StringVar(&o.keepWarm, "keep-warm", "", "keep local models loaded for this duration after use (e.g. 30s, 5m, -1 for indefinite); default frees them immediately")
	return cmd
}

// runAnalyze executes the full pipeline.
func runAnalyze(cmd *cobra.Command, imagePath string, o *analysisOptions) error {
	ctx := cmd.Context()
	start := time.Now()
	resetStageTimes()
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
	cacheDB := resolveCacheDB(cfg.Cache.Dir)
	c, err := cache.New(cacheDir(cacheDB), cfg.Cache.Enabled)
	if err != nil {
		return err
	}

	// manifest.
	manifest := map[string]any{
		"image":        imagePath,
		"image_sha256": img.Sha256,
		"width":        W,
		"height":       H,
		"started_at":   time.Now(),
	}
	if err := ws.WriteJSON("manifest.json", manifest); err != nil {
		return fail("write manifest: %w", err)
	}

	// Overview image.
	overview := img.Resize(cfg.Image.OverviewWidth)
	if err := imageproc.WritePNG(ws.Path("overview.png"), overview); err != nil {
		return err
	}
	stage("overview", start)

	// OCR.
	var toks []ir.OCRToken
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
	writeArtifact(func() error { return ws.WriteJSON("evidence/ocr.json", map[string]any{"tokens": toks, "count": len(toks)}) })
	stage("ocr", start)

	// Classify the page type once, from OCR text, before the vision stage so it
	// can frame the crop prompts (reducing content-driven mislabels, e.g. a
	// task-detail view being described as a login page). Reused for the page
	// summary below.
	pageTypeHint := inferPageType(nil, toks)

	// Crop plan.
	planCfg := crop.CropPlanConfig{
		CropLongSide:      cfg.Image.CropLongSide,
		Overlap:           cfg.Image.CropOverlap,
		Rect:              rct(0, 0, W, H),
		DetailLongSide:    cfg.Image.DetailLongSide,
		MinimumTextHeight: cfg.Image.MinimumTextHeightAfter,
	}
	var plan *crop.Plan
	switch {
	case !o.adaptive:
		// Explicit fixed-grid benchmark strategy (legacy --adaptive=false).
		plan = &crop.Plan{Crops: []crop.Crop{{ID: "ov", BBox: ir.BoundingBox{X0: 0, Y0: 0, X1: W, Y1: H}, Level: 0}}}
		plan.Crops = append(plan.Crops, crop.PlanFixed(W, H, cfg.Image.CropLongSide, cfg.Image.CropOverlap)...)
	case cfg.Image.CropStrategy == "adaptive":
		// Legacy OCR-density-driven subdivision (opt-in). Can explode the crop
		// count on text-dense pages; retained for comparison/benchmarking.
		plan = crop.BuildPlan(img, toks, planCfg)
	default:
		// Default: bounded overview + fixed grid of higher-res focused tiles.
		// Produces exactly 1 + GridRows*GridCols VLM calls regardless of image
		// content, keeping a single model warm and avoiding OOM (see gap report).
		rows := cfg.Image.GridRows
		cols := cfg.Image.GridCols
		if rows < 1 {
			rows = 2
		}
		if cols < 1 {
			cols = 2
		}
		plan = crop.PlanOverviewGrid(W, H, crop.GridPlanConfig{
			Rows:           rows,
			Cols:           cols,
			Overlap:        cfg.Image.CropOverlap,
			DetailLongSide: cfg.Image.DetailLongSide,
		})
	}
	writeArtifact(func() error { return ws.WriteJSON("evidence/regions.json", plan.Crops) })
	stage("crop-plan", start)

	// Vision backend.
	vision, err := buildVisionBackend(cfg, o)
	if err != nil {
		slog.Warn("vision backend unavailable", "err", err)
	}

	// Analyze crops. Vision inference is the expensive step, so it is run
	// concurrently under a worker-pool limited by cfg.Vision.Workers, while
	// persistence/repair/counting remain serial and ordered (QA finding G4).
	workers := cfg.Vision.Workers
	if workers < 1 {
		workers = 1
	}
	type cropOutcome struct {
		cp *crop.Crop
		vr *model.VisionResult
	}
	analyzeCrops := make([]crop.Crop, 0, len(plan.Crops))
	for i := range plan.Crops {
		cp := plan.Crops[i]
		// The overview crop IS analyzed (low-res whole-page context) so the VLM
		// contributes coarse page-level layout; the higher-res tiles then add
		// detail. Overlapping observations are reconciled downstream by IoU.
		analyzeCrops = append(analyzeCrops, cp)
	}
	outcomes := make([]cropOutcome, len(analyzeCrops))
	sem := make(chan struct{}, workers)
	// Phase A: parallel inference (cache-aware), preserving input order.
	for i := range analyzeCrops {
		i := i
		cp := analyzeCrops[i]
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			var vr *model.VisionResult
			vKey := cache.Key(img.Sha256, cp.ID, "vision-v2", cfg.Vision.Model)
			ok := false
			if c.Has(vKey) {
				ok, _ = c.Get(vKey, &vr)
			}
			if ok && vr != nil {
				outcomes[i] = cropOutcome{cp: &analyzeCrops[i], vr: vr}
				return
			}
			if vision == nil {
				slog.Warn("no vision backend; skipping crop", "crop", cp.ID)
				return
			}
			cropToks := tokensIn(cp.BBox, toks)
			cropColors := cropDominantColors(img, cp.BBox)
			res, aerr := vision.Analyze(ctx, model.VisionRequest{
				ImageData:     cropBytes(img, cp),
				ImageMIME:     "image/png",
				CropID:        cp.ID,
				BBoxGlobal:    cp.BBox,
				OCRContext:    cropToks,
				PromptVersion: prompt.CropAnalysisV1,
				SchemaVersion: prompt.SchemaVersion,
				Prompt:        prompt.BuildGroundedCropPromptTyped(cp.BBox, cropToks, cropColors, pageTypeHint),
			})
			if aerr != nil {
				slog.Warn("crop analyze failed", "crop", cp.ID, "err", aerr)
				return
			}
			_ = c.Set(vKey, res, nil)
			outcomes[i] = cropOutcome{cp: &analyzeCrops[i], vr: res}
		}()
	}
	// Phase B: serial processing of results in deterministic order.
	var cropResults []*model.VisionResult
	var cropComponents []ir.Component
	schemaFailures := 0
	boxFailures := 0
	boxRepairs := 0
	boxDrops := 0
	for _, oc := range outcomes {
		cp := oc.cp
		if cp == nil || oc.vr == nil {
			continue
		}
		vr := oc.vr
		cropResults = append(cropResults, vr)
		if vr.SchemaFailed {
			schemaFailures++
		}
		// Persist per-crop outputs.
		writeArtifact(func() error { return ws.WriteJSON("crops/"+cp.ID+".json", vr) })
		if vr.Description != "" {
			writeArtifact(func() error { return ws.WriteText("crops/"+cp.ID+".md", vr.Description) })
		}
		// Repair/normalize each component: synthesize IDs, recover empty boxes
		// (via OCR text match or the enclosing crop's box), and clamp to bounds.
		ocrCropTokens := tokensIn(cp.BBox, toks)
		for idx, comp := range vr.Components {
			cc, oc := repairComponent(comp, cp.ID, idx, cp.BBox, ocrCropTokens, W, H)
			if oc.Dropped {
				boxFailures++
				boxDrops++
				continue
			}
			if oc.BoxesFlagged {
				boxFailures++
			}
			if oc.Repaired {
				boxRepairs++
			}
			cropComponents = append(cropComponents, cc)
		}
	}
	stage("vision", start)

	// Deterministic component synthesis. Small local VLMs cannot reliably emit
	// precise bounding boxes, so components are derived from measured evidence
	// (OCR tokens) rather than trusting the model's geometry. VLM output still
	// contributes semantic role/description at the region level. This is the
	// key reliability fix: components exist even when the VLM schema fails.
	ocrComps := detect.TextComponentsFromOCR(toks, detect.DefaultTextComponentOptions())
	cropComponents = append(cropComponents, ocrComps...)
	slog.Info("synthesized deterministic components", "ocr_components", len(ocrComps), "vlm_components", len(cropComponents)-len(ocrComps))

	// Deterministic non-text region detection (cards, panels, chart containers).
	// Runs when enabled; the detector implementation is selected at build time
	// (pure-Go by default, OpenCV Canny with `-tags opencv`). These cv_region
	// components merge with OCR/VLM components in the reconciler by overlap.
	var regionComps []ir.Component
	if cfg.Analysis.DetectRegions {
		regionComps = detectRegionComponents(img.AsImage(), toks)
		cropComponents = append(cropComponents, regionComps...)
		slog.Info("detected non-text regions", "region_components", len(regionComps))
	}

	// Normalize + dedupe overlapping observations.
	merged := dub.Reconcile(cropComponents, dub.Options{
		IoUThreshold:    cfg.Recon.IoUThreshold,
		ConfidenceMerge: cfg.Recon.ConfidenceMerge,
	})
	graph.SortByPosition(merged)

	// Tier-2 grounded VLM labeling of graphic elements (icon/logo/chart/image):
	// each such region is cropped and given a short element label attached to
	// Component.Semantic (inference). Bounded by MaxElementLabels; no-op without
	// a vision backend. Deterministic geometry/colors remain the source of
	// truth; these labels are interpretation.
	if cfg.Analysis.LabelElements {
		prof := resolveVisionProfile(cfg)
		canon, cErr := iconlabel.NewWithProfile(prof.MaxLabelWords, prof.GarbageMarkers)
		if cErr != nil {
			slog.Warn("icon-label canonicalizer unavailable; skipping element labels", "err", cErr)
		} else {
			n := labelGraphicElements(ctx, vision, canon, img, merged, toks,
				cfg.Analysis.MaxElementLabels, cfg.Analysis.ElementLabelRuns,
				cfg.Analysis.ElementLabelThreshold, elementPadFrac(cfg), cfg.Vision.Provider, cfg.Vision.Model)
			slog.Info("labeled graphic elements (voted)", "count", n,
				"runs", cfg.Analysis.ElementLabelRuns, "threshold", cfg.Analysis.ElementLabelThreshold)
		}
	}
	writeArtifact(func() error { return ws.WriteJSON("evidence/merged_components.json", merged) })

	// Pixel color sampling for each merged component (measured colors).
	colors := sampleColors(img, merged)
	writeArtifact(func() error { return ws.WriteJSON("evidence/colors.json", colors) })

	// Build canonical UI IR page.json. Geometric relationships are derived
	// deterministically, then a text backend (if available) augments them with
	// model-inferred relationships (G7). A nil backend falls back to geometry.
	uiGraph := inferPageGraph(ctx, merged, buildTextBackend(cfg, o))
	writeArtifact(func() error { return ws.WriteJSON("graph.json", uiGraph) })
	stage("merge+graph", start)

	// Summaries. Each crop becomes a region with a natural-language summary
	// (regions/<id>.md); the region summaries are then condensed into the
	// page-level summary (page.md). When confidence/schema/resolution signals
	// warrant it (and no LocalOnly is set), the page-level aggregation is
	// escalated to the dedicated aggregator backend (M4). If cloud escalation
	// is allowed and text redaction is configured, sensitive text is redacted
	// before it leaves local processing (M2).
	pageSummary := ""
	if !o.noSummary && !cfg.Analysis.NoSummary {
		sum := summarize.New(buildTextBackend(cfg, o))
		// gemma's whole-image (overview) description — the "original summary".
		overviewDesc := ""
		for _, r := range cropResults {
			if r != nil && r.CropID == "ov" {
				overviewDesc = strings.TrimSpace(r.Description)
				break
			}
		}
		regionMds := []string{}
		sections := []summarize.Section{}
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
			writeArtifact(func() error { return ws.WriteText("regions/"+cp.ID+".md", regionSummary) })
			regionMds = append(regionMds, regionSummary)
			sections = append(sections, summarize.Section{ID: cp.ID, Description: regionSummary})
		}
		pageType := pageTypeHint

		// Decide whether to escalate the page-level aggregation to a stronger
		// backend (M4) and, if so, whether it may leave local processing (M2).
		signal := escalate.Signal{
			PageConfidence:       pageConfidence(merged),
			UnresolvedComponents: unresolvedComponents(merged, cfg.Analysis.ConfidenceThreshold),
			CropDisagreementRate: cropDisagreementRate(boxFailures, boxRepairs, len(analyzeCrops)),
			SchemaFailures:       schemaFailures,
		}
		policy := escalate.DefaultPolicy()
		escalateToCloud := escalate.NeedsEscalation(signal, policy) &&
			cfg.Cloud.AllowCloud && !cfg.Cloud.LocalOnly

		if escalateToCloud {
			// Escalation path: use the stronger backend to genuinely synthesize
			// across regions (the one case where a text model earns its cost).
			aggBackend := buildAggregatorBackend(cfg)
			textRegionMds := regionMds
			if cfg.Cloud.RedactText {
				textRegionMds = redactAll(regionMds)
			}
			if aggBackend != nil {
				if ps, err := summarize.New(aggBackend).PageSummary(ctx, textRegionMds, pageType); err == nil && ps != "" {
					pageSummary = ps
					slog.Info("escalated page aggregation to stronger backend", "policy", "cloud")
				}
			}
		}
		// Default (and fallback): deterministic assembly of gemma's own grounded
		// descriptions — the whole-image overview read first, then each section
		// verbatim. No text model: a straight concatenation needs no summarizer,
		// which removes the small text model's hallucination/latency from the
		// default path. (The qwen PageSummary aggregation is retained only for the
		// cloud-escalation case above, where cross-region synthesis is warranted.)
		if pageSummary == "" {
			pageSummary = summarize.AssemblePage(pageType, overviewDesc, sections)
		}
		if pageSummary != "" {
			writeArtifact(func() error { return ws.WriteText("page.md", pageSummary) })
		}
	}
	stage("summary", start)

	// Grounding guard (deterministic, no model): flag summary claims about
	// colors or behavior that the measured evidence does not support, and emit
	// a machine-readable report so a downstream agent can decide whether to
	// trust the summary or fall back to a direct (paid) image read.
	grounding := detect.CheckGrounding(pageSummary, colors, toks, resolveVisionProfile(cfg).StripHexInNumbers)
	writeArtifact(func() error { return ws.WriteJSON("evidence/grounding.json", grounding) })

	// Cross-check (Gap 7 first step): compare gemma's whole-image (overview)
	// read against the deterministic measured evidence (OCR text, colors,
	// detected components). This turns the two independent reads — the overview
	// pass and the per-crop/measurement pipeline — into a mutual grounding
	// signal. Passive/diagnostic: the agent decides whether to trust page.md.
	ovDesc := ""
	for _, r := range cropResults {
		if r != nil && r.CropID == "ov" {
			ovDesc = strings.TrimSpace(r.Description)
			break
		}
	}
	crosscheck := detect.CrossCheck(ovDesc, colors, toks, merged)
	writeArtifact(func() error { return ws.WriteJSON("evidence/crosscheck.json", crosscheck) })

	// DOM guess (probable DOM, clearly inferred).
	dom := ""
	if !o.noDOM && cfg.Analysis.GenerateDOMGuess {
		dom = probableDOM(merged)
		writeArtifact(func() error { return ws.WriteText("dom.md", dom) })
		writeArtifact(func() error { return ws.WriteJSON("dom.json", map[string]any{"inferred": true, "tree": dom, "note": "inferred probable DOM, not observed"}) })
	}
	stage("dom", start)

	// page.json
	pageJSON := map[string]any{
		"image_sha256":           img.Sha256,
		"width":                  W,
		"height":                 H,
		"schema_version":         prompt.SchemaVersion,
		"components":             merged,
		"colors":                 colors,
		"relationships_elements": uiGraph.Relationships,
		"summary":                pageSummary,
		"grounding":              grounding,
		"crosscheck":             crosscheck,
		"provenance": map[string]any{
			"vision":     cfg.Vision,
			"summary":    cfg.Summary,
			"aggregator": cfg.Aggregator,
		},
		"escalation": map[string]any{
			"schema_failures":        schemaFailures,
			"components_box_failed":  boxFailures,
			"components_box_repairs": boxRepairs,
			"components_dropped":     boxDrops,
			"unresolved":             countUnresolved(merged),
		},
		"meta": map[string]any{
			"stages":     stageTimes,
			"crop_count": len(plan.Crops),
		},
	}
	// page.json is the flagship artifact, so a failed write must surface
	// rather than be swallowed (QA finding G8).
	if err := ws.WriteJSON("page.json", pageJSON); err != nil {
		return fail("write page.json: %w", err)
	}

	if flagJSON {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]any{"output": strings.TrimSuffix(o.output, "/")})
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("Analysis complete: %s (%s), %d crops, %d components.\n", strings.TrimSuffix(o.output, "/"), elapsed, len(plan.Crops), len(merged))
	return nil
}

// writeArtifact is a non-fatal convenience for auxiliary artifacts: it returns
// the write error but logs it so a failed write during analyze is visible
// rather than silently swallowed (QA finding G8).
func writeArtifact(write func() error) {
	if err := write(); err != nil {
		slog.Warn("failed to write artifact", "err", err)
	}
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
func stage(name string, start time.Time) {
	if verbose {
		fmt.Fprintf(os.Stderr, "[stage] %-12s %s\n", name, time.Since(start).Round(time.Millisecond))
	}
	now := time.Now()
	if !lastStageAt.IsZero() {
		stageTimes[name] = now.Sub(lastStageAt).Milliseconds()
	}
	lastStageAt = now
}

// stageTimes records per-stage durations (ms) keyed by stage name, used to
// produce honest per-stage timings in page.json (see C3).
var stageTimes = map[string]int64{}

// lastStageAt is the end time of the most recently completed stage.
var lastStageAt time.Time

func resetStageTimes() {
	stageTimes = map[string]int64{}
	lastStageAt = time.Time{}
}
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
func rct(x0, y0, x1, y1 int) image.Rectangle { return image.Rect(x0, y0, x1, y1) }
