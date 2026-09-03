package prompt

import (
	"strings"
	"testing"
)

func TestBuildConsolidatePrompt(t *testing.T) {
	p := BuildConsolidatePrompt("settings", "overview text here", []string{"section one text", "section two text"})
	for _, want := range []string{
		"Consolidate",
		"Do NOT add new facts",
		"Remove redundancy",
		"Page type (deterministic): settings",
		"overview text here",
		"section one text",
		"section two text",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("consolidate prompt missing %q", want)
		}
	}
	// generic page type is not injected as a directive.
	if strings.Contains(BuildConsolidatePrompt("generic", "x", nil), "Page type (deterministic): generic") {
		t.Fatal("generic page type should not be injected")
	}
}
