package cli

import (
	"context"
	"path/filepath"
	"strings"
)

// AnalyzeRequest is the in-process analyze input for embedders (e.g. the MCP
// server). It maps to the same pipeline the `analyze` CLI command runs.
type AnalyzeRequest struct {
	ImagePath  string // required
	ConfigPath string // optional; empty uses built-in defaults
	OutputDir  string // optional; empty => ./analysis-<basename>
}

// Analyze runs the full analysis pipeline in-process and returns the output
// directory (where page.json / page.md / graph.json / evidence/ were written).
// It is the embedding entry point for the MCP server: no cobra, no os.Exit.
func Analyze(ctx context.Context, req AnalyzeRequest) (string, error) {
	// The pipeline reads a few package-level flags; set them for this call.
	configPath = req.ConfigPath
	out := req.OutputDir
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(req.ImagePath), filepath.Ext(req.ImagePath))
		out = "./analysis-" + base
	}
	o := &analysisOptions{output: out, quiet: true}
	if err := runAnalyze(ctx, req.ImagePath, o); err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "/"), nil
}
