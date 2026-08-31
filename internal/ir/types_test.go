package ir

import (
	"encoding/json"
	"testing"
)

func TestBoundingBoxUnmarshalArray(t *testing.T) {
	var b BoundingBox
	if err := json.Unmarshal([]byte(`[10,20,30,40]`), &b); err != nil {
		t.Fatalf("array form failed: %v", err)
	}
	if b.X0 != 10 || b.Y0 != 20 || b.X1 != 30 || b.Y1 != 40 {
		t.Fatalf("unexpected array decode: %+v", b)
	}
}

func TestBoundingBoxUnmarshalObject(t *testing.T) {
	var b BoundingBox
	if err := json.Unmarshal([]byte(`{"x0":1,"y0":2,"x1":3,"y1":4}`), &b); err != nil {
		t.Fatalf("object form failed: %v", err)
	}
	if b.X0 != 1 || b.Y0 != 2 || b.X1 != 3 || b.Y1 != 4 {
		t.Fatalf("unexpected object decode: %+v", b)
	}
}
