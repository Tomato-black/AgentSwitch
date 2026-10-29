package bridge

import (
	"testing"
	"time"

	"AgentSwitch/internal/storage"
)

func TestUsageReportAndPrices(t *testing.T) {
	a := tempApp(t)
	if err := a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "r1", StartedAt: time.Now().UnixMilli(), Channel: "main", Model: "m1",
		Status: 200, InputTokens: 1_000_000, OutputTokens: 500_000,
	}); err != nil {
		t.Fatal(err)
	}
	g := Gateway{A: a}
	if err := g.SavePricesJSON(`{"m1":{"input":1.5,"output":3.0}}`); err != nil {
		t.Fatal(err)
	}
	rep := g.UsageReport(30)
	if len(rep.Rows) != 1 || rep.TotalInput != 1_000_000 || rep.TotalOutput != 500_000 || !rep.Priced {
		t.Fatalf("report = %+v", rep)
	}
	if rep.TotalCostUSD < 2.9 || rep.TotalCostUSD > 3.1 {
		t.Fatalf("cost = %v", rep.TotalCostUSD)
	}
	if g.PricesJSON() == "" {
		t.Fatal("prices json must not be empty")
	}
}

func TestSavePricesRejectsInvalid(t *testing.T) {
	g := Gateway{A: tempApp(t)}
	if err := g.SavePricesJSON(`{broken`); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	if err := g.SavePricesJSON(`{"m1":{"input":-1,"output":0}}`); err == nil {
		t.Fatal("negative price must be rejected")
	}
}

func TestDurationSeriesOrderAndFilter(t *testing.T) {
	a := tempApp(t)
	now := time.Now()
	_ = a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "old", StartedAt: now.Add(-48 * time.Hour).UnixMilli(), Channel: "old", DurationMs: 1,
	})
	_ = a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "a", StartedAt: now.Add(-time.Minute).UnixMilli(), Channel: "c1", DurationMs: 120,
	})
	_ = a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "b", StartedAt: now.UnixMilli(), Channel: "c2", DurationMs: 80,
	})
	got := (Gateway{A: a}).DurationSeries(1, 100)
	if len(got) != 2 || got[0].Channel != "c1" || got[1].Channel != "c2" {
		t.Fatalf("series = %+v", got)
	}
}

func TestDailyUsageFillsMissingDays(t *testing.T) {
	a := tempApp(t)
	now := time.Now()
	if err := a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "r1", StartedAt: now.UnixMilli(), Channel: "ds",
		InputTokens: 100, OutputTokens: 20,
	}); err != nil {
		t.Fatal(err)
	}
	got := (Gateway{A: a}).DailyUsage(3)
	if len(got) != 3 {
		t.Fatalf("daily = %+v", got)
	}
	last := got[len(got)-1]
	if last.InputTokens != 100 || last.OutputTokens != 20 {
		t.Fatalf("today row = %+v", last)
	}
}

func TestQuotaReportAndValidation(t *testing.T) {
	a := tempApp(t)
	now := time.Now()
	if err := a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "r1", StartedAt: now.UnixMilli(), Channel: "ds", Model: "m1",
		InputTokens: 1_000_000, OutputTokens: 0, Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	g := Gateway{A: a}
	if err := g.SavePricesJSON(`{"m1":{"input":1,"output":0}}`); err != nil {
		t.Fatal(err)
	}
	if err := g.SaveQuotasJSON(`{"ds":{"tokens":2000000,"usd":10,"reset_day":1}}`); err != nil {
		t.Fatal(err)
	}
	rep := g.QuotaReport()
	if len(rep) != 1 {
		t.Fatalf("quota report = %+v", rep)
	}
	q := rep[0]
	if q.TokenUsed != 1_000_000 || q.TokenRemaining != 1_000_000 {
		t.Fatalf("token quota = %+v", q)
	}
	if q.USDUsed != 1 || q.USDRemaining != 9 {
		t.Fatalf("usd quota = %+v", q)
	}
	if err := g.SaveQuotasJSON(`{"ds":{"reset_day":29}}`); err == nil {
		t.Fatal("reset_day 29 must be rejected")
	}
	if err := g.SaveQuotasJSON(`{"ds":{"tokens":-1}}`); err == nil {
		t.Fatal("negative quota must be rejected")
	}
}

func TestQuotaPeriodStart(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, loc)
	if got := quotaPeriodStart(now, 10); got.Month() != time.September || got.Day() != 10 {
		t.Fatalf("start = %v", got)
	}
	if got := quotaPeriodStart(now, 1); got.Month() != time.October || got.Day() != 1 {
		t.Fatalf("start = %v", got)
	}
}
