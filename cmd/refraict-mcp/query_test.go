package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func sz(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// TestQueryToolsSavings exercises the query tools against a real output dir and
// prints payload sizes vs the full artifacts (run: go test -run Savings -v).
func TestQueryToolsSavings(t *testing.T) {
	dir := os.Getenv("QOUT")
	if dir == "" {
		dir = "/tmp/qout"
	}
	if _, err := os.Stat(dir + "/page.json"); err != nil {
		t.Skip("no output dir at " + dir)
	}
	ctx := context.Background()

	// Full artifacts for comparison.
	pj, _ := os.ReadFile(dir + "/page.json")
	cj, _ := os.ReadFile(dir + "/evidence/colors.json")
	mc, _ := os.ReadFile(dir + "/evidence/merged_components.json")
	t.Logf("FULL page.json=%dB colors.json=%dB merged_components.json=%dB", len(pj), len(cj), len(mc))

	// get_components: icons
	_, ic, err := getComponents(ctx, nil, getComponentsInput{OutputDir: dir, Type: "icon", Fields: []string{"id", "type", "semantic", "bbox"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("get_components(icon) -> %d items, %dB", ic.Returned, sz(ic))

	// get_components: bold text
	_, bt, _ := getComponents(ctx, nil, getComponentsInput{OutputDir: dir, Type: "text", Has: "weight", Fields: []string{"id", "text", "weight"}})
	t.Logf("get_components(text,has=weight) -> %d items, %dB", bt.Returned, sz(bt))

	// query_text: headings with color
	_, qh, _ := queryText(ctx, nil, queryTextInput{OutputDir: dir, Tier: "heading"})
	t.Logf("query_text(tier=heading) -> %d items, %dB", qh.Returned, sz(qh))

	// query_text: near a teal color
	_, qc, _ := queryText(ctx, nil, queryTextInput{OutputDir: dir, NearColor: "#275152", ColorTol: 70})
	t.Logf("query_text(near #275152) -> %d items, %dB", qc.Returned, sz(qc))

	// get_container_children by label
	_, cc, err := getContainerChildren(ctx, nil, getContainerChildrenInput{OutputDir: dir, Label: "TO DO"})
	if err != nil {
		t.Logf("get_container_children(TO DO): %v", err)
	} else {
		t.Logf("get_container_children(TO DO) -> %d children, %dB, axis=%s", len(cc.Children), sz(cc), cc.Axis)
	}
}
