package iconlabel

import "testing"

func mustNew(t *testing.T) *Canonicalizer {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestCanonicalizeMetaphors(t *testing.T) {
	c := mustNew(t)
	cases := map[string]string{
		"search icon":       "search",
		"a magnifier":       "search",
		"magnifying glass":  "search",
		"credit card":       "credit card",
		"settings gear":     "settings", // "settings" strong single-token
		"the X button":      "x",
	}
	for in, want := range cases {
		if got := c.Canonicalize(in); got != want {
			t.Errorf("Canonicalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalizeRejectsGarbageAndLong(t *testing.T) {
	c := mustNew(t)
	for _, in := range []string{
		"",
		"```svg <path fill",
		"i am not sure but this could be one of several things possibly a settings", // > maxLabelWords
		"{ \"type\": \"object\" }",
	} {
		if got := c.Canonicalize(in); got != "" {
			t.Errorf("Canonicalize(%q) = %q, want empty", in, got)
		}
	}
}

func TestCanonicalizeWeakLastResort(t *testing.T) {
	c := mustNew(t)
	// "document" is a weak alias (common); alone it still resolves as a last
	// resort (non-empty), but a strong token in the phrase should win.
	if got := c.Canonicalize("document"); got == "" {
		t.Errorf("weak alias should resolve as last resort, got empty")
	}
}

func TestVoteModeAndAgreement(t *testing.T) {
	c := mustNew(t)
	raw := []string{
		"search icon", "search", "a magnifier", // -> search x3
		"credit card",                          // -> credit card x1
		"```garbage```",                        // ignored
		"",                                     // ignored
	}
	r := c.Vote(raw)
	if r.Concept != "search" {
		t.Fatalf("Vote concept = %q, want search", r.Concept)
	}
	if r.Agreement != 3 {
		t.Fatalf("Vote agreement = %d, want 3", r.Agreement)
	}
	if r.Samples != len(raw) {
		t.Fatalf("Vote samples = %d, want %d", r.Samples, len(raw))
	}
	if r.Ratio <= 0 || r.Ratio > 1 {
		t.Fatalf("Vote ratio out of range: %v", r.Ratio)
	}
}

func TestVoteEmpty(t *testing.T) {
	c := mustNew(t)
	r := c.Vote(nil)
	if r.Concept != "" || r.Agreement != 0 || r.Ratio != 0 {
		t.Fatalf("empty vote should be zero-valued, got %+v", r)
	}
}
