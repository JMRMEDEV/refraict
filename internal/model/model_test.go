package model

import (
	"encoding/json"
	"testing"
)

func TestVisionResultArrayBBox(t *testing.T) {
	data := `{
		"role_guess": "header",
		"layout": {"type": "row", "columns": 3, "gap_px_approx": 8},
		"components": [
			{"id":"btn1","type":"button","bbox_global":[5,6,20,30],"confidence":0.9,"text":"Go","role":"primary_action"}
		],
		"description": "a header band",
		"confidence": 0.92
	}`
	var vr VisionResult
	if err := json.Unmarshal([]byte(data), &vr); err != nil {
		t.Fatalf("vision result unmarshal failed: %v", err)
	}
	if len(vr.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(vr.Components))
	}
	c := vr.Components[0]
	if c.BBoxGlobal.X0 != 5 || c.BBoxGlobal.Y0 != 6 || c.BBoxGlobal.X1 != 20 || c.BBoxGlobal.Y1 != 30 {
		t.Fatalf("array bbox not decoded: %+v", c.BBoxGlobal)
	}
	if c.Role != "primary_action" || c.Text != "Go" {
		t.Fatalf("component fields not decoded: %+v", c)
	}
	if vr.RoleGuess != "header" {
		t.Fatalf("role_guess not decoded: %q", vr.RoleGuess)
	}
}
