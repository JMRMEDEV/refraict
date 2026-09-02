package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// TestCallTimeoutDegrades verifies a runaway (slow) generation is bounded by
// CallTimeout so callers can fall back to their deterministic path instead of
// blocking until the transport timeout.
func TestCallTimeoutDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a generation that never returns in time.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte(`{"response":"too late","done":true}`))
		}
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test")
	o.CallTimeout = 50 * time.Millisecond
	start := time.Now()
	_, err := o.Complete(context.Background(), TextRequest{Prompt: "x"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("call did not honor CallTimeout (took %s)", time.Since(start))
	}
}

// TestMaxTokensSentAsNumPredict verifies TextRequest.MaxTokens is forwarded as
// Ollama options.num_predict (the runaway-generation guard).
func TestMaxTokensSentAsNumPredict(t *testing.T) {
	var gotNumPredict int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var gr genRequest
		_ = json.Unmarshal(body, &gr)
		if gr.Options != nil {
			gotNumPredict = gr.Options.NumPredict
		}
		_, _ = w.Write([]byte(`{"response":"ok","done":true}`))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test")
	if _, err := o.Complete(context.Background(), TextRequest{Prompt: "x", MaxTokens: 321}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if gotNumPredict != 321 {
		t.Fatalf("expected num_predict=321, got %d", gotNumPredict)
	}
}
