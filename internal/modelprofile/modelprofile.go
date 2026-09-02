// Package modelprofile centralizes the model-reactive output quirks that
// otherwise leak into shared filters as hardcoded constants. Different small
// VLMs produce differently-shaped noise (verbosity, format-garbage, citing hex
// color codes in prose), and the right handling is model-specific. A Profile
// captures those knobs explicitly and per model, so switching the vision model
// switches its output-handling rules rather than silently relying on constants
// tuned for whichever model happened to be tested last.
package modelprofile

import "strings"

// Profile holds the output-handling knobs for a given model.
type Profile struct {
	// Name is the profile's identifier (for diagnostics).
	Name string
	// MaxLabelWords rejects element labels longer than this (rambling/refusals).
	// Verbose models (gemma3) warrant a tighter cap; terser models can allow more.
	MaxLabelWords int
	// StripHexInNumbers strips hex color codes from a summary before the
	// numeric-claim grounding check. Models that cite "#RRGGBB" in prose
	// (gemma3) need this so colors are not mis-flagged as unsupported numbers.
	StripHexInNumbers bool
	// GarbageMarkers are substrings whose presence marks an output as
	// format-garbage (code/markup) to be discarded before voting.
	GarbageMarkers []string
	// StructuredOutput indicates the model reliably supports Ollama JSON-schema
	// guided generation. Most small VLMs do not (return empty), so default false.
	StructuredOutput bool
}

// defaultGarbage is the shared baseline set of format-garbage markers.
var defaultGarbage = []string{"<svg", "```", "viewbox", "{", "http", "path fill"}

// Default is the conservative baseline profile used when a model has no specific
// entry. It applies the general small-VLM noise defenses.
func Default() Profile {
	return Profile{
		Name:              "default",
		MaxLabelWords:     4,
		StripHexInNumbers: true, // harmless when absent; correct when present
		GarbageMarkers:    append([]string(nil), defaultGarbage...),
		StructuredOutput:  false,
	}
}

// registry maps a model-name substring (lowercased) to its profile. Resolution
// is by substring so "gemma3:4b", "gemma3:12b", etc. all match "gemma3".
var registry = []struct {
	match   string
	profile Profile
}{
	{
		match: "gemma3",
		profile: Profile{
			Name:              "gemma3",
			MaxLabelWords:     4,
			StripHexInNumbers: true, // gemma3 cites hex codes in prose
			GarbageMarkers:    append([]string(nil), defaultGarbage...),
			StructuredOutput:  false,
		},
	},
	{
		match: "llava-phi3",
		profile: Profile{
			Name:              "llava-phi3",
			MaxLabelWords:     4, // phi3 rambles/refuses on tiny crops
			StripHexInNumbers: true,
			GarbageMarkers:    append([]string(nil), defaultGarbage...),
			StructuredOutput:  false,
		},
	},
	{
		match: "moondream",
		profile: Profile{
			Name:              "moondream",
			MaxLabelWords:     5, // moondream tends to be terse; allow a bit more
			StripHexInNumbers: true,
			GarbageMarkers:    append([]string(nil), defaultGarbage...),
			StructuredOutput:  false,
		},
	},
}

// Resolve returns the Profile for a model name (case-insensitive substring
// match), falling back to Default when none matches.
func Resolve(model string) Profile {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, e := range registry {
		if strings.Contains(m, e.match) {
			return e.profile
		}
	}
	return Default()
}
