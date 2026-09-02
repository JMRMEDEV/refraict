package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ollama implements the Ollama local inference backend for both vision and
// text via the /api/generate endpoint. It keeps a persistent HTTP client so
// model weights remain warm between crops (no reconnect/restart overhead).
type Ollama struct {
	Endpoint string
	Model    string
	client   *http.Client
	// KeepAlive controls model unload time.
	KeepAlive string
	// StructuredOutput enables Ollama JSON-schema guided generation. Only set
	// true for backends known to support it (qwen2.5-coder, llama3.1+, etc.);
	// many VLM models (qwen3-vl, moondream) return empty with it enabled.
	StructuredOutput bool
	// CallTimeout bounds a single generate call. A runaway/looping generation
	// (observed on the graph/summary text calls) otherwise blocks the whole
	// per-image pipeline until the transport timeout. On timeout the call
	// returns an error and callers fall back to their deterministic path.
	// Zero disables the per-call bound (transport Timeout still applies).
	CallTimeout time.Duration
}

// NewOllama creates an Ollama backend. By default it frees the model
// immediately after each request (keep_alive=0) to minimize resident memory —
// important on constrained VRAM. Use NewOllamaKeepAlive to keep models warm for
// batch/agentic callers.
func NewOllama(endpoint, model string) *Ollama {
	return NewOllamaKeepAlive(endpoint, model, "0")
}

// NewOllamaKeepAlive creates an Ollama backend with an explicit keep_alive
// value (an Ollama duration string such as "0", "30s", "5m", or "-1" to keep
// loaded indefinitely). Empty is treated as "0" (free immediately).
func NewOllamaKeepAlive(endpoint, model, keepAlive string) *Ollama {
	if keepAlive == "" {
		keepAlive = "0"
	}
	return &Ollama{
		Endpoint:  endpoint,
		Model:     model,
		client:    &http.Client{Timeout: 10 * time.Minute},
		KeepAlive: keepAlive,
		// Default per-call bound: generous enough for legitimate crop/summary
		// generations on a modest local model, short enough that a runaway
		// generation degrades to the deterministic fallback in seconds.
		CallTimeout: 90 * time.Second,
	}
}

func (o *Ollama) baseURL() string {
	return o.Endpoint
}

// genRequest is the Ollama /api/generate request body.
type genRequest struct {
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Images    []string        `json:"images,omitempty"`
	Stream    bool            `json:"stream"`
	Format    json.RawMessage `json:"format,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Options   *genOptions     `json:"options,omitempty"`
}

// genOptions carries Ollama generation options. NumPredict caps the generated
// token count (the guard against runaway generations).
type genOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
}

type genResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

func (o *Ollama) generate(ctx context.Context, req genRequest) (string, error) {
	req.Model = o.Model
	req.Stream = false
	req.KeepAlive = o.KeepAlive
	// Bound a single call so a runaway/looping generation degrades to the
	// caller's deterministic fallback quickly rather than blocking the pipeline
	// until the transport timeout.
	if o.CallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.CallTimeout)
		defer cancel()
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL()+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, truncate(string(data), 400))
	}
	var gr genResponse
	if err := json.Unmarshal(data, &gr); err != nil {
		return "", fmt.Errorf("ollama response parse: %w (%s)", err, truncate(string(data), 300))
	}
	if gr.Error != "" {
		return "", fmt.Errorf("ollama model error: %s", gr.Error)
	}
	return gr.Response, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// schemaJSON returns a stable JSON schema for structured output if supported.
var visionSchema = json.RawMessage(`{
	"type":"object",
	"properties": {
		"role_guess":{"type":"string"},
		"layout":{"type":"object","properties":{"type":{"type":"string"},"columns":{"type":"integer"},"gap_px_approx":{"type":"integer"}}},
		"components":{"type":"array","items":{"type":"object","properties":{
			"id":{"type":"string"},"type":{"type":"string"},
			"bbox_global":{"type":"array","items":{"type":"integer"}},
			"confidence":{"type":"number"},"text":{"type":"string"},"role":{"type":"string"}
		}}},
		"description":{"type":"string"},
		"confidence":{"type":"number"}
	},
	"required":["role_guess","components","description","confidence"]
}`)

var _ VisionBackend = (*Ollama)(nil)

// Analyze sends a crop image to the VLM and parses structured + descriptive
// output.
func (o *Ollama) Analyze(ctx context.Context, req VisionRequest) (*VisionResult, error) {
	imgB64 := base64.StdEncoding.EncodeToString(req.ImageData)
	greq := genRequest{Prompt: req.Prompt, Images: []string{imgB64}}
	if o.StructuredOutput {
		greq.Format = visionSchema
	}
	out, err := o.generate(ctx, greq)
	if err != nil {
		return nil, err
	}
	res := &VisionResult{
		CropID:     req.CropID,
		BBoxGlobal: req.BBoxGlobal,
		RawOutput:  out,
	}
	// Grounded mode: the crop prompt asks for a short Markdown description, not
	// JSON. If the output parses as the legacy JSON schema, use it; otherwise
	// treat the text as the grounded description (the expected path for small
	// VLMs). Either way we get a usable, non-empty description.
	trimmed := strings.TrimSpace(out)
	looksJSON := strings.HasPrefix(trimmed, "{")
	if looksJSON && json.Unmarshal([]byte(out), res) == nil {
		// Legacy structured path succeeded.
	} else {
		res.Description = trimmed
		res.Confidence = descriptionConfidence(trimmed)
	}
	if res.CropID == "" {
		res.CropID = req.CropID
	}
	if res.BBoxGlobal.X0 == 0 && res.BBoxGlobal.Y0 == 0 {
		res.BBoxGlobal = req.BBoxGlobal
	}
	return res, nil
}

// descriptionConfidence assigns a coarse confidence to a grounded text
// description: empty -> 0, very short -> low, otherwise moderate. The pipeline
// treats vision descriptions as interpretation, so confidence stays <1.
func descriptionConfidence(s string) float64 {
	n := len(strings.Fields(s))
	switch {
	case n == 0:
		return 0
	case n < 5:
		return 0.3
	default:
		return 0.6
	}
}

var _ TextBackend = (*Ollama)(nil)

// Complete sends a text-only completion to the local text model.
func (o *Ollama) Complete(ctx context.Context, req TextRequest) (*TextResult, error) {
	greq := genRequest{Prompt: req.Prompt}
	if req.MaxTokens > 0 {
		greq.Options = &genOptions{NumPredict: req.MaxTokens}
	}
	out, err := o.generate(ctx, greq)
	if err != nil {
		return nil, err
	}
	return &TextResult{Output: out, Confidence: 1.0, RawOutput: out}, nil
}
