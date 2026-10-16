package bridge

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AgentSwitch/internal/credential"
)

func channelsSvc(t *testing.T) (Channels, *credential.Manager) {
	t.Helper()
	a := tempApp(t)
	creds := &credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	return Channels{A: a, Creds: creds, KeychainAvailable: func() bool { return false }}, creds
}

func TestChannelsCreateValidatesPresetAndURL(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, err := cs.Create("anthropic-official", "official", "", 10, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Protocol != "anthropic" || c.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("preset default: %+v", c)
	}
	// 兼容类无默认 URL：空 BaseURL 报中文错
	if _, err := cs.Create("openai-compatible", "agg", "", 10, true, false); err == nil ||
		!strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("empty baseURL: %v", err)
	}
	// 未知类型
	if _, err := cs.Create("nope", "x", "https://x.example.com", 10, true, false); err == nil {
		t.Fatal("unknown type must error")
	}
	// 非回环 http 拒绝（ValidateBaseURL 沿用）
	if _, err := cs.Create("anthropic-compatible", "x", "http://relay.example.com", 10, true, false); err == nil {
		t.Fatal("non-loopback http must be rejected")
	}
}

func TestChannelsUpdateListRemove(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "main", "https://r.example.com", 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345\n", true); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddModel(c.ID, "pub-1", "up-1"); err != nil {
		t.Fatal(err)
	}
	if err := cs.Update(c.ID, "main2", "https://r2.example.com", 5, false, false); err != nil {
		t.Fatal(err)
	}
	list := cs.List()
	if len(list) != 1 || list[0].Name != "main2" || list[0].Priority != 5 || list[0].Enabled {
		t.Fatalf("list %+v", list)
	}
	if list[0].KeyCount != 1 || list[0].ModelCount != 1 {
		t.Fatalf("counts: %+v", list[0])
	}
	d, err := cs.Get(c.ID)
	if err != nil || len(d.Keys) != 1 || len(d.Models) != 1 {
		t.Fatalf("detail %+v %v", d, err)
	}
	// Remove 级联清密钥本体
	if err := cs.Remove(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(cs.List()) != 0 {
		t.Fatal("remove failed")
	}
}

func TestChannelsAddKeysBatchAndBlankLines(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	// Review Focus 4：空行与首尾空白容忍
	n, err := cs.AddKeys(c.ID, "pool", "  sk-aaa11111  \n\nsk-bbb22222\n\n", true)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// 密钥本体存在性由 Remove/RemoveKey 的级联清密钥路径覆盖
	d, _ := cs.Get(c.ID)
	// ListByChannel 同优先级按随机 id 排序——按尾4索引断言（值与简报一致，不依赖行序）。
	if len(d.Keys) != 2 {
		t.Fatalf("keys %+v", d.Keys)
	}
	tails := map[string]string{}
	for _, k := range d.Keys {
		tails[k.KeyTail] = k.SecretBackend
	}
	if tails["1111"] != "fallback-file" || tails["2222"] != "fallback-file" {
		t.Fatalf("keys %+v", d.Keys)
	}
}

func TestChannelsRemoveKeyDeletesSecretAndCascade(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	d, _ := cs.Get(c.ID)
	if err := cs.RemoveKey(d.Keys[0].ID); err != nil {
		t.Fatal(err)
	}
	d, _ = cs.Get(c.ID)
	if len(d.Keys) != 0 {
		t.Fatal("key not removed")
	}
	// 删渠道级联删 key 行；密钥本体清理在 Remove 内逐个 Delete（不报错即过）
	if _, err := cs.AddKeys(c.ID, "k", "sk-def67890", true); err != nil {
		t.Fatal(err)
	}
	if err := cs.Remove(c.ID); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 2：重复公开模型 → 中文友好错误。
func TestChannelsAddModelDuplicateFriendlyError(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	if _, err := cs.AddModel(c.ID, "pub", "up"); err != nil {
		t.Fatal(err)
	}
	_, err := cs.AddModel(c.ID, "pub", "up2")
	if err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("want friendly duplicate error, got %v", err)
	}
	// 跨渠道同公开名允许
	c2, _ := cs.Create("openai-compatible", "o", "https://o.example.com", 20, true, false)
	if _, err := cs.AddModel(c2.ID, "pub", "up-openai"); err != nil {
		t.Fatalf("cross-channel duplicate must be allowed: %v", err)
	}
}

func TestChannelsTestAndTestKey(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("probe path %q", r.URL.Path)
		}
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("anthropic-compatible", "m", up.URL, 10, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	if res := cs.Test(c.ID); !res.OK {
		t.Fatalf("channel test: %+v", res)
	}
	d, _ := cs.Get(c.ID)
	if res := cs.TestKey(d.Keys[0].ID); !res.OK {
		t.Fatalf("key test: %+v", res)
	}
}

func TestChannelsFetchModelsBothProtocols(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" && r.Header.Get("X-Api-Key") == "" {
			t.Error("no auth header")
		}
		w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}]}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", up.URL, 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil || len(ids) != 2 || ids[1] != "m-b" {
		t.Fatalf("fetch: %v %v", ids, err)
	}
	// 无密钥渠道报错
	c2, _ := cs.Create("openai-compatible", "o", up.URL, 20, true, false)
	if _, err := cs.FetchModels(c2.ID); err == nil {
		t.Fatal("fetch without key must error")
	}
}

func TestChannelsPoolStatusAndCooldownKeyView(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	// 直接冷却 k1
	d, _ := cs.Get(c.ID)
	cs.A.Cooldowns().MarkClassified(d.Keys[0].ID, "rate_limited", 30, time.Now())
	d, _ = cs.Get(c.ID)
	if !d.Keys[0].Cooling || d.Keys[0].CoolingRemainSec <= 0 || d.Keys[0].CoolingRemainSec > 30 {
		t.Fatalf("key cooling view: %+v", d.Keys[0])
	}
	if d.Channel.CoolingCount != 1 {
		t.Fatalf("channel cooling count: %+v", d.Channel)
	}
	ps := Gateway{A: cs.A}.PoolStatus()
	if len(ps) != 1 || ps[0].KeyName != "k" || ps[0].RemainingSec <= 0 {
		t.Fatalf("pool: %+v", ps)
	}
	// Review Focus 5：剩余秒恒为正（coolingMap +1 钳制），过期条目由 Snapshot 过滤
	for _, e := range ps {
		if e.RemainingSec < 0 {
			t.Fatalf("negative remaining: %+v", e)
		}
	}
}

func TestModelsAggJoinAndToggle(t *testing.T) {
	cs, _ := channelsSvc(t)
	c1, _ := cs.Create("anthropic-compatible", "a", "https://a.example.com", 10, true, false)
	c2, _ := cs.Create("openai-compatible", "b", "https://b.example.com", 20, true, false)
	cs.AddModel(c1.ID, "pub", "up-a")
	cs.AddModel(c2.ID, "pub", "up-b")
	agg := ModelsAgg{A: cs.A}
	rows := agg.ListAll()
	if len(rows) != 2 {
		t.Fatalf("agg rows %+v", rows)
	}
	// List 按 (channel_id, public_model) 序，channel_id 随机——按渠道名索引断言
	// （值与简报一致，不依赖行序）。
	byName := map[string]ModelAggregateView{}
	for _, r := range rows {
		byName[r.ChannelName] = r
	}
	if byName["a"].PublicModel != "pub" || byName["a"].UpstreamModel != "up-a" ||
		byName["b"].UpstreamModel != "up-b" {
		t.Fatalf("agg rows %+v", rows)
	}
	if err := agg.SetEnabled(rows[0].ID, false); err != nil {
		t.Fatal(err)
	}
	for _, r := range agg.ListAll() {
		if r.ID == rows[0].ID && r.Enabled {
			t.Fatal("toggle failed")
		}
	}
}
