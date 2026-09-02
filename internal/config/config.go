// Package config loads and exposes Refraict runtime configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the top-level Refraict configuration.
type Config struct {
	Vision     VisionConfig     `json:"vision" yaml:"vision"`
	Summary    BackendConfig    `json:"summary" yaml:"summary"`
	Aggregator BackendConfig    `json:"aggregator" yaml:"aggregator"`
	Models     ModelsConfig     `json:"models" yaml:"models"`
	Image      ImageConfig      `json:"image" yaml:"image"`
	Analysis   AnalysisConfig   `json:"analysis" yaml:"analysis"`
	Cache      CacheConfig      `json:"cache" yaml:"cache"`
	Cloud      CloudConfig      `json:"cloud" yaml:"cloud"`
	Output     OutputConfig     `json:"output" yaml:"output"`
	Recon      ReconcilerConfig `json:"reconciler" yaml:"reconciler"`
}

// ModelsConfig holds cross-cutting local-model runtime settings.
type ModelsConfig struct {
	// KeepAlive is the Ollama keep_alive value applied to every local model
	// request. Default "0" frees each model from memory immediately after use
	// (minimizes resident RAM/VRAM — only one model loaded at a time). Set a
	// duration ("30s", "5m") or "-1" (keep indefinitely) for batch/agentic
	// callers that prefer to trade memory for reduced reload latency. The
	// --keep-warm flag overrides this at runtime.
	KeepAlive string `json:"keep_alive" yaml:"keep_alive"`
}

// VisionConfig configures the vision model backend.
type VisionConfig struct {
	Provider  string `json:"provider" yaml:"provider"`
	Model     string `json:"model" yaml:"model"`
	Endpoint  string `json:"endpoint" yaml:"endpoint"`
	Workers   int    `json:"workers" yaml:"workers"`
	BatchSize int    `json:"batch_size" yaml:"batch_size"`
}

// BackendConfig configures a text model backend.
type BackendConfig struct {
	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
	Endpoint string `json:"endpoint" yaml:"endpoint"`
}

// ImageConfig controls image ingest and crop planning.
type ImageConfig struct {
	OverviewWidth          int     `json:"overview_width" yaml:"overview_width"`
	CropLongSide           int     `json:"crop_long_side" yaml:"crop_long_side"`
	CropOverlap            float64 `json:"crop_overlap" yaml:"crop_overlap"`
	MinimumTextHeightAfter int     `json:"minimum_text_height_after_resize" yaml:"minimum_text_height_after_resize"`
	DetailLongSide         int     `json:"detail_long_side" yaml:"detail_long_side"`

	// CropStrategy selects the crop planner:
	//   "grid"     -> bounded overview + Rows x Cols focused tiles (default;
	//                 fast, OCR-independent, keeps a single model warm).
	//   "adaptive" -> legacy OCR-density-driven subdivision (can explode the
	//                 crop count on text-dense pages).
	CropStrategy string `json:"crop_strategy" yaml:"crop_strategy"`
	// GridRows/GridCols define the focused-tile grid for the "grid" strategy.
	GridRows int `json:"grid_rows" yaml:"grid_rows"`
	GridCols int `json:"grid_cols" yaml:"grid_cols"`
}

// AnalysisConfig controls analysis behavior.
type AnalysisConfig struct {
	ConfidenceThreshold float64 `json:"confidence_threshold" yaml:"confidence_threshold"`
	GenerateDOMGuess    bool    `json:"generate_dom_guess" yaml:"generate_dom_guess"`
	NoOCR               bool    `json:"no_ocr" yaml:"no_ocr"`
	NoSummary           bool    `json:"no_summary" yaml:"no_summary"`
	// DetectRegions enables deterministic CV detection of non-text UI regions
	// (cards, panels, chart containers). The pure-Go detector runs by default;
	// building with `-tags opencv` uses the stronger OpenCV Canny detector.
	DetectRegions bool `json:"detect_regions" yaml:"detect_regions"`
}

// CacheConfig controls caching behavior. The cache is a file-based JSON store
// on disk rooted at Dir; despite the earlier ".sqlite" naming, no SQLite is
// involved (see QA finding B2).
type CacheConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Dir     string `json:"dir" yaml:"dir"`
}

// UnmarshalJSON accepts both the canonical "dir" key and the legacy "database"
// key so existing configuration files keep working after the naming fix.
func (c *CacheConfig) UnmarshalJSON(data []byte) error {
	type alias CacheConfig
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*c = CacheConfig(a)
	// Legacy compatibility: if only "database" was supplied, use it as Dir.
	var legacy struct {
		Database string `json:"database"`
	}
	if err := json.Unmarshal(data, &legacy); err == nil && legacy.Database != "" && c.Dir == "" {
		c.Dir = legacy.Database
	}
	return nil
}

// CloudConfig controls cloud escalation policy.
type CloudConfig struct {
	Enabled    bool `json:"enabled" yaml:"enabled"`
	LocalOnly  bool `json:"local_only" yaml:"local_only"`
	AllowCloud bool `json:"allow_cloud" yaml:"allow_cloud"`
	RedactText bool `json:"redact_text_before_cloud" yaml:"redact_text_before_cloud"`
}

// OutputConfig controls output behavior.
type OutputConfig struct {
	Verbose bool `json:"verbose" yaml:"verbose"`
	JSON    bool `json:"json" yaml:"json"`
}

// ReconcilerConfig controls duplicate reconciliation.
type ReconcilerConfig struct {
	IoUThreshold    float64 `json:"iou_threshold" yaml:"iou_threshold"`
	ConfidenceMerge float64 `json:"confidence_merge" yaml:"confidence_merge"`
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Vision: VisionConfig{
			Provider:  "ollama",
			Model:     "qwen-vl-3b",
			Endpoint:  "http://localhost:11434",
			Workers:   1,
			BatchSize: 2,
		},
		Summary: BackendConfig{
			Provider: "ollama",
			Model:    "qwen-3b",
			Endpoint: "http://localhost:11434",
		},
		Aggregator: BackendConfig{
			Provider: "ollama",
			Model:    "qwen-14b",
			Endpoint: "http://localhost:11434",
		},
		Models: ModelsConfig{
			KeepAlive: "0",
		},
		Image: ImageConfig{
			OverviewWidth:          1000,
			CropLongSide:           1280,
			CropOverlap:            0.20,
			MinimumTextHeightAfter: 12,
			DetailLongSide:         1100,
			CropStrategy:           "grid",
			GridRows:               2,
			GridCols:               2,
		},
		Analysis: AnalysisConfig{
			ConfidenceThreshold: 0.80,
			GenerateDOMGuess:    true,
			DetectRegions:       true,
		},
		Cache: CacheConfig{
			Enabled: true,
			Dir:     "./.refraict-cache",
		},
		Cloud: CloudConfig{
			Enabled:    false,
			LocalOnly:  true,
			AllowCloud: false,
			RedactText: true,
		},
		Recon: ReconcilerConfig{
			IoUThreshold:    0.65,
			ConfidenceMerge: 0.5,
		},
	}
}

// Load reads a JSON config file and overlays it on defaults. If path is empty
// or the file does not exist, defaults are returned.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}
