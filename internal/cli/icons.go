package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/iconlabel"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/ocr"
	"github.com/spf13/cobra"
)

type iconsOptions struct {
	dumpCrops string
	noLabel   bool
	runs      int
	threshold float64
	visionModel string
	keepWarm    string
}

// newIconsCmd builds the `icons` subcommand: detect + type non-text UI elements
// (icon/logo/chart/image) and, unless --no-label, identify each via vote-based
// VLM labeling over the Lucide alias map. `--dump-crops` writes the exact crop
// image fed to the VLM for each element (combine with --no-label for a fast,
// model-free crop inspection / framing-tuning loop).
func newIconsCmd() *cobra.Command {
	o := &iconsOptions{runs: -1, threshold: -1}
	cmd := &cobra.Command{
		Use:   "icons <image>",
		Short: "Detect and identify non-text UI elements (icons, logos, charts)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIcons(cmd, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.dumpCrops, "dump-crops", "", "write each element's VLM crop PNG to this directory")
	cmd.Flags().BoolVar(&o.noLabel, "no-label", false, "skip VLM labeling (detect + type + optional dump only)")
	cmd.Flags().IntVar(&o.runs, "runs", -1, "VLM samples per element for voting (default: config)")
	cmd.Flags().Float64Var(&o.threshold, "threshold", -1, "min vote agreement ratio to accept a label (default: config)")
	cmd.Flags().StringVar(&o.visionModel, "vision-model", "", "vision model name (overrides config)")
	cmd.Flags().StringVar(&o.keepWarm, "keep-warm", "", "keep the model loaded for this duration after use")
	return cmd
}

func runIcons(cmd *cobra.Command, imagePath string, o *iconsOptions) error {
	ctx := cmd.Context()
	cfg := config.Default()
	if configPath != "" {
		c, err := config.Load(configPath)
		if err != nil {
			return err
		}
		cfg = c
	}
	if o.visionModel != "" {
		cfg.Vision.Model = o.visionModel
	}
	runs := cfg.Analysis.ElementLabelRuns
	if o.runs >= 0 {
		runs = o.runs
	}
	threshold := cfg.Analysis.ElementLabelThreshold
	if o.threshold >= 0 {
		threshold = o.threshold
	}

	img, err := imageproc.Load(imagePath)
	if err != nil {
		return fail("image ingest: %w", err)
	}

	// Best-effort OCR (used for region typing: text-emptiness distinguishes
	// icons/logos). Not fatal if unavailable.
	var toks []ir.OCRToken
	if eng, e := buildOCREngine(); e == nil && eng != nil {
		if res, e2 := eng.Recognize(ctx, ocr.Input{ImagePath: imagePath}); e2 == nil {
			toks = res
		}
	}

	// Detect + type graphic regions (build-tag-selected detector).
	comps := detectRegionComponents(img.AsImage(), toks)
	var graphics []ir.Component
	for _, c := range comps {
		if isGraphicType(c.Type.Value) {
			graphics = append(graphics, c)
		}
	}

	// Optionally dump the exact crops the VLM would see.
	if o.dumpCrops != "" {
		if err := os.MkdirAll(o.dumpCrops, 0o755); err != nil {
			return fail("mkdir dump dir: %w", err)
		}
		for _, c := range graphics {
			data := elementCropBytes(img, c.BBox)
			if len(data) == 0 {
				continue
			}
			p := filepath.Join(o.dumpCrops, fmt.Sprintf("%s_%s.png", c.ID, c.Type.Value))
			if err := os.WriteFile(p, data, 0o644); err != nil {
				return fail("write crop %s: %w", p, err)
			}
		}
		fmt.Fprintf(os.Stderr, "dumped %d element crops to %s\n", len(graphics), o.dumpCrops)
	}

	// Label (vote) unless disabled.
	type result struct {
		ID         string         `json:"id"`
		Type       string         `json:"type"`
		BBox       ir.BoundingBox `json:"bbox"`
		Concept    string         `json:"concept,omitempty"`
		Agreement  int            `json:"agreement,omitempty"`
		Samples    int            `json:"samples,omitempty"`
		Confidence float64        `json:"confidence,omitempty"`
		Accepted   bool           `json:"accepted"`
	}
	results := make([]result, 0, len(graphics))

	if !o.noLabel {
		vision, verr := buildVisionBackendKeepAlive(cfg, o.keepWarm)
		if verr != nil || vision == nil {
			return fail("vision backend unavailable: %v", verr)
		}
		canon, cerr := iconlabel.New()
		if cerr != nil {
			return fail("icon canonicalizer: %w", cerr)
		}
		for _, c := range graphics {
			data := elementCropBytes(img, c.BBox)
			raw := voteRawLabels(ctx, vision, data, c, runs)
			v := canon.Vote(raw)
			results = append(results, result{
				ID: c.ID, Type: c.Type.Value, BBox: c.BBox,
				Concept: v.Concept, Agreement: v.Agreement, Samples: v.Samples,
				Confidence: v.Ratio, Accepted: v.Concept != "" && v.Ratio >= threshold,
			})
		}
	} else {
		for _, c := range graphics {
			results = append(results, result{ID: c.ID, Type: c.Type.Value, BBox: c.BBox})
		}
	}

	if flagJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"elements": results, "count": len(results)})
	}
	fmt.Printf("%d graphic elements\n", len(results))
	for _, r := range results {
		if o.noLabel {
			fmt.Printf("  %-6s %-8s [%d,%d,%d,%d]\n", r.ID, r.Type, r.BBox.X0, r.BBox.Y0, r.BBox.X1, r.BBox.Y1)
			continue
		}
		mark := " "
		if r.Accepted {
			mark = "✓"
		}
		fmt.Printf("  %s %-6s %-8s [%d,%d,%d,%d] -> %-16s %d/%d (%.2f)\n",
			mark, r.ID, r.Type, r.BBox.X0, r.BBox.Y0, r.BBox.X1, r.BBox.Y1,
			r.Concept, r.Agreement, r.Samples, r.Confidence)
	}
	return nil
}
