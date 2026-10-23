package main

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/refraict/refraict/internal/imageproc"
)

// writeTestOutputDir builds a minimal analyze-style output_dir: a source image
// PNG plus a manifest.json whose "image" field points at it (relative, as the
// real pipeline writes it). Returns the output_dir path. The image is a thin
// SOLID green bar on a white band — the F-UI-5 shape — with an optional mixed
// strip the control case targets.
func writeTestOutputDir(t *testing.T) (outDir string, barRect, mixedRect [4]int) {
	t.Helper()
	root := t.TempDir()
	outDir = filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 200x40 image: rows 10..26 solid green bar (full width); the right half of
	// a different band (rows 0..10) is a hard green/white split = a mixed region.
	w, h := 200, 40
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	green := color.RGBA{7, 138, 82, 255}
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case y >= 10 && y < 26:
				img.SetRGBA(x, y, green) // the solid bar
			case y < 10 && x >= 100:
				img.SetRGBA(x, y, green) // half of the mixed strip
			default:
				img.SetRGBA(x, y, white)
			}
		}
	}
	imgPath := filepath.Join(outDir, "source.png")
	if err := imageproc.WritePNG(imgPath, img); err != nil {
		t.Fatal(err)
	}
	// manifest.json with a RELATIVE image path (as analyze writes the path given).
	manifest := map[string]any{
		"image":        "source.png",
		"image_sha256": "test",
		"width":        w,
		"height":       h,
	}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	return outDir, [4]int{0, 10, 200, 26}, [4]int{0, 0, 200, 10}
}

// Grounded case: the thin solid green bar is measured as green with high
// coverage/confidence and grounded:true — the F-UI-5 before→after.
func TestMeasureRegion_GroundedSolidBar(t *testing.T) {
	outDir, bar, _ := writeTestOutputDir(t)
	ctx := context.Background()
	_, out, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir,
		BBox:      bboxInput{X0: bar[0], Y0: bar[1], X1: bar[2], Y1: bar[3]},
		ExpectHex: "#078A52",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hex != "#078A52" {
		t.Fatalf("hex = %s, want #078A52", out.Hex)
	}
	if !out.Grounded {
		t.Fatalf("expected grounded:true for a solid bar (coverage=%.3f)", out.Coverage)
	}
	if out.Coverage < 0.99 {
		t.Fatalf("coverage = %.3f, want ~1.0", out.Coverage)
	}
	if out.Expect == nil || !out.Expect.WithinTolerance {
		t.Fatalf("expected expect.within_tolerance:true, got %+v", out.Expect)
	}
}

// Control / integrity case: a genuinely mixed (two-tone) region returns LOW
// coverage and grounded:false — it does NOT falsely ground. This is the
// headline property verify_ui relies on.
func TestMeasureRegion_MixedNotGrounded(t *testing.T) {
	outDir, _, mixed := writeTestOutputDir(t)
	ctx := context.Background()
	_, out, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir,
		BBox:      bboxInput{X0: mixed[0], Y0: mixed[1], X1: mixed[2], Y1: mixed[3]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Grounded {
		t.Fatalf("expected grounded:false for a two-tone region (coverage=%.3f)", out.Coverage)
	}
	if out.Coverage > 0.60 {
		t.Fatalf("coverage = %.3f, want LOW for a mixed region", out.Coverage)
	}
	if out.Note == "" {
		t.Fatal("expected an explanatory note on a non-grounded result")
	}
}

// Sandbox-escape guard: a manifest whose image path escapes the sandbox via
// ".." must be REJECTED, not silently measured.
func TestMeasureRegion_SandboxEscape(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a real file outside the sandbox root so only the GUARD (not a
	// missing-file error) can be what rejects it.
	outside := filepath.Join(root, "secret.png")
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := imageproc.WritePNG(outside, img); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"image": filepath.Join("..", "..", "secret.png")}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir,
		BBox:      bboxInput{X0: 0, Y0: 0, X1: 4, Y1: 4},
	})
	if err == nil {
		t.Fatal("expected a sandbox-escape rejection, got nil error")
	}
}

// Missing-artifact guard: an output_dir without manifest.json returns an
// actionable error, not a panic or a false measurement.
func TestMeasureRegion_MissingArtifact(t *testing.T) {
	outDir := t.TempDir() // no manifest.json
	ctx := context.Background()
	_, _, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir,
		BBox:      bboxInput{X0: 0, Y0: 0, X1: 10, Y1: 10},
	})
	if err == nil {
		t.Fatal("expected an error for a missing manifest, got nil")
	}
}

// Degenerate / out-of-bounds bbox guards.
func TestMeasureRegion_BadBBox(t *testing.T) {
	outDir, _, _ := writeTestOutputDir(t)
	ctx := context.Background()
	// Degenerate (x1<=x0).
	if _, _, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir, BBox: bboxInput{X0: 10, Y0: 0, X1: 10, Y1: 5},
	}); err == nil {
		t.Fatal("expected error for a degenerate bbox")
	}
	// Entirely outside the image.
	if _, _, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir, BBox: bboxInput{X0: 500, Y0: 500, X1: 600, Y1: 600},
	}); err == nil {
		t.Fatal("expected error for an out-of-bounds bbox")
	}
}

// Real TASK-378 fixture (the canonical F-UI-5 frame), when present: the thin
// green bar grounds as #078A52. Skipped if the fixture isn't in the tree.
func TestMeasureRegion_TASK378Fixture(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "task378-checklist-bar.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Skip("TASK-378 fixture not present")
	}
	outDir := t.TempDir()
	manifest := map[string]any{"image": fixture} // absolute path
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The measured green band from the empirical probe: y=127..142, x≈32..1216.
	_, out, err := measureRegion(ctx, nil, measureRegionInput{
		OutputDir: outDir,
		BBox:      bboxInput{X0: 40, Y0: 128, X1: 1210, Y1: 142},
		ExpectHex: "#078A52",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hex != "#078A52" {
		t.Fatalf("hex = %s, want #078A52", out.Hex)
	}
	if !out.Grounded {
		t.Fatalf("expected grounded:true for the solid TASK-378 bar (coverage=%.3f)", out.Coverage)
	}
	if out.Expect == nil || !out.Expect.WithinTolerance {
		t.Fatalf("expected within_tolerance for #078A52, got %+v", out.Expect)
	}
}
