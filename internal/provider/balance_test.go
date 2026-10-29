package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeBalanceDeepSeek(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Fatal("missing bearer auth")
		}
		w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`))
	}))
	defer up.Close()
	got := ProbeBalance(context.Background(), "deepseek", up.URL, "k", true)
	if !got.Supported || got.Currency != "CNY" || got.Total != 110 || got.Granted != 10 || got.ToppedUp != 100 {
		t.Fatalf("balance = %+v", got)
	}
}

func TestProbeBalanceKimi(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/users/me/balance" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":0,"data":{"available_balance":88.5,"voucher_balance":8.5,"cash_balance":80},"status":true}`))
	}))
	defer up.Close()
	got := ProbeBalance(context.Background(), "kimi", up.URL, "k", true)
	if !got.Supported || got.Available != 88.5 || got.Granted != 8.5 || got.ToppedUp != 80 {
		t.Fatalf("balance = %+v", got)
	}
}

func TestProbeBalanceOpenRouter(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credits" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":{"total_credits":100.5,"total_usage":25.75}}`))
	}))
	defer up.Close()
	got := ProbeBalance(context.Background(), "openrouter", up.URL, "k", true)
	if !got.Supported || got.Total != 100.5 || got.Available != 74.75 || got.Currency != "USD" {
		t.Fatalf("balance = %+v", got)
	}
}

func TestProbeBalanceUnsupportedAndHTTPError(t *testing.T) {
	got := ProbeBalance(context.Background(), "custom", "https://api.example.com", "k", false)
	if got.Supported || got.Message == "" {
		t.Fatalf("unsupported = %+v", got)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer up.Close()
	got = ProbeBalance(context.Background(), "deepseek", up.URL, "k", true)
	if got.Message == "" || got.Available != 0 {
		t.Fatalf("error = %+v", got)
	}
}
