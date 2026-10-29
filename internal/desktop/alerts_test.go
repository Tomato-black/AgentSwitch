package desktop

import (
	"testing"
	"time"

	"AgentSwitch/internal/telemetry"
)

func TestAlertRulesAndThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	a := newAlertState()
	if id, _, _ := a.evaluate(telemetry.RequestRecord{Status: 503, ErrorClass: "no_candidate"}, now); id != "no-candidate" {
		t.Fatalf("no_candidate id = %q", id)
	}
	if id, _, _ := a.evaluate(telemetry.RequestRecord{Status: 503, ErrorClass: "no_candidate"}, now.Add(time.Minute)); id != "" {
		t.Fatal("must throttle within 5m")
	}
	for i := 0; i < 2; i++ {
		if id, _, _ := a.evaluate(telemetry.RequestRecord{FailoverCount: 1, Status: 200}, now.Add(time.Duration(i)*time.Second)); id != "" {
			t.Fatalf("premature failover alert %q", id)
		}
	}
	if id, _, _ := a.evaluate(telemetry.RequestRecord{FailoverCount: 1, Status: 200}, now.Add(3*time.Second)); id != "failover" {
		t.Fatalf("failover id = %q", id)
	}
	q := telemetry.RequestRecord{Status: 429, ErrorClass: "quota_exhausted", Channel: "main"}
	if id, _, _ := a.evaluate(q, now.Add(10*time.Second)); id != "quota" {
		t.Fatalf("quota id = %q", id)
	}
	if id, _, _ := a.evaluate(q, now.Add(20*time.Second)); id != "" {
		t.Fatal("must throttle quota per channel")
	}
}
