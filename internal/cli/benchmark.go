package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/refraict/refraict/internal/config"
	"github.com/refraict/refraict/internal/crop"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/spf13/cobra"
)

func newBenchmarkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "benchmark <dataset-dir>",
		Short: "Run benchmark evaluation over an image dataset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fail("read dataset: %w", err)
			}
			var results []map[string]any
			for _, e := range entries {
				if e.IsDir() || !isImage(e.Name()) {
					continue
				}
				path := filepath.Join(dir, e.Name())
				img, err := imageproc.Load(path)
				if err != nil {
					results = append(results, map[string]any{"file": e.Name(), "error": err.Error()})
					continue
				}
				W, H := img.Bounds()
				// Deterministic-only benchmark (no model): report geometry.
				results = append(results, map[string]any{
					"file":    e.Name(),
					"width":   W,
					"height":  H,
					"sha256":  img.Sha256[:12],
					"ingest":  "ok",
					"regions": len(cropPlanOnly(img, W, H)),
				})
			}
			fmt.Printf("Benchmarked %d images in %s\n", len(results), dir)
			for _, r := range results {
				fmt.Printf("  %s: %v x %v, regions=%v\n", r["file"], r["width"], r["height"], r["regions"])
			}
			return nil
		},
	}
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// cropPlanOnly returns the crop regions a deterministic (no-OCR) analysis would
// produce for an image, using the real fixed-tile planner. See QA finding B5.
func cropPlanOnly(img *imageproc.Image, W, H int) []crop.Crop {
	_ = img
	cfg := config.Default()
	side := cfg.Image.CropLongSide
	if side <= 0 {
		side = 1280
	}
	overlap := cfg.Image.CropOverlap
	return crop.PlanFixed(W, H, side, overlap)
}
