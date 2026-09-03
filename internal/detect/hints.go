package detect

import (
	"regexp"
	"strings"

	"github.com/refraict/refraict/internal/ir"
)

// hintPattern maps a compiled regex to a semantic-hint kind. anchored patterns
// must match the WHOLE token (identifiers like task_id/branch that are standalone
// by nature); search patterns find the pattern anywhere within a phrase (data
// like emails/ratios/counts commonly embedded in longer OCR spans). For search
// patterns, the captured submatch (group 1, else the whole match) becomes Value.
type hintPattern struct {
	kind     string
	re       *regexp.Regexp
	anchored bool
}

// hintPatterns is the ordered library of deterministic text→meaning rules.
// Ordered most-specific-first so a token matches at most one kind. Grown
// iteratively; start with high-value, unambiguous UI patterns (Milestone D).
var hintPatterns = []hintPattern{
	{"task_id", regexp.MustCompile(`^[A-Z]{2,5}-\d{1,6}$`), true},
	{"git_branch_ref", regexp.MustCompile(`^(?:feat|fix|chore|refactor|docs|test|ci|build|perf|style)/[\w.\-/]+$`), true},
	{"email", regexp.MustCompile(`([\w.+\-]+@[\w\-]+\.[\w.\-]*[\w])`), false},
	{"overdue_deadline", regexp.MustCompile(`(?i)overdue`), false},
	{"completion_ratio", regexp.MustCompile(`(?:^|\s)(\d{1,3}/\d{1,3})(?:\s|$)`), false},
	{"currency_amount", regexp.MustCompile(`(\$\d[\d,]*(?:\.\d+)?)`), false},
	{"percentage", regexp.MustCompile(`(?:^|\s)(\d{1,3}%)`), false},
	{"count_badge", regexp.MustCompile(`\((\d{1,4})\)`), false},
}

// AttachSemanticHints scans each component's text and, when it matches a known UI
// pattern, attaches an ir.SemanticHint (deterministic, no model). It only sets a
// hint on components that don't already have one, and never overwrites Semantic
// (VLM labels). Returns the number of hints attached.
func AttachSemanticHints(comps []ir.Component) int {
	n := 0
	for i := range comps {
		c := &comps[i]
		if c.SemanticHint != nil || c.Text == nil {
			continue
		}
		txt := strings.TrimSpace(c.Text.Value)
		if txt == "" {
			continue
		}
		for _, p := range hintPatterns {
			if p.anchored {
				if p.re.MatchString(txt) {
					c.SemanticHint = &ir.SemanticHint{Kind: p.kind, Value: txt}
					n++
					break
				}
				continue
			}
			m := p.re.FindStringSubmatch(txt)
			if m == nil {
				continue
			}
			val := m[0]
			if len(m) > 1 && m[1] != "" {
				val = m[1]
			}
			h := &ir.SemanticHint{Kind: p.kind}
			// overdue_deadline is a state flag, not a datum — no value.
			if p.kind != "overdue_deadline" {
				h.Value = strings.TrimSpace(val)
			}
			c.SemanticHint = h
			n++
			break
		}
	}
	return n
}

