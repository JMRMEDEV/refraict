package detect

import (
	"testing"

	"github.com/refraict/refraict/internal/ir"
)

func hintComp(text string) ir.Component {
	return ir.Component{Type: ir.ConstString{Value: "text"}, Text: &ir.ConstString{Value: text}}
}

func TestAttachSemanticHints(t *testing.T) {
	cases := []struct {
		text     string
		wantKind string
		wantVal  string
	}{
		{"PH-123", "task_id", "PH-123"},
		{"feat/PH-123-implement-login-screen", "git_branch_ref", "feat/PH-123-implement-login-screen"},
		{"We sent a verification email to you@example.com.", "email", "you@example.com"},
		{"Apr 25, 2026 (Overdue)", "overdue_deadline", ""},
		{"2/5", "completion_ratio", "2/5"},
		{"IN PROGRESS (3)", "count_badge", "3"},
		{"Total $1,591", "currency_amount", "$1,591"},
		{"75%", "percentage", "75%"},
	}
	comps := make([]ir.Component, len(cases))
	for i, c := range cases {
		comps[i] = hintComp(c.text)
	}
	n := AttachSemanticHints(comps)
	if n != len(cases) {
		t.Fatalf("expected %d hints, got %d", len(cases), n)
	}
	for i, c := range cases {
		h := comps[i].SemanticHint
		if h == nil {
			t.Fatalf("%q: no hint", c.text)
		}
		if h.Kind != c.wantKind {
			t.Fatalf("%q: kind=%q want %q", c.text, h.Kind, c.wantKind)
		}
		if h.Value != c.wantVal {
			t.Fatalf("%q: value=%q want %q", c.text, h.Value, c.wantVal)
		}
	}
}

func TestAttachSemanticHintsNoFalsePositives(t *testing.T) {
	comps := []ir.Component{
		hintComp("Sign in to your account"),
		hintComp("Dashboard"),
		hintComp(""),
	}
	if n := AttachSemanticHints(comps); n != 0 {
		t.Fatalf("expected 0 hints on plain text, got %d", n)
	}
}
