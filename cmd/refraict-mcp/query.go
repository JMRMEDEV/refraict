package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Selective query tools (bounded, server-side filtered/projected) so an agent
// pulls only the slice it needs instead of whole artifacts. All read a prior
// analyze output_dir. Three lean, composable verbs cover the common questions:
//   - get_components: filter components by type/has/text/tier/region + project fields.
//   - query_text: text components JOINED with measured color + weight + tier.
//   - get_container_children: direct children (with occupancy shares) of a container.

const defaultQueryLimit = 40

// ---- shared page.json loading ----

func loadComponents(outputDir string) ([]map[string]any, error) {
	if outputDir == "" {
		return nil, fmt.Errorf("output_dir is required")
	}
	full := filepath.Join(outputDir, "page.json")
	if !within(outputDir, full) {
		return nil, fmt.Errorf("resolved path escapes output_dir")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read page.json: %w", err)
	}
	var page map[string]any
	if err := json.Unmarshal(b, &page); err != nil {
		return nil, fmt.Errorf("parse page.json: %w", err)
	}
	raw, _ := page["components"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func compType(c map[string]any) string {
	if t, ok := c["type"].(map[string]any); ok {
		s, _ := t["value"].(string)
		return s
	}
	return ""
}
func compText(c map[string]any) string {
	if t, ok := c["text"].(map[string]any); ok {
		s, _ := t["value"].(string)
		return s
	}
	return ""
}
func compBBox(c map[string]any) (x0, y0, x1, y1 int, ok bool) {
	b, o := c["bbox"].(map[string]any)
	if !o {
		return 0, 0, 0, 0, false
	}
	gi := func(k string) int {
		if f, ok := b[k].(float64); ok {
			return int(f)
		}
		return 0
	}
	return gi("x0"), gi("y0"), gi("x1"), gi("y1"), true
}

// ---- get_components ----

type getComponentsInput struct {
	OutputDir string   `json:"output_dir" jsonschema:"the output_dir returned by a prior analyze call"`
	Type      string   `json:"type,omitempty" jsonschema:"filter by component type: text, icon, logo, card, panel, region, chart, image"`
	Has       string   `json:"has,omitempty" jsonschema:"keep only components carrying this attribute: weight, tier, semantic, corner_style, padding"`
	Text      string   `json:"text,omitempty" jsonschema:"case-insensitive substring the component text must contain"`
	Tier      string   `json:"tier,omitempty" jsonschema:"filter text components by typography tier: heading, body, caption"`
	Region    string   `json:"region,omitempty" jsonschema:"spatial band: top, bottom, left, right (top/bottom = outer third vertically; left/right = outer third horizontally)"`
	Fields    []string `json:"fields,omitempty" jsonschema:"project only these fields (default id,type,text,bbox). Options: id,type,text,bbox,weight,tier,semantic,corner_style,padding,confidence"`
	Limit     int      `json:"limit,omitempty" jsonschema:"max results (default 40)"`
	Offset    int      `json:"offset,omitempty" jsonschema:"skip this many results (pagination)"`
}

type getComponentsOutput struct {
	Total    int              `json:"total_matched"`
	Returned int              `json:"returned"`
	Offset   int              `json:"offset"`
	Items    []map[string]any `json:"items"`
}

func getComponents(_ context.Context, _ *mcp.CallToolRequest, in getComponentsInput) (*mcp.CallToolResult, getComponentsOutput, error) {
	comps, err := loadComponents(in.OutputDir)
	if err != nil {
		return nil, getComponentsOutput{}, err
	}
	// page dimensions for region bands
	pw, ph := pageDims(in.OutputDir)
	var matched []map[string]any
	for _, c := range comps {
		if in.Type != "" && compType(c) != in.Type {
			continue
		}
		if in.Has != "" {
			if _, ok := c[in.Has]; !ok {
				continue
			}
		}
		if in.Tier != "" {
			tm, ok := c["tier"].(map[string]any)
			if !ok || tm["tier"] != in.Tier {
				continue
			}
		}
		if in.Text != "" && !strings.Contains(strings.ToLower(compText(c)), strings.ToLower(in.Text)) {
			continue
		}
		if in.Region != "" && !inRegion(c, in.Region, pw, ph) {
			continue
		}
		matched = append(matched, c)
	}
	total := len(matched)
	off := clampOffset(in.Offset, total)
	lim := in.Limit
	if lim <= 0 {
		lim = defaultQueryLimit
	}
	end := off + lim
	if end > total {
		end = total
	}
	fields := in.Fields
	if len(fields) == 0 {
		fields = []string{"id", "type", "text", "bbox"}
	}
	items := make([]map[string]any, 0, end-off)
	for _, c := range matched[off:end] {
		items = append(items, project(c, fields))
	}
	return nil, getComponentsOutput{Total: total, Returned: len(items), Offset: off, Items: items}, nil
}

// project returns only the requested fields, flattening type/text to their value.
func project(c map[string]any, fields []string) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		switch f {
		case "id":
			out["id"] = c["id"]
		case "type":
			out["type"] = compType(c)
		case "text":
			if t := compText(c); t != "" {
				out["text"] = t
			}
		case "bbox":
			out["bbox"] = c["bbox"]
		case "confidence":
			out["confidence"] = c["confidence"]
		case "weight", "tier", "semantic", "corner_style", "padding":
			if v, ok := c[f]; ok {
				out[f] = v
			}
		}
	}
	return out
}

// ---- query_text ----

type queryTextInput struct {
	OutputDir string `json:"output_dir" jsonschema:"the output_dir returned by a prior analyze call"`
	Contains  string `json:"contains,omitempty" jsonschema:"case-insensitive substring filter on the text"`
	Tier      string `json:"tier,omitempty" jsonschema:"filter by typography tier: heading, body, caption"`
	Weight    string `json:"weight,omitempty" jsonschema:"filter by font weight: regular, heavy"`
	NearColor string `json:"near_color,omitempty" jsonschema:"keep only text whose measured color is within tolerance of this hex (e.g. #275152)"`
	ColorTol  int    `json:"color_tol,omitempty" jsonschema:"RGB distance tolerance for near_color (default 60)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max results (default 40)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"skip this many results (pagination)"`
}

type textRow struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Tier   string `json:"tier,omitempty"`
	Weight string `json:"weight,omitempty"`
	Color  string `json:"color,omitempty"`
	BBox   any    `json:"bbox"`
}

type queryTextOutput struct {
	Total    int       `json:"total_matched"`
	Returned int       `json:"returned"`
	Offset   int       `json:"offset"`
	Items    []textRow `json:"items"`
}

func queryText(_ context.Context, _ *mcp.CallToolRequest, in queryTextInput) (*mcp.CallToolResult, queryTextOutput, error) {
	comps, err := loadComponents(in.OutputDir)
	if err != nil {
		return nil, queryTextOutput{}, err
	}
	colors := loadColors(in.OutputDir)
	tol := in.ColorTol
	if tol <= 0 {
		tol = 60
	}
	var target [3]int
	haveTarget := false
	if in.NearColor != "" {
		if rgb, ok := hexToRGB(in.NearColor); ok {
			target, haveTarget = rgb, true
		}
	}

	var matched []textRow
	for _, c := range comps {
		if compType(c) != "text" {
			continue
		}
		txt := compText(c)
		if txt == "" {
			continue
		}
		if in.Contains != "" && !strings.Contains(strings.ToLower(txt), strings.ToLower(in.Contains)) {
			continue
		}
		tier := ""
		if tm, ok := c["tier"].(map[string]any); ok {
			tier, _ = tm["tier"].(string)
		}
		if in.Tier != "" && tier != in.Tier {
			continue
		}
		weight := ""
		if wm, ok := c["weight"].(map[string]any); ok {
			weight, _ = wm["weight"].(string)
		}
		if in.Weight != "" && weight != in.Weight {
			continue
		}
		colorHex, colorRGB := nearestColor(c, colors)
		if haveTarget {
			if colorRGB == nil || rgbDist(*colorRGB, target) > tol {
				continue
			}
		}
		matched = append(matched, textRow{
			ID: getString(c, "id"), Text: txt, Tier: tier, Weight: weight, Color: colorHex, BBox: c["bbox"],
		})
	}
	total := len(matched)
	off := clampOffset(in.Offset, total)
	lim := in.Limit
	if lim <= 0 {
		lim = defaultQueryLimit
	}
	end := off + lim
	if end > total {
		end = total
	}
	return nil, queryTextOutput{Total: total, Returned: end - off, Offset: off, Items: matched[off:end]}, nil
}

// ---- get_container_children ----

type getContainerChildrenInput struct {
	OutputDir   string `json:"output_dir" jsonschema:"the output_dir returned by a prior analyze call"`
	ContainerID string `json:"container_id,omitempty" jsonschema:"component ID of a container (from layout.json / get_components)"`
	Label       string `json:"label,omitempty" jsonschema:"header label of an INFERRED container, e.g. 'TO DO (4)' (substring match)"`
}

type childRow struct {
	ComponentID string  `json:"component_id,omitempty"`
	Inferred    bool    `json:"inferred,omitempty"`
	Kind        string  `json:"kind,omitempty"`
	Text        string  `json:"text,omitempty"`
	Type        string  `json:"type,omitempty"`
	Share       float64 `json:"share_pct,omitempty"`
	BBox        any     `json:"bbox"`
}

type getContainerChildrenOutput struct {
	Container string     `json:"container"`
	Kind      string     `json:"kind,omitempty"`
	Label     string     `json:"label,omitempty"`
	Axis      string     `json:"axis,omitempty"`
	Children  []childRow `json:"children"`
}

func getContainerChildren(_ context.Context, _ *mcp.CallToolRequest, in getContainerChildrenInput) (*mcp.CallToolResult, getContainerChildrenOutput, error) {
	if in.OutputDir == "" {
		return nil, getContainerChildrenOutput{}, fmt.Errorf("output_dir is required")
	}
	full := filepath.Join(in.OutputDir, "layout.json")
	if !within(in.OutputDir, full) {
		return nil, getContainerChildrenOutput{}, fmt.Errorf("resolved path escapes output_dir")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, getContainerChildrenOutput{}, fmt.Errorf("read layout.json: %w", err)
	}
	var lt map[string]any
	if err := json.Unmarshal(b, &lt); err != nil {
		return nil, getContainerChildrenOutput{}, fmt.Errorf("parse layout.json: %w", err)
	}
	// text lookup for labeling children.
	texts := map[string]string{}
	types := map[string]string{}
	if comps, e := loadComponents(in.OutputDir); e == nil {
		for _, c := range comps {
			id := getString(c, "id")
			texts[id] = compText(c)
			types[id] = compType(c)
		}
	}

	var found map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if found != nil {
			return
		}
		if in.ContainerID != "" && n["component_id"] == in.ContainerID {
			found = n
			return
		}
		if in.Label != "" {
			if lbl, _ := n["label"].(string); lbl != "" && strings.Contains(strings.ToLower(lbl), strings.ToLower(in.Label)) {
				found = n
				return
			}
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
	if found == nil {
		return nil, getContainerChildrenOutput{}, fmt.Errorf("container not found (by container_id or label)")
	}
	out := getContainerChildrenOutput{
		Container: getString(found, "component_id"),
		Kind:      getString(found, "kind"),
		Label:     getString(found, "label"),
	}
	if out.Container == "" && found["inferred"] == true {
		out.Container = "(inferred " + out.Kind + ")"
	}
	if ch, ok := found["children"].([]any); ok {
		for _, c := range ch {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			cid := getString(cm, "component_id")
			row := childRow{
				ComponentID: cid,
				Text:        texts[cid],
				Type:        types[cid],
				BBox:        cm["bbox"],
			}
			if cm["inferred"] == true {
				row.Inferred, row.Kind, row.Type = true, getString(cm, "kind"), ""
			}
			if sh, ok := cm["share"].(map[string]any); ok {
				if f, ok := sh["fraction"].(float64); ok {
					row.Share = math.Round(f*1000) / 10 // percent, 1 decimal
				}
				if out.Axis == "" {
					out.Axis, _ = sh["axis"].(string)
				}
			}
			out.Children = append(out.Children, row)
		}
	}
	return nil, out, nil
}

// ---- color helpers ----

type colorSample struct {
	hex  string
	rgb  [3]int
	bbox [4]int
}

func loadColors(outputDir string) []colorSample {
	full := filepath.Join(outputDir, "evidence", "colors.json")
	if !within(outputDir, full) {
		return nil
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	var raw []map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	out := make([]colorSample, 0, len(raw))
	for _, m := range raw {
		cs := colorSample{}
		cs.hex, _ = m["value"].(string)
		if arr, ok := m["rgb"].([]any); ok && len(arr) == 3 {
			for i := 0; i < 3; i++ {
				if f, ok := arr[i].(float64); ok {
					cs.rgb[i] = int(f)
				}
			}
		}
		if bb, ok := m["bbox_global"].(map[string]any); ok {
			keys := []string{"x0", "y0", "x1", "y1"}
			for i, k := range keys {
				if f, ok := bb[k].(float64); ok {
					cs.bbox[i] = int(f)
				}
			}
		}
		out = append(out, cs)
	}
	return out
}

// nearestColor returns the measured color sample whose bbox best matches the
// component's bbox (highest IoU / containment), so text maps to its own color.
func nearestColor(c map[string]any, colors []colorSample) (string, *[3]int) {
	x0, y0, x1, y1, ok := compBBox(c)
	if !ok || len(colors) == 0 {
		return "", nil
	}
	best := -1
	bestScore := -1.0
	cb := [4]int{x0, y0, x1, y1}
	for i, s := range colors {
		score := iou(cb, s.bbox)
		if score > bestScore {
			bestScore, best = score, i
		}
	}
	if best < 0 || bestScore <= 0 {
		return "", nil
	}
	rgb := colors[best].rgb
	return colors[best].hex, &rgb
}

func iou(a, b [4]int) float64 {
	ix0, iy0 := maxi(a[0], b[0]), maxi(a[1], b[1])
	ix1, iy1 := mini(a[2], b[2]), mini(a[3], b[3])
	if ix1 <= ix0 || iy1 <= iy0 {
		return 0
	}
	inter := float64((ix1 - ix0) * (iy1 - iy0))
	areaA := float64((a[2] - a[0]) * (a[3] - a[1]))
	areaB := float64((b[2] - b[0]) * (b[3] - b[1]))
	if areaA+areaB-inter <= 0 {
		return 0
	}
	return inter / (areaA + areaB - inter)
}

func hexToRGB(h string) ([3]int, bool) {
	h = strings.TrimPrefix(strings.TrimSpace(h), "#")
	if len(h) != 6 {
		return [3]int{}, false
	}
	var rgb [3]int
	for i := 0; i < 3; i++ {
		var v int
		if _, err := fmt.Sscanf(h[i*2:i*2+2], "%02x", &v); err != nil {
			return [3]int{}, false
		}
		rgb[i] = v
	}
	return rgb, true
}

func rgbDist(a, b [3]int) int {
	d := 0
	for i := 0; i < 3; i++ {
		diff := a[i] - b[i]
		d += diff * diff
	}
	return int(math.Sqrt(float64(d)))
}

// ---- region + misc helpers ----

func pageDims(outputDir string) (int, int) {
	b, err := os.ReadFile(filepath.Join(outputDir, "page.json"))
	if err != nil {
		return 0, 0
	}
	var p map[string]any
	if json.Unmarshal(b, &p) != nil {
		return 0, 0
	}
	return toInt(p["width"]), toInt(p["height"])
}

func inRegion(c map[string]any, region string, pw, ph int) bool {
	x0, y0, x1, y1, ok := compBBox(c)
	if !ok {
		return false
	}
	cx, cy := (x0+x1)/2, (y0+y1)/2
	switch region {
	case "top":
		return ph > 0 && cy < ph/3
	case "bottom":
		return ph > 0 && cy > ph*2/3
	case "left":
		return pw > 0 && cx < pw/3
	case "right":
		return pw > 0 && cx > pw*2/3
	}
	return true
}

func getString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func clampOffset(off, total int) int {
	if off < 0 {
		return 0
	}
	if off > total {
		return total
	}
	return off
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
