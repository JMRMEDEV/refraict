package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/refraict/refraict/internal/imageproc"
)

// measure_region is the targeted-region measured-color verb (roadmap F-UI-5).
//
// WHY IT EXISTS. refraict's auto-detected color samples come from the region
// detector, which (by design) drops thin, elongated elements — a full-width
// progress bar / divider / 1-2px underline / small chip falls between the
// "card/panel" size band and the roughly-square "icon" band, so it never becomes
// its own measurable sample and nearestColor() has nothing to ground against. A
// genuinely correct, visually-obvious color on such an element is therefore
// UNVERIFIABLE-by-measurement and must route to a human (verify_ui's honest
// UNVERIFIABLE path, ADR-0016).
//
// measure_region lowers that floor WITHOUT weakening the detector or any
// confidence threshold: given an explicit bbox (the rect the acceptance criterion
// is about — e.g. a testid's rendered rect the capture driver knows), it samples
// the DOMINANT measured color of exactly that rect from the analyze image artifact
// and returns {hex, rgb, coverage, confidence, grounded}. It MEASURES a rect it is
// GIVEN; it never guesses one.
//
// INTEGRITY (the crux). The confidence is REAL, derived from COVERAGE — the
// fraction of pixels close to the dominant color. A solid fill → coverage ≈ 1.0 →
// grounded. A mixed / antialiased / gradient region → low coverage → low
// confidence → grounded:false, so the conductor keeps it UNVERIFIABLE → human.
// The floor is lowered by measuring the RIGHT rect, not by relaxing what counts
// as "measured."

// groundedCoverageFloor is the coverage below which a measurement is NOT
// considered grounded. A solid region sits ~1.0; antialiased/mixed regions fall
// well under this. Conservative by intent: it is better to withhold (→ human)
// than to falsely ground an ambiguous region.
const groundedCoverageFloor = 0.90

// minMeasurablePixels rejects a degenerate rect that samples too few pixels to be
// a trustworthy measurement (grounded stays false).
const minMeasurablePixels = 16

// nearThreshDefault is the per-pixel RGB distance within which a pixel counts as
// "the dominant color" for the coverage computation.
const nearThreshDefault = 24.0

type bboxInput struct {
	X0 int `json:"x0" jsonschema:"left edge (global image x, inclusive)"`
	Y0 int `json:"y0" jsonschema:"top edge (global image y, inclusive)"`
	X1 int `json:"x1" jsonschema:"right edge (global image x, exclusive)"`
	Y1 int `json:"y1" jsonschema:"bottom edge (global image y, exclusive)"`
}

type measureRegionInput struct {
	OutputDir string    `json:"output_dir" jsonschema:"the output_dir returned by a prior analyze call (its manifest.json resolves the source image)"`
	BBox      bboxInput `json:"bbox" jsonschema:"rectangle to measure, in GLOBAL image coordinates (same frame as component bboxes)"`
	BorderPct float64   `json:"border_pct,omitempty" jsonschema:"fraction trimmed from each edge before sampling, to avoid border/antialias contamination (default 0.0)"`
	ExpectHex string    `json:"expect_hex,omitempty" jsonschema:"optional expected color hex (e.g. #078A52); if set, the result also reports the measured distance to it and whether it is within tolerance"`
	Tolerance int       `json:"tolerance,omitempty" jsonschema:"RGB distance tolerance for expect_hex (default 24)"`
}

type expectResult struct {
	Hex             string `json:"hex"`
	Distance        int    `json:"distance"`
	WithinTolerance bool   `json:"within_tolerance"`
}

type measureRegionOutput struct {
	Hex           string        `json:"hex"`
	RGB           [3]int        `json:"rgb"`
	Coverage      float64       `json:"coverage"`
	Confidence    float64       `json:"confidence"`
	Grounded      bool          `json:"grounded"`
	PixelsSampled int           `json:"pixels_sampled"`
	BBox          bboxInput     `json:"bbox"`
	Expect        *expectResult `json:"expect,omitempty"`
	Note          string        `json:"note,omitempty"`
}

// resolveSourceImage reads <output_dir>/manifest.json and returns the absolute
// path to the analyzed source image, guarded against sandbox escape. The manifest
// "image" field is the path passed to analyze (relative to the analyze CWD or
// absolute). A relative path is resolved against output_dir's parent is NOT
// assumed — refraict writes the path as-given, so we try (1) as-is, (2) relative
// to output_dir. Either way the final resolved path must stay inside a sane
// sandbox: we require it to resolve to an existing file and reject a manifest
// image that escapes above the output_dir's parent via "..".
func resolveSourceImage(outputDir string) (string, error) {
	manifestPath := filepath.Join(outputDir, "manifest.json")
	if !within(outputDir, manifestPath) {
		return "", fmt.Errorf("resolved manifest path escapes output_dir")
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read manifest.json (is this an analyze output_dir?): %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", fmt.Errorf("parse manifest.json: %w", err)
	}
	img, _ := m["image"].(string)
	if img == "" {
		return "", fmt.Errorf("manifest.json has no image field")
	}
	// INTEGRITY GUARD. The source image is inherently EXTERNAL to output_dir (it
	// is the screenshot refraict analyzed, whose path analyze recorded in the
	// manifest) — so we cannot sandbox it to output_dir. The real threat is a
	// CRAFTED manifest using ".." traversal to read an arbitrary file. Defense:
	// reject any image path containing a ".." segment (upward traversal). This
	// blocks the escape attack while allowing the two legitimate shapes refraict
	// writes: an absolute path, or a path relative to the analyze CWD / output_dir.
	if hasDotDotSegment(img) {
		return "", fmt.Errorf("manifest image path escapes sandbox (contains \"..\")")
	}
	// Candidate resolutions for a relative path, in order of preference:
	// (1) as-is (relative to the MCP process CWD — the common case, since the MCP
	//     runs analyze in-process from its own CWD), (2) under output_dir, (3) under
	//     output_dir's parent. A ".."-free relative path cannot traverse upward, so
	//     every candidate stays at or below its base.
	candidates := []string{img}
	if !filepath.IsAbs(img) {
		candidates = append(candidates,
			filepath.Join(outputDir, img),
			filepath.Join(filepath.Dir(outputDir), img),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("source image %q (from manifest) not found", img)
}

// hasDotDotSegment reports whether a path contains a ".." path segment (upward
// traversal), checking both separators so it is robust on any platform.
func hasDotDotSegment(p string) bool {
	norm := strings.ReplaceAll(p, "\\", "/")
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func measureRegion(_ context.Context, _ *mcp.CallToolRequest, in measureRegionInput) (*mcp.CallToolResult, measureRegionOutput, error) {
	if in.OutputDir == "" {
		return nil, measureRegionOutput{}, fmt.Errorf("output_dir is required")
	}
	bb := in.BBox
	if bb.X1 <= bb.X0 || bb.Y1 <= bb.Y0 {
		return nil, measureRegionOutput{}, fmt.Errorf("bbox is degenerate (require x1>x0 and y1>y0)")
	}
	srcPath, err := resolveSourceImage(in.OutputDir)
	if err != nil {
		return nil, measureRegionOutput{}, err
	}
	img, err := imageproc.Load(srcPath)
	if err != nil {
		return nil, measureRegionOutput{}, fmt.Errorf("load source image: %w", err)
	}
	w, h := img.Bounds()
	// Clamp the rect to image bounds; reject if it falls entirely outside.
	x0, y0, x1, y1 := bb.X0, bb.Y0, bb.X1, bb.Y1
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
	if x1 <= x0 || y1 <= y0 {
		return nil, measureRegionOutput{}, fmt.Errorf("bbox is outside the image bounds (%dx%d)", w, h)
	}

	hex, r, g, b, coverage, pixels, ok := imageproc.SampleRegionCoverage(
		img.AsImage(), x0, y0, x1, y1, in.BorderPct, nearThreshDefault)
	if !ok {
		return nil, measureRegionOutput{}, fmt.Errorf("region could not be sampled (no opaque pixels)")
	}

	// Confidence is REAL and monotonic in coverage. It is clamped low for a
	// too-small sample so a degenerate rect cannot ground. It is NOT a fixed flag
	// and NOT a function of element size — a thin SOLID bar grounds; a thin MIXED
	// strip does not.
	confidence := coverage
	grounded := coverage >= groundedCoverageFloor && pixels >= minMeasurablePixels
	if pixels < minMeasurablePixels {
		// Too few pixels to trust: suppress confidence so it cannot ground.
		if confidence > 0.5 {
			confidence = 0.5
		}
	}

	out := measureRegionOutput{
		Hex:           hex,
		RGB:           [3]int{r, g, b},
		Coverage:      round3(coverage),
		Confidence:    round3(confidence),
		Grounded:      grounded,
		PixelsSampled: pixels,
		BBox:          bboxInput{X0: x0, Y0: y0, X1: x1, Y1: y1},
	}
	if !grounded {
		out.Note = "NOT grounded: coverage below floor or too few pixels — the rect is not a single measurable color (mixed/antialiased/gradient) or is degenerate. Treat as UNVERIFIABLE-by-measurement (route to human), do NOT assume the dominant color."
	}

	if in.ExpectHex != "" {
		tol := in.Tolerance
		if tol <= 0 {
			tol = 24
		}
		if ergb, ok := hexToRGB(in.ExpectHex); ok {
			dist := rgbDist([3]int{r, g, b}, ergb)
			out.Expect = &expectResult{
				Hex:             in.ExpectHex,
				Distance:        dist,
				WithinTolerance: dist <= tol,
			}
		}
	}
	return nil, out, nil
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
