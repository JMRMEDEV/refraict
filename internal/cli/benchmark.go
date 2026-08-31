package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
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

func cropPlanOnly(img *imageproc.Image, W, H int) []ir.BoundingBox {
	_ = img
	_ = context.Background
	// Placeholder region count based on width/height.
	return []ir.BoundingBox{{X0: 0, Y0: 0, X1: W, Y1: H}}
}
