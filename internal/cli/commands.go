package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/crop"
	"github.com/refraict/refraict/internal/dub"
	"github.com/refraict/refraict/internal/graph"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/ocr"
	"github.com/refraict/refraict/internal/summarize"
	"github.com/refraict/refraict/internal/workdir"
	"github.com/spf13/cobra"
)

func newOCRCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ocr <image>",
		Short: "Run OCR on an image and print tokens as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := buildOCREngine()
			if err != nil || eng == nil {
				fmt.Fprintln(os.Stderr, "warning: no OCR engine configured")
				enc := json.NewEncoder(os.Stdout)
				return enc.Encode(map[string]any{"tokens": []ir.OCRToken{}, "count": 0})
			}
			toks, err := eng.Recognize(context.Background(), ocr.Input{ImagePath: args[0]})
			if err != nil {
				return fail("ocr: %w", err)
			}
			enc := json.NewEncoder(os.Stdout)
			return enc.Encode(map[string]any{"tokens": toks, "count": len(toks)})
		},
	}
}

func newRegionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "regions <image>",
		Short: "Print proposed crop/region plan as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			if configPath != "" {
				c, err := config.Load(configPath)
				if err != nil {
					return err
				}
				cfg = c
			}
			img, err := imageproc.Load(args[0])
			if err != nil {
				return fail("image: %w", err)
			}
			W, H := img.Bounds()
			var toks []ir.OCRToken
			eng, err := buildOCREngine()
			if err == nil && eng != nil {
				if t, tErr := eng.Recognize(context.Background(), ocr.Input{ImagePath: args[0]}); tErr == nil {
					toks = t
				}
			}
			plan := crop.BuildPlan(img, toks, crop.CropPlanConfig{
				CropLongSide: cfg.Image.CropLongSide,
				Overlap:      cfg.Image.CropOverlap,
				Rect:         rct(0, 0, W, H),
			})
			enc := json.NewEncoder(os.Stdout)
			return enc.Encode(map[string]any{"crops": plan.Crops, "count": len(plan.Crops), "width": W, "height": H})
		},
	}
}

func newInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <image-or-crop>",
		Short: "Show deterministic facts (dimensions, colors, hash) for an image or crop",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			img, err := imageproc.Load(args[0])
			if err != nil {
				return fail("image: %w", err)
			}
			W, H := img.Bounds()
			out := map[string]any{
				"path":   args[0],
				"width":  W,
				"height": H,
				"sha256": img.Sha256,
				"format": img.DetectFormat(),
			}
			// Sample a few anchor colors.
			if W > 0 && H > 0 {
				hex, _, _, _, ok := imageproc.SampleRegion(img.AsImage(), 0, 0, W, H, 0)
				if ok {
					out["dominant_color"] = hex
				}
			}
			if flagJSON {
				enc := json.NewEncoder(os.Stdout)
				return enc.Encode(out)
			}
			for k, v := range out {
				fmt.Printf("%s: %v\n", k, v)
			}
			return nil
		},
	}
}

func newMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge <analysis-dir>",
		Short: "Reconcile overlapping crop components in an existing analysis dir",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workdir.New(args[0])
			if err != nil {
				return err
			}
			crops := filepath.Join(args[0], "crops")
			entries, err := os.ReadDir(crops)
			if err != nil {
				return fail("read crops: %w", err)
			}
			var comps []ir.Component
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(crops, e.Name()))
				if err != nil {
					continue
				}
				var vr struct {
					Components []model.VisionCompRef `json:"components"`
				}
				if json.Unmarshal(data, &vr) != nil {
					continue
				}
				for _, c := range vr.Components {
					comps = append(comps, ir.Component{
						ID: c.ID, Type: ir.ConstString{Value: c.Type, Confidence: c.Confidence},
						BBox: c.BBoxGlobal, Confidence: c.Confidence, Source: "crop-vision",
					})
				}
			}
			merged := dub.Reconcile(comps, dub.Options{IoUThreshold: 0.65})
			graph.SortByPosition(merged)
			return ws.WriteJSON("evidence/merged_components.json", merged)
		},
	}
}

func newSummarizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "summarize <analysis-dir>",
		Short: "Regenerate region + page summaries from existing analysis",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workdir.New(args[0])
			if err != nil {
				return err
			}
			cfg := config.Default()
			sum := summarize.New(buildTextBackend(cfg, nil))
			var crops []string
			dir := filepath.Join(args[0], "crops")
			if entries, e := os.ReadDir(dir); e == nil {
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".md") {
						if d, r := os.ReadFile(filepath.Join(dir, e.Name())); r == nil {
							crops = append(crops, string(d))
						}
					}
				}
			}
			ps, err := sum.PageSummary(context.Background(), crops, "unknown")
			if err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
			return ws.WriteText("page.md", ps)
		},
	}
}

func newReconstructCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconstruct <analysis-dir>",
		Short: "Build a probable DOM/UI tree from an existing analysis",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workdir.New(args[0])
			if err != nil {
				return err
			}
			data, err := os.ReadFile(ws.Path("evidence/merged_components.json"))
			if err != nil {
				return fail("read components: %w", err)
			}
			var comps []ir.Component
			if err := json.Unmarshal(data, &comps); err != nil {
				return fail("parse components: %w", err)
			}
			dom := probableDOM(comps)
			if err := ws.WriteText("dom.md", dom); err != nil {
				return fail("write dom.md: %w", err)
			}
			fmt.Println(dom)
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("refraict v0.1.0")
		},
	}
}
