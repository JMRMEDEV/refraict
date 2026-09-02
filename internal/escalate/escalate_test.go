package escalate

import "testing"

func TestDefaultPolicyEscalatesOnLowConfidence(t *testing.T) {
	p := DefaultPolicy()
	ok := NeedsEscalation(Signal{PageConfidence: 0.60, SchemaFailures: 0}, p)
	if !ok {
		t.Fatal("low page confidence should escalate")
	}
}

func TestDefaultPolicyDoesNotEscalateHealthy(t *testing.T) {
	p := DefaultPolicy()
	ok := NeedsEscalation(Signal{
		PageConfidence:       0.95,
		UnresolvedComponents: 2,
		CropDisagreementRate: 0.05,
		SchemaFailures:       0,
	}, p)
	if ok {
		t.Fatal("healthy signal should not escalate")
	}
}

func TestFailOnSchemaFailureGoverns(t *testing.T) {
	p := DefaultPolicy()
	if !NeedsEscalation(Signal{SchemaFailures: 1}, p) {
		t.Fatal("schema failure should escalate by default")
	}
	p.FailOnSchemaFailure = false
	if NeedsEscalation(Signal{SchemaFailures: 1, PageConfidence: 1.0}, p) {
		t.Fatal("schema failure ignored when FailOnSchemaFailure is false")
	}
}
