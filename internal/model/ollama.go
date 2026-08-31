package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

// NewOllama creates an Ollama backend.
func NewOllama(endpoint, model string) *Ollama {
	return &Ollama{
		Endpoint:  endpoint,
		Model:     model,
		client:    &http.Client{Timeout: 10 * time.Minute},
		KeepAlive: "5m",
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
	if err := json.Unmarshal([]byte(out), res); err != nil {
		// Schema non-compliant -> attempt a one-time schema-repair retry.
		repair := out + "\n\nThat was not valid JSON for the requested schema. "
		repair += "Reply with ONLY the corrected JSON object matching the schema. "
		repair += "Do not include the schema text or choose placeholder values."
		greq2 := genRequest{Prompt: req.Prompt + "\n" + repair, Images: []string{imgB64}}
		if o.StructuredOutput {
			greq2.Format = visionSchema
		}
		out2, err2 := o.generate(ctx, greq2)
		if err2 == nil {
			if json.Unmarshal([]byte(out2), res) == nil {
				out = out2
			} else {
				res.SchemaFailed = true
			}
		} else {
			res.SchemaFailed = true
		}
		res.RawOutput = out
	}
	if res.CropID == "" {
		res.CropID = req.CropID
	}
	if res.BBoxGlobal.X0 == 0 && res.BBoxGlobal.Y0 == 0 {
		res.BBoxGlobal = req.BBoxGlobal
	}
	if res.SchemaFailed && len(res.Components) == 0 {
		res.Confidence = 0
	}
	return res, nil
}

var _ TextBackend = (*Ollama)(nil)

// Complete sends a text-only completion to the local text model.
func (o *Ollama) Complete(ctx context.Context, req TextRequest) (*TextResult, error) {
	out, err := o.generate(ctx, genRequest{Prompt: req.Prompt})
	if err != nil {
		return nil, err
	}
	return &TextResult{Output: out, Confidence: 1.0, RawOutput: out}, nil
}
