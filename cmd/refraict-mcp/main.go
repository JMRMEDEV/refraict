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
	"sort"

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
	// Derived-signal ROLL-UPS (counts, not per-item arrays) to keep the summary
	// lean — pull per-item detail on demand via the query tools:
	//   corner styles  -> get_components(has=corner_style)
	//   paddings       -> get_components(has=padding)
	//   font weights   -> get_components(type=text,has=weight) / query_text
	//   layout detail  -> get_container_children / layout_json
	CornerStyles     *cornerRollup      `json:"corner_styles,omitempty"`
	PaddingContainers int               `json:"padding_containers,omitempty"`
	TextTiers        []tierBandRef      `json:"text_tiers,omitempty"`
	TextWeights      *weightRollup      `json:"text_weights,omitempty"`
	LayoutContainers []layoutContainerRef `json:"layout_containers,omitempty"`
	EvenGroups       int                `json:"evenly_spaced_groups,omitempty"`
	Grounding       any               `json:"grounding,omitempty"`
	CrossCheck      any               `json:"crosscheck,omitempty"`
	ConsolidationOK any               `json:"consolidation_check,omitempty"`
	Note            string            `json:"note"`
}

// cornerRollup summarizes corner-style measurements (counts, not per-card list).
type cornerRollup struct {
	Rounded  int `json:"rounded"`
	Square   int `json:"square"`
	Measured int `json:"measured"`
}

// weightRollup summarizes font weight (counts + a few sample bold words, not the
// full per-token list). Full detail via get_components/query_text.
type weightRollup struct {
	Heavy       int      `json:"heavy"`
	Regular     int      `json:"regular"`
	HeavySample []string `json:"heavy_sample,omitempty"`
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
			"layout_json":         filepath.Join(outDir, "layout.json"),
			"dom_md":              filepath.Join(outDir, "dom.md"),
			"evidence_dir":        filepath.Join(outDir, "evidence"),
		},
		Note: "Summary is bounded — counts/rollups + decision signals only. For per-item detail use the SELECTIVE query tools (cheap, filtered): get_components (has=corner_style|padding|weight|tier, or type=icon/card/...), query_text (text+color+weight+tier), get_container_children. Use get_artifact only as a last resort (whole files, often 100s of KB). page.md = faithful assembled summary; page-consolidated.md = gemma narrative (see consolidation_check).",
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
			out.CornerStyles = cornerRollupOf(comps)
			out.PaddingContainers = countHas(comps, "padding")
			out.TextTiers = tierBandRefs(comps)
			out.TextWeights = weightRollupOf(comps)
		}
	}
	// Repeated-group count + evenly-spaced count from graph.json.
	if b, rerr := os.ReadFile(filepath.Join(outDir, "graph.json")); rerr == nil {
		var g map[string]any
		if json.Unmarshal(b, &g) == nil {
			if rg, ok := g["repeated_groups"].([]any); ok {
				out.RepeatedGroups = len(rg)
				out.EvenGroups = evenlySpacedCount(rg)
			}
		}
	}
	// Layout hierarchy (Milestone J): bounded rollup of inferred containers.
	if b, rerr := os.ReadFile(filepath.Join(outDir, "layout.json")); rerr == nil {
		var lt map[string]any
		if json.Unmarshal(b, &lt) == nil {
			out.LayoutContainers = layoutContainerRefs(lt)
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
	"layout_json":       "layout.json",
	"dom_md":            "dom.md",
	"grounding":         "evidence/grounding.json",
	"crosscheck":        "evidence/crosscheck.json",
	"merged_components": "evidence/merged_components.json",
	"colors":            "evidence/colors.json",
	"ocr":               "evidence/ocr.json",
}

type getArtifactInput struct {
	OutputDir string `json:"output_dir" jsonschema:"the output_dir returned by a prior analyze call"`
	Artifact  string `json:"artifact" jsonschema:"which artifact to read: page_json, page_md, page_consolidated, graph_json, layout_json, dom_md, grounding, crosscheck, merged_components, colors, ocr"`
}

type getArtifactOutput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func getArtifact(_ context.Context, _ *mcp.CallToolRequest, in getArtifactInput) (*mcp.CallToolResult, getArtifactOutput, error) {
	rel, ok := allowedArtifacts[in.Artifact]
	if !ok {
		return nil, getArtifactOutput{}, fmt.Errorf("unknown artifact %q; allowed: page_json, page_md, page_consolidated, graph_json, layout_json, dom_md, grounding, crosscheck, merged_components, colors, ocr", in.Artifact)
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

// cornerRollupOf counts corner-style measurements (rounded/square/total).
func cornerRollupOf(comps []any) *cornerRollup {
	r := &cornerRollup{}
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		cs, ok := m["corner_style"].(map[string]any)
		if !ok || cs == nil {
			continue
		}
		r.Measured++
		switch cs["style"] {
		case "rounded":
			r.Rounded++
		case "square":
			r.Square++
		}
	}
	if r.Measured == 0 {
		return nil
	}
	return r
}

// countHas counts components carrying a given attribute key.
func countHas(comps []any, key string) int {
	n := 0
	for _, c := range comps {
		if m, ok := c.(map[string]any); ok {
			if _, has := m[key]; has {
				n++
			}
		}
	}
	return n
}

// weightRollupOf counts font weights and keeps a few sample heavy words.
func weightRollupOf(comps []any) *weightRollup {
	r := &weightRollup{}
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		wm, ok := m["weight"].(map[string]any)
		if !ok || wm == nil {
			continue
		}
		switch wm["weight"] {
		case "heavy":
			r.Heavy++
			if len(r.HeavySample) < 5 {
				if t, ok := m["text"].(map[string]any); ok {
					if v, _ := t["value"].(string); v != "" {
						r.HeavySample = append(r.HeavySample, v)
					}
				}
			}
		case "regular":
			r.Regular++
		}
	}
	if r.Heavy == 0 && r.Regular == 0 {
		return nil
	}
	return r
}

// evenlySpacedCount counts repeated groups whose members are evenly spaced
// (gap_spread small relative to gap_median) — a decision signal without the
// per-group detail (pull graph.json for that).
func evenlySpacedCount(groups []any) int {
	n := 0
	for _, g := range groups {
		m, ok := g.(map[string]any)
		if !ok {
			continue
		}
		med, _ := m["gap_median"].(float64)
		spread, _ := m["gap_spread"].(float64)
		if med > 0 && spread/med < 0.25 {
			n++
		}
	}
	return n
}

// tierBandRef is a compact typography-hierarchy rollup (Milestone H): one entry
// per detected size band (heading/body/caption), with how many text components
// fall in it and the band's text-height range in px. Bounded — it does NOT list
// every text component (pull page.json for per-component `tier`). A size proxy,
// not font weight/family.
type tierBandRef struct {
	Tier      string `json:"tier"`
	Level     int    `json:"level"`
	Count     int    `json:"count"`
	MinHeight int    `json:"min_height_px"`
	MaxHeight int    `json:"max_height_px"`
}

// tierBandRefs rolls up per-component `tier` entries in page.json into one
// bounded entry per (tier, level) band, ordered largest band first.
func tierBandRefs(comps []any) []tierBandRef {
	type acc struct {
		tier          string
		level         int
		count         int
		minH, maxH    int
	}
	bands := map[int]*acc{}
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		tm, ok := m["tier"].(map[string]any)
		if !ok || tm == nil {
			continue
		}
		tier, _ := tm["tier"].(string)
		level := 0
		if f, ok := tm["level"].(float64); ok {
			level = int(f)
		}
		h := 0
		if f, ok := tm["height_px"].(float64); ok {
			h = int(f)
		}
		a := bands[level]
		if a == nil {
			a = &acc{tier: tier, level: level, minH: h, maxH: h}
			bands[level] = a
		}
		a.count++
		if h < a.minH {
			a.minH = h
		}
		if h > a.maxH {
			a.maxH = h
		}
	}
	if len(bands) == 0 {
		return nil
	}
	out := make([]tierBandRef, 0, len(bands))
	for _, a := range bands {
		out = append(out, tierBandRef{Tier: a.tier, Level: a.level, Count: a.count, MinHeight: a.minH, MaxHeight: a.maxH})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	return out
}

// layoutContainerRef is a bounded rollup of an INFERRED layout container
// (Milestone J): a column/row synthesized from aligned siblings, with its
// members count, header label (if any), and confidence. Bordered nesting +
// per-child shares live in the full layout.json (get_artifact 'layout_json').
type layoutContainerRef struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label,omitempty"`
	Members    int     `json:"members"`
	Confidence float64 `json:"confidence"`
}

// layoutContainerRefs walks the layout tree JSON and lists inferred containers.
func layoutContainerRefs(lt map[string]any) []layoutContainerRef {
	var out []layoutContainerRef
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if inf, _ := n["inferred"].(bool); inf {
			kind, _ := n["kind"].(string)
			label, _ := n["label"].(string)
			conf, _ := n["confidence"].(float64)
			members := 0
			if ch, ok := n["children"].([]any); ok {
				members = len(ch)
			}
			out = append(out, layoutContainerRef{Kind: kind, Label: label, Members: members, Confidence: conf})
		}
		if ch, ok := n["children"].([]any); ok {
			for _, c := range ch {
				if cm, ok := c.(map[string]any); ok {
					walk(cm)
				}
			}
		}
	}
	if roots, ok := lt["roots"].([]any); ok {
		for _, r := range roots {
			if rm, ok := r.(map[string]any); ok {
				walk(rm)
			}
		}
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
		Description: "Run the full refraict analysis pipeline on a UI screenshot. Returns a LEAN bounded summary: page type + confidence, component counts by type, roll-up COUNTS for the derived signals (corner_styles rounded/square/measured, padding_containers, text_tiers bands, text_weights heavy/regular + a few sample bold words, layout_containers inferred columns/rows, evenly_spaced_groups) and the grounding/crosscheck/consolidation scores — plus output_dir. For PER-ITEM detail use the selective query tools (get_components, query_text, get_container_children), not get_artifact. Requires OpenCV and (for semantic output) a local Ollama vision/text model.",
	}, analyze)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "inspect",
		Description: "Deterministic facts about an image (dimensions, SHA-256, format, dominant color). No models involved; fast.",
	}, inspect)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_artifact",
		Description: "Read back a named artifact (page_json, graph_json, page_md, etc.) from a prior analyze output_dir. Use this to pull full detail on demand instead of receiving it all up front. For most questions prefer the selective query tools (get_components, query_text, get_container_children) which return a small filtered slice instead of a whole artifact.",
	}, getArtifact)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_components",
		Description: "Selectively list components from a prior analyze output_dir, filtered server-side and projected to only the fields you ask for (default id,type,text,bbox) — returns a small slice, not the whole page.json. Filter by type (text/icon/card/...), has (weight/tier/semantic/corner_style/padding), text substring, tier (heading/body/caption), or region (top/bottom/left/right band). Bounded by limit/offset. Use this for 'which icons?', 'which bold texts?', 'list the cards', 'any non-rounded corners?'.",
	}, getComponents)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_text",
		Description: "List text components JOINED with their measured color, font weight, and typography tier in one compact row — server-side, so you avoid pulling and cross-referencing colors.json + page.json yourself. Filter by contains (substring), tier, weight (regular/heavy), or near_color (hex + tolerance). Use this for 'which texts are blueish?', 'what are the bold texts?', 'headings and their colors'.",
	}, queryText)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_container_children",
		Description: "Return the direct children (with measured occupancy share %) of a layout container from a prior analyze output_dir, by container_id or by inferred-container label (e.g. 'TO DO (4)'). Use this for 'what's inside the sidebar/card X?', 'what's in the TO DO column?', 'how is this container's space divided?'.",
	}, getContainerChildren)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "refraict-mcp:", err)
		os.Exit(1)
	}
}

func version() string { return "0.1.0" }
