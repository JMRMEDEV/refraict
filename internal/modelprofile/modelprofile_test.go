package modelprofile

import "testing"

func TestResolveKnownModels(t *testing.T) {
	cases := map[string]string{
		"gemma3:4b":         "gemma3",
		"gemma3:12b":        "gemma3",
		"llava-phi3":        "llava-phi3",
		"llava-phi3:latest": "llava-phi3",
		"moondream":         "moondream",
		"moondream:latest":  "moondream",
	}
	for model, wantName := range cases {
		if got := Resolve(model); got.Name != wantName {
			t.Errorf("Resolve(%q).Name = %q, want %q", model, got.Name, wantName)
		}
	}
}

func TestResolveUnknownFallsBackToDefault(t *testing.T) {
	got := Resolve("some-unknown-model:7b")
	if got.Name != "default" {
		t.Fatalf("expected default profile, got %q", got.Name)
	}
}

func TestResolveCaseInsensitive(t *testing.T) {
	if Resolve("GEMMA3:4B").Name != "gemma3" {
		t.Fatal("resolution should be case-insensitive")
	}
}

func TestProfilesHaveSaneDefaults(t *testing.T) {
	for _, m := range []string{"gemma3:4b", "llava-phi3", "moondream", "unknown"} {
		p := Resolve(m)
		if p.MaxLabelWords <= 0 {
			t.Errorf("%s: MaxLabelWords must be positive, got %d", m, p.MaxLabelWords)
		}
		if len(p.GarbageMarkers) == 0 {
			t.Errorf("%s: GarbageMarkers must be non-empty", m)
		}
	}
}

func TestDefaultProfile(t *testing.T) {
	d := Default()
	if d.Name != "default" || d.MaxLabelWords != 4 || !d.StripHexInNumbers {
		t.Fatalf("unexpected default profile: %+v", d)
	}
}
