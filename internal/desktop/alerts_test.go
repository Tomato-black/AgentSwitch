package desktop

import (
	"strings"
	"testing"
	"time"

	"AgentSwitch/internal/bridge"
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

// TestAlertQuotaThresholds 覆盖 B3：本地渠道配额用量 ≥80% 与 ≥100% 各
// 告警一次（按渠道 24 小时去重），79% 不告警；文案含渠道名。
func TestAlertQuotaThresholds(t *testing.T) {
	a := newAlertState()
	now := time.Now()
	none := bridge.QuotaStatus{Channel: "主渠道", TokenQuota: 1000, TokenUsed: 790}
	if id, _, _ := a.evaluateQuota([]bridge.QuotaStatus{none}, now); id != "" {
		t.Fatalf("79%% 不应告警：%s", id)
	}
	over80 := bridge.QuotaStatus{Channel: "主渠道", TokenQuota: 1000, TokenUsed: 810}
	id, title, body := a.evaluateQuota([]bridge.QuotaStatus{over80}, now)
	if id != "quota-80" || !strings.Contains(body, "主渠道") || title == "" {
		t.Fatalf("81%% 应告警 quota-80：%q %q %q", id, title, body)
	}
	if id, _, _ := a.evaluateQuota([]bridge.QuotaStatus{over80}, now.Add(time.Hour)); id != "" {
		t.Fatalf("同渠道 80%% 24 小时内不应重复告警：%s", id)
	}
	full := bridge.QuotaStatus{Channel: "主渠道", TokenQuota: 1000, TokenUsed: 1000}
	id, _, body = a.evaluateQuota([]bridge.QuotaStatus{full}, now.Add(2*time.Hour))
	if id != "quota-100" || !strings.Contains(body, "主渠道") {
		t.Fatalf("100%% 应告警 quota-100：%q %q", id, body)
	}
	other := bridge.QuotaStatus{Channel: "备渠道", USDQuota: 10, USDUsed: 9.5}
	if id, _, _ := a.evaluateQuota([]bridge.QuotaStatus{other}, now.Add(3*time.Hour)); id != "quota-80" {
		t.Fatalf("其他渠道独立计数（USD 配额 95%%）：%q", id)
	}
}

// TestAlertQuotaCheckDue 覆盖 10 分钟闸门：首查放行、窗口内拒绝、过窗放行。
func TestAlertQuotaCheckDue(t *testing.T) {
	a := newAlertState()
	now := time.Now()
	if !a.quotaCheckDue(now) {
		t.Fatal("首查应放行")
	}
	if a.quotaCheckDue(now.Add(time.Minute)) {
		t.Fatal("窗口内不应重查")
	}
	if !a.quotaCheckDue(now.Add(11 * time.Minute)) {
		t.Fatal("过窗应放行")
	}
}
