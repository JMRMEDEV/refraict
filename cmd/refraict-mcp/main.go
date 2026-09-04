// Command refraict-mcp is an MCP (Model Context Protocol) server that exposes
// refraict's screenshot-analysis pipeline to AI agents over stdio. It runs the
// same in-process pipeline as the `refraict analyze` CLI and returns a BOUNDED
// summary plus pointers to the on-disk artifacts (page.json, page.md,
// graph.json, evidence/...) — never dumping the large full JSON into the agent's
// context. The agent pulls specific artifacts on demand via get_artifact.
//
// Requires OpenCV 4.x at runtime (same as the refraict binary). Tools:
//   - analyze:      run the full pipeline on an image; return summary + paths.
//   - inspect:      deterministic facts (dimensions, colors, hash), no models.
//   - get_artifact: read back a named artifact from a prior analyze output dir.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/refraict/refraict/internal/cli"
	"github.com/refraict/refraict/internal/imageproc"
)

// ---- analyze ----

type analyzeInput struct {
	ImagePath  string `json:"image_path" jsonschema:"absolute or relative path to the screenshot image (PNG/JPEG)"`
	ConfigPath string `json:"config_path,omitempty" jsonschema:"optional path to a refraict JSON config; empty uses defaults"`
	OutputDir  string `json:"output_dir,omitempty" jsonschema:"optional output directory; empty uses ./analysis-<basename>"`
}

// analyzeOutput is the BOUNDED summary returned to the agent. The heavy data
// (full component list, colors, relationships) stays on disk; the agent reads it
// via get_artifact only when needed. The decision signals (page type, grounding,
// crosscheck) are surfaced so the agent can judge whether to trust the summary.
type analyzeOutput struct {
	OutputDir       string            `json:"output_dir"`
	Artifacts       map[string]string `json:"artifacts"`
	Width           int               `json:"width"`
	Height          int               `json:"height"`
	PageType        any               `json:"page_type,omitempty"`
	ComponentCount  int               `json:"component_count"`
	Counts          map[string]int    `json:"component_counts_by_type"`
	RepeatedGroups  int               `json:"repeated_group_count"`
	CornerStyles    []cornerStyleRef  `json:"corner_styles,omitempty"`
	Paddings        []paddingRef      `json:"paddings,omitempty"`
	GroupSpacing    []spacingRef      `json:"group_spacing,omitempty"`
	Grounding       any               `json:"grounding,omitempty"`
	CrossCheck      any               `json:"crosscheck,omitempty"`
	ConsolidationOK any               `json:"consolidation_check,omitempty"`
	Note            string            `json:"note"`
}

func analyze(ctx context.Context, _ *mcp.CallToolRequest, in analyzeInput) (*mcp.CallToolResult, analyzeOutput, error) {
	if in.ImagePath == "" {
		return nil, analyzeOutput{}, fmt.Errorf("image_path is required")
	}
	outDir, err := cli.Analyze(ctx, cli.AnalyzeRequest{
		ImagePath:  in.ImagePath,
		ConfigPath: in.ConfigPath,
		OutputDir:  in.OutputDir,
	})
	if err != nil {
		return nil, analyzeOutput{}, fmt.Errorf("analyze failed: %w", err)
	}

	out := analyzeOutput{
		OutputDir: outDir,
		Artifacts: map[string]string{
			"page_json":           filepath.Join(outDir, "page.json"),
			"page_md":             filepath.Join(outDir, "page.md"),
			"page_consolidated":   filepath.Join(outDir, "page-consolidated.md"),
			"graph_json":          filepath.Join(outDir, "graph.json"),
			"dom_md":              filepath.Join(outDir, "dom.md"),
			"evidence_dir":        filepath.Join(outDir, "evidence"),
		},
		Note: "Summary is bounded; call get_artifact with 'page_json' or 'graph_json' for full detail. page.md is the faithful assembled summary; page-consolidated.md is the gemma-consolidated narrative (see consolidation_check for its grounding).",
	}

	// Read the flagship page.json to populate the bounded summary.
	var page map[string]any
	if b, rerr := os.ReadFile(filepath.Join(outDir, "page.json")); rerr == nil {
		_ = json.Unmarshal(b, &page)
	}
	if page != nil {
		out.Width = toInt(page["width"])
		out.Height = toInt(page["height"])
		out.PageType = page["page_type"]
		out.Grounding = page["grounding"]
		out.CrossCheck = page["crosscheck"]
		out.ConsolidationOK = page["consolidation_check"]
		if comps, ok := page["components"].([]any); ok {
			out.ComponentCount = len(comps)
			out.Counts = countByType(comps)
			out.CornerStyles = cornerStyleRefs(comps)
			out.Paddings = paddingRefs(comps)
		}
	}
	// Repeated-group count from graph.json.
	if b, rerr := os.ReadFile(filepath.Join(outDir, "graph.json")); rerr == nil {
		var g map[string]any
		if json.Unmarshal(b, &g) == nil {
			if rg, ok := g["repeated_groups"].([]any); ok {
				out.RepeatedGroups = len(rg)
				out.GroupSpacing = spacingRefs(rg)
			}
		}
	}
	return nil, out, nil
}

// ---- inspect ----

type inspectInput struct {
	ImagePath string `json:"image_path" jsonschema:"path to the image to inspect (deterministic facts only, no models)"`
}

type inspectOutput struct {
	Path          string `json:"path"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Sha256        string `json:"sha256"`
	Format        string `json:"format"`
	DominantColor string `json:"dominant_color"`
}

func inspect(_ context.Context, _ *mcp.CallToolRequest, in inspectInput) (*mcp.CallToolResult, inspectOutput, error) {
	if in.ImagePath == "" {
		return nil, inspectOutput{}, fmt.Errorf("image_path is required")
	}
	img, err := imageproc.Load(in.ImagePath)
	if err != nil {
		return nil, inspectOutput{}, fmt.Errorf("load image: %w", err)
	}
	w, h := img.Bounds()
	out := inspectOutput{
		Path:   in.ImagePath,
		Width:  w,
		Height: h,
		Sha256: img.Sha256,
		Format: img.DetectFormat(),
	}
	if hexc, _, _, _, ok := imageproc.SampleRegion(img.AsImage(), 0, 0, w, h, 0.0); ok {
		out.DominantColor = hexc
	}
	return nil, out, nil
}

// ---- get_artifact ----

var allowedArtifacts = map[string]string{
	"page_json":         "page.json",
	"page_md":           "page.md",
	"page_consolidated": "page-consolidated.md",
	"graph_json":        "graph.json",
	"dom_md":            "dom.md",
	"grounding":         "evidence/grounding.json",
	"crosscheck":        "evidence/crosscheck.json",
	"merged_components": "evidence/merged_components.json",
	"colors":            "evidence/colors.json",
	"ocr":               "evidence/ocr.json",
}

type getArtifactInput struct {
	OutputDir string `json:"output_dir" jsonschema:"the output_dir returned by a prior analyze call"`
	Artifact  string `json:"artifact" jsonschema:"which artifact to read: page_json, page_md, page_consolidated, graph_json, dom_md, grounding, crosscheck, merged_components, colors, ocr"`
}

type getArtifactOutput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func getArtifact(_ context.Context, _ *mcp.CallToolRequest, in getArtifactInput) (*mcp.CallToolResult, getArtifactOutput, error) {
	rel, ok := allowedArtifacts[in.Artifact]
	if !ok {
		return nil, getArtifactOutput{}, fmt.Errorf("unknown artifact %q; allowed: page_json, page_md, page_consolidated, graph_json, dom_md, grounding, crosscheck, merged_components, colors, ocr", in.Artifact)
	}
	if in.OutputDir == "" {
		return nil, getArtifactOutput{}, fmt.Errorf("output_dir is required")
	}
	// Guard against path escape: the artifact is a fixed relative name joined to
	// the caller-provided output dir; reject if the cleaned path escapes it.
	full := filepath.Join(in.OutputDir, rel)
	if !within(in.OutputDir, full) {
		return nil, getArtifactOutput{}, fmt.Errorf("resolved path escapes output_dir")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, getArtifactOutput{}, fmt.Errorf("read artifact: %w", err)
	}
	return nil, getArtifactOutput{Path: full, Content: string(b)}, nil
}

// ---- helpers ----

func toInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// cornerStyleRef is a compact per-component corner-style entry surfaced in the
// analyze summary so an agent can settle a "rounded vs square" dispute without
// pulling the full page.json.
type cornerStyleRef struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Style      string  `json:"style"`
	Confidence float64 `json:"confidence"`
}

// cornerStyleRefs extracts the compact corner-style rollup from page.json
// components (only those that carry a corner_style).
func cornerStyleRefs(comps []any) []cornerStyleRef {
	var out []cornerStyleRef
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		cs, ok := m["corner_style"].(map[string]any)
		if !ok || cs == nil {
			continue
		}
		id, _ := m["id"].(string)
		typ := ""
		if t, ok := m["type"].(map[string]any); ok {
			typ, _ = t["value"].(string)
		}
		style, _ := cs["style"].(string)
		conf, _ := cs["confidence"].(float64)
		out = append(out, cornerStyleRef{ID: id, Type: typ, Style: style, Confidence: conf})
	}
	return out
}

// paddingRef is a compact container-padding entry (Milestone G) for the analyze
// summary. content_fills=false means right/bottom are leftover slack, not real
// padding.
type paddingRef struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Left         int    `json:"left"`
	Right        int    `json:"right"`
	Top          int    `json:"top"`
	Bottom       int    `json:"bottom"`
	ContentFills bool   `json:"content_fills"`
}

func paddingRefs(comps []any) []paddingRef {
	var out []paddingRef
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		pm, ok := m["padding"].(map[string]any)
		if !ok || pm == nil {
			continue
		}
		id, _ := m["id"].(string)
		typ := ""
		if t, ok := m["type"].(map[string]any); ok {
			typ, _ = t["value"].(string)
		}
		gi := func(k string) int {
			if f, ok := pm[k].(float64); ok {
				return int(f)
			}
			return 0
		}
		cf, _ := pm["content_fills"].(bool)
		out = append(out, paddingRef{ID: id, Type: typ, Left: gi("left"), Right: gi("right"), Top: gi("top"), Bottom: gi("bottom"), ContentFills: cf})
	}
	return out
}

// spacingRef is a compact repeated-group spacing entry (Milestone G): the gap
// median + spread between adjacent siblings (spread ~0 = evenly spaced).
type spacingRef struct {
	Type      string `json:"type"`
	Axis      string `json:"axis"`
	Members   int    `json:"members"`
	Header    string `json:"header,omitempty"`
	GapMedian int    `json:"gap_median"`
	GapSpread int    `json:"gap_spread"`
}

func spacingRefs(groups []any) []spacingRef {
	var out []spacingRef
	gi := func(m map[string]any, k string) int {
		if f, ok := m[k].(float64); ok {
			return int(f)
		}
		return 0
	}
	for _, g := range groups {
		m, ok := g.(map[string]any)
		if !ok {
			continue
		}
		mem, _ := m["member_ids"].([]any)
		if len(mem) < 2 {
			continue
		}
		typ, _ := m["type"].(string)
		axis, _ := m["axis"].(string)
		hdr, _ := m["header"].(string)
		out = append(out, spacingRef{Type: typ, Axis: axis, Members: len(mem), Header: hdr, GapMedian: gi(m, "gap_median"), GapSpread: gi(m, "gap_spread")})
	}
	return out
}

func countByType(comps []any) map[string]int {
	out := map[string]int{}
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(map[string]any)
		if t == nil {
			continue
		}
		if v, ok := t["value"].(string); ok {
			out[v]++
		}
	}
	return out
}

func within(dir, path string) bool {
	ad, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	ap, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(ad, ap)
	if err != nil {
		return false
	}
	return rel != ".." && !hasDotDotPrefix(rel)
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 3 && rel[0] == '.' && rel[1] == '.' && (rel[2] == '/' || rel[2] == '\\')
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "refraict",
		Version: version(),
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze",
		Description: "Run the full refraict analysis pipeline on a UI screenshot. Returns a bounded summary (page type + confidence, component counts, corner_styles rounded/square per card, container paddings, repeated-group spacing gaps, grounding + crosscheck scores) and paths to on-disk artifacts. Requires OpenCV and (for semantic output) a local Ollama vision/text model.",
	}, analyze)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "inspect",
		Description: "Deterministic facts about an image (dimensions, SHA-256, format, dominant color). No models involved; fast.",
	}, inspect)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_artifact",
		Description: "Read back a named artifact (page_json, graph_json, page_md, etc.) from a prior analyze output_dir. Use this to pull full detail on demand instead of receiving it all up front.",
	}, getArtifact)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "refraict-mcp:", err)
		os.Exit(1)
	}
}

func version() string { return "0.1.0" }
