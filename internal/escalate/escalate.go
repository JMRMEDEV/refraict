// Package escalate implements graceful escalation: region-level retries with a
// stronger model when confidence is low, crop disagreement is high, or schema
// compliance fails.
package escalate

// Signal aggregates indicators that may trigger escalation.
type Signal struct {
	PageConfidence        float64
	UnresolvedComponents  int
	CropDisagreementRate  float64
	SchemaFailures        int
	TinyTextRegions       int
}

// Policy defines escalation thresholds.
type Policy struct {
	MinPageConfidence      float64
	MaxUnresolved          int
	MaxDisagreementRate    float64
	FailOnSchemaFailure    bool
}

// DefaultPolicy returns the recommended initial policy.
func DefaultPolicy() Policy {
	return Policy{
		MinPageConfidence:   0.80,
		MaxUnresolved:       20,
		MaxDisagreementRate: 0.30,
		FailOnSchemaFailure: true,
	}
}

// NeedsEscalation evaluates the policy against a signal.
func NeedsEscalation(s Signal, p Policy) bool {
	if s.PageConfidence < p.MinPageConfidence {
		return true
	}
	if s.UnresolvedComponents > p.MaxUnresolved {
		return true
	}
	if s.CropDisagreementRate > p.MaxDisagreementRate {
		return true
	}
	if p.FailOnSchemaFailure && s.SchemaFailures > 0 {
		return true
	}
	return false
}
