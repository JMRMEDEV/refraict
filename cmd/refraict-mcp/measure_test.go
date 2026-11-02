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

// writeFillTestOutputDir builds an analyze-style output_dir for measure_fill:
// a source image with (a) a solid blue button with white centered text, (b) a
// full-width blue-highlighted row with white text, and (c) a gradient-filled row
// with dark text — plus a page.json carrying a text component over the button so
// the text_component_id resolution path is exercised. Returns the output_dir and
// the three element rects in global coordinates.
func writeFillTestOutputDir(t *testing.T) (outDir string, buttonRect, rowRect, gradientRect [4]int, buttonTextID string) {
	t.Helper()
	root := t.TempDir()
	outDir = filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w, h := 420, 160
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	page := color.RGBA{245, 245, 245, 255} // light page background
	blue := color.RGBA{2, 96, 201, 255}     // #0260C9 accent fill
	white := color.RGBA{255, 255, 255, 255}
	dark := color.RGBA{20, 20, 20, 255}
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	// page background
	fill(0, 0, w, h, page)

	// (a) solid blue button ~90x40 at (20,20)-(110,60), white centered glyph block.
	buttonRect = [4]int{20, 20, 110, 60}
	fill(buttonRect[0], buttonRect[1], buttonRect[2], buttonRect[3], blue)
	// white "Open" centered: a glyph block covering ~40% of the button.
	fill(48, 32, 82, 48, white)

	// (b) full-width blue-highlighted menu row (0..420) x (80..112), white text.
	rowRect = [4]int{0, 80, 420, 112}
	fill(rowRect[0], rowRect[1], rowRect[2], rowRect[3], blue)
	fill(150, 88, 270, 104, white) // white "Center Align" block

	// (c) gradient-filled row (0..420) x (120..152) with dark centered text — the
	// integrity control: the fill is NOT one color, so it must NOT ground.
	gradientRect = [4]int{0, 120, 420, 152}
	for y := 120; y < 152; y++ {
		for x := 0; x < 420; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x % 256), 100, 180, 255})
		}
	}
	fill(180, 128, 240, 144, dark)

	imgPath := filepath.Join(outDir, "source.png")
	if err := imageproc.WritePNG(imgPath, img); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"image": "source.png", "width": w, "height": h}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	// page.json with a text component over the button (tight glyph bbox, as OCR
	// would emit it) so measure_fill can resolve + pad it to reach the fill.
	buttonTextID = "txt-open"
	pageJSON := map[string]any{
		"width":  w,
		"height": h,
		"components": []any{
			map[string]any{
				"id":   buttonTextID,
				"type": map[string]any{"value": "text"},
				"text": map[string]any{"value": "Open"},
				"bbox": map[string]any{"x0": 48, "y0": 32, "x1": 82, "y1": 48},
			},
		},
	}
	pb, _ := json.Marshal(pageJSON)
	if err := os.WriteFile(filepath.Join(outDir, "page.json"), pb, 0o644); err != nil {
		t.Fatal(err)
	}
	return outDir, buttonRect, rowRect, gradientRect, buttonTextID
}

// Example 1 (explicit bbox): the solid blue button's fill grounds as #0260C9
// despite the white centered text.
func TestMeasureFill_SolidButtonByBBox(t *testing.T) {
	outDir, button, _, _, _ := writeFillTestOutputDir(t)
	_, out, err := measureFill(context.Background(), nil, measureFillInput{
		OutputDir: outDir,
		BBox:      &bboxInput{X0: button[0], Y0: button[1], X1: button[2], Y1: button[3]},
		ExpectHex: "#0260C9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hex != "#0260C9" {
		t.Fatalf("fill hex = %s, want #0260C9 (ink must not flip the fill)", out.Hex)
	}
	if !out.Grounded {
		t.Fatalf("expected grounded:true for a solid button fill (coverage=%.3f)", out.Coverage)
	}
	if out.InkFraction < 0.05 {
		t.Fatalf("ink_fraction = %.3f, expected the white glyphs to be detected as ink", out.InkFraction)
	}
	if out.Expect == nil || !out.Expect.WithinTolerance {
		t.Fatalf("expected expect.within_tolerance:true, got %+v", out.Expect)
	}
	if out.Source != "bbox" {
		t.Fatalf("rect_source = %s, want bbox", out.Source)
	}
}

// Example 1 (text_component_id): the SAME button fill is reachable from the
// detected "Open" text token — the association the caller previously lacked.
func TestMeasureFill_SolidButtonByTextComponentID(t *testing.T) {
	outDir, _, _, _, textID := writeFillTestOutputDir(t)
	_, out, err := measureFill(context.Background(), nil, measureFillInput{
		OutputDir:       outDir,
		TextComponentID: textID,
		ExpectHex:       "#0260C9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hex != "#0260C9" {
		t.Fatalf("fill hex = %s, want #0260C9 via text_component_id", out.Hex)
	}
	if !out.Grounded {
		t.Fatalf("expected grounded:true for the button fill behind 'Open' (coverage=%.3f)", out.Coverage)
	}
	if out.Source != "text_component_id" {
		t.Fatalf("rect_source = %s, want text_component_id", out.Source)
	}
	if out.Expect == nil || !out.Expect.WithinTolerance {
		t.Fatalf("expected within_tolerance for #0260C9, got %+v", out.Expect)
	}
}

// Example 2: the full-width blue-highlighted menu row grounds as the accent blue.
func TestMeasureFill_FullWidthHighlightedRow(t *testing.T) {
	outDir, _, row, _, _ := writeFillTestOutputDir(t)
	_, out, err := measureFill(context.Background(), nil, measureFillInput{
		OutputDir: outDir,
		BBox:      &bboxInput{X0: row[0], Y0: row[1], X1: row[2], Y1: row[3]},
		ExpectHex: "#0260C9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hex != "#0260C9" {
		t.Fatalf("row fill hex = %s, want #0260C9", out.Hex)
	}
	if !out.Grounded {
		t.Fatalf("expected grounded:true for the solid highlighted row (coverage=%.3f)", out.Coverage)
	}
}

// Integrity control: a GRADIENT-filled row behind text must NOT ground —
// removing the ink does not rescue an ambiguous fill.
func TestMeasureFill_GradientNotGrounded(t *testing.T) {
	outDir, _, _, grad, _ := writeFillTestOutputDir(t)
	_, out, err := measureFill(context.Background(), nil, measureFillInput{
		OutputDir: outDir,
		BBox:      &bboxInput{X0: grad[0], Y0: grad[1], X1: grad[2], Y1: grad[3]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Grounded {
		t.Fatalf("expected grounded:false for a gradient fill (coverage=%.3f)", out.Coverage)
	}
	if out.Note == "" {
		t.Fatal("expected an explanatory note on a non-grounded fill")
	}
}

// Input-validation: exactly one of text_component_id / bbox must be given.
func TestMeasureFill_InputValidation(t *testing.T) {
	outDir, button, _, _, textID := writeFillTestOutputDir(t)
	ctx := context.Background()
	// Neither.
	if _, _, err := measureFill(ctx, nil, measureFillInput{OutputDir: outDir}); err == nil {
		t.Fatal("expected error when neither text_component_id nor bbox is given")
	}
	// Both.
	if _, _, err := measureFill(ctx, nil, measureFillInput{
		OutputDir:       outDir,
		TextComponentID: textID,
		BBox:            &bboxInput{X0: button[0], Y0: button[1], X1: button[2], Y1: button[3]},
	}); err == nil {
		t.Fatal("expected error when BOTH text_component_id and bbox are given")
	}
}

// Missing component: an unknown text_component_id returns an actionable error.
func TestMeasureFill_UnknownComponent(t *testing.T) {
	outDir, _, _, _, _ := writeFillTestOutputDir(t)
	if _, _, err := measureFill(context.Background(), nil, measureFillInput{
		OutputDir:       outDir,
		TextComponentID: "does-not-exist",
	}); err == nil {
		t.Fatal("expected error for an unknown text_component_id")
	}
}

// Sandbox-escape guard: a crafted manifest image path escaping via ".." is
// rejected, same as measure_region.
func TestMeasureFill_SandboxEscape(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "secret.png")
	if err := imageproc.WritePNG(outside, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"image": filepath.Join("..", "..", "secret.png")}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := measureFill(context.Background(), nil, measureFillInput{
		OutputDir: outDir,
		BBox:      &bboxInput{X0: 0, Y0: 0, X1: 4, Y1: 4},
	}); err == nil {
		t.Fatal("expected a sandbox-escape rejection, got nil error")
	}
}
