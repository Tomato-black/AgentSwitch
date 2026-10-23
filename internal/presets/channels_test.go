package presets

import (
	"os"
	"strings"
	"testing"

	"AgentSwitch/internal/provider"
)

// fixtureEmbedded 是最小内嵌默认表 fixture：一条双协议（qwen 形态）+ 一条仅
// openai，供 Task 2 加载器测试；真实 20 条默认表由 default.json 任务落地。
const fixtureEmbedded = `[
  {"id":"qwen","displayName":"阿里千问","docUrl":"https://bailian.console.aliyun.com/",
   "protocols":{
     "openai":{"baseUrl":"https://dashscope.aliyuncs.com/compatible-mode/v1","defaultModels":["qwen3.8-max"]},
     "anthropic":{"baseUrl":"https://dashscope.aliyuncs.com/apps/anthropic","defaultModels":["qwen3.8-max"]}}},
  {"id":"groq","displayName":"Groq","docUrl":"https://console.groq.com/",
   "protocols":{"openai":{"baseUrl":"https://api.groq.com/openai/v1","defaultModels":["openai/gpt-oss-120b"]}}}
]`

// TestLoadEmbeddedOnly 覆盖 spec §4：仅内嵌字节（用户文件空）时 All() 即该表，
// 顺序保持，Warning() 为空。
func TestLoadEmbeddedOnly(t *testing.T) {
	Load([]byte(fixtureEmbedded), "")
	all := All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all[0].ID != "qwen" || all[1].ID != "groq" {
		t.Fatalf("order = [%s %s], want [qwen groq]", all[0].ID, all[1].ID)
	}
	if all[0].Protocols["openai"].BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("qwen openai baseUrl = %+v", all[0].Protocols["openai"])
	}
	if all[0].Protocols["anthropic"].BaseURL != "https://dashscope.aliyuncs.com/apps/anthropic" {
		t.Fatalf("qwen anthropic baseUrl = %+v", all[0].Protocols["anthropic"])
	}
	if w := Warning(); w != "" {
		t.Fatalf("Warning() = %q, want empty", w)
	}
}

// TestLoadSkipsInvalidEntries 覆盖 spec §4 单条校验失败只跳过该条：id 空、
// protocols 空、协议键未知各自被剔除且计入警告，好条目照常保留。
func TestLoadSkipsInvalidEntries(t *testing.T) {
	fixture := `[
  {"id":"","displayName":"无 ID","protocols":{"openai":{"baseUrl":"https://a.example.com"}}},
  {"id":"empty-proto","displayName":"空协议","protocols":{}},
  {"id":"bad-proto","displayName":"未知协议","protocols":{"grpc":{"baseUrl":"https://b.example.com"}}},
  {"id":"good","displayName":"好条目","protocols":{"anthropic":{"baseUrl":"https://api.anthropic.com"}}}
]`
	Load([]byte(fixture), "")
	all := All()
	if len(all) != 1 || all[0].ID != "good" {
		t.Fatalf("All() = %+v, want only good", all)
	}
	w := Warning()
	for _, want := range []string{"empty-proto", "bad-proto"} {
		if !strings.Contains(w, want) {
			t.Errorf("Warning() %q missing %q", w, want)
		}
	}
}

// TestById 按 id 命中与未命中。
func TestById(t *testing.T) {
	Load([]byte(fixtureEmbedded), "")
	p, ok := ById("qwen")
	if !ok || p.ID != "qwen" {
		t.Fatalf("ById(qwen) = %+v, %v", p, ok)
	}
	if _, ok := ById("no-such"); ok {
		t.Error("ById(no-such) ok=true, want false")
	}
}

// TestLoadEmbeddedMustPassValidateBaseURL 覆盖 spec §4：内嵌条目 baseUrl 必须
// 过 provider.ValidateBaseURL(_, false)（公网 https），ftp 等非法 scheme 被跳过。
func TestLoadEmbeddedMustPassValidateBaseURL(t *testing.T) {
	Load([]byte(`[{"id":"ftp-bad","protocols":{"openai":{"baseUrl":"ftp://x"}}}]`), "")
	if len(All()) != 0 {
		t.Fatalf("ftp entry must be skipped, got %+v", All())
	}
	if Warning() == "" {
		t.Fatal("Warning() empty, want non-empty for skipped ftp entry")
	}
}

// TestDefaultJSONTwentyEntries 锁死内嵌默认表（spec 2026-10-23 §6）：20 条
// （14 按量 + 6 Coding Plan 订阅）、展示序、每条至少一协议且 baseUrl 过
// ValidateBaseURL(_, false)、id 唯一、抽查端点逐字相等。数据变更须先改 spec。
func TestDefaultJSONTwentyEntries(t *testing.T) {
	Load(DefaultJSON(), "")
	all := All()
	if len(all) != 20 {
		t.Fatalf("All() len = %d, want 20", len(all))
	}
	wantOrder := []string{
		"xiaomi", "qwen", "deepseek", "kimi", "zhipu", "siliconflow", "stepfun", "doubao",
		"anthropic", "openai", "google", "groq", "together", "openrouter",
		"zhipu-coding", "doubao-coding", "kimi-coding", "qwen-coding", "stepfun-coding", "xiaomi-coding",
	}
	seen := map[string]bool{}
	for i, p := range all {
		if p.ID != wantOrder[i] {
			t.Fatalf("all[%d].ID = %q, want %q", i, p.ID, wantOrder[i])
		}
		if seen[p.ID] {
			t.Errorf("duplicate id %q", p.ID)
		}
		seen[p.ID] = true
		if len(p.Protocols) == 0 {
			t.Errorf("preset %s has no protocols", p.ID)
		}
		for proto, ep := range p.Protocols {
			if err := provider.ValidateBaseURL(ep.BaseURL, false); err != nil {
				t.Errorf("preset %s %s baseUrl %q: %v", p.ID, proto, ep.BaseURL, err)
			}
			if len(ep.DefaultModels) == 0 {
				t.Errorf("preset %s %s has no default models", p.ID, proto)
			}
		}
	}
	if w := Warning(); w != "" {
		t.Fatalf("default table must load clean, Warning() = %q", w)
	}
	// 抽查：小米双协议、Kimi Code anthropic 尾斜杠、OpenRouter 双协议同址。
	xm, _ := ById("xiaomi")
	if xm.Protocols["openai"].BaseURL != "https://api.xiaomimimo.com/v1" ||
		xm.Protocols["anthropic"].BaseURL != "https://api.xiaomimimo.com/anthropic" {
		t.Errorf("xiaomi protocols = %+v", xm.Protocols)
	}
	kc, _ := ById("kimi-coding")
	if kc.Protocols["anthropic"].BaseURL != "https://api.kimi.com/coding/" {
		t.Errorf("kimi-coding anthropic baseUrl = %q", kc.Protocols["anthropic"].BaseURL)
	}
	or, _ := ById("openrouter")
	if or.Protocols["openai"].BaseURL != "https://openrouter.ai/api/v1" ||
		or.Protocols["anthropic"].BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("openrouter protocols = %+v", or.Protocols)
	}
	// 仅 openai 的四家不得有 anthropic 键。
	for _, id := range []string{"openai", "google", "groq", "together"} {
		p, _ := ById(id)
		if _, has := p.Protocols["anthropic"]; has {
			t.Errorf("preset %s must not have anthropic endpoint (spec §6: 无)", id)
		}
	}
}

// userFileFixture 是用户覆盖文件 fixture：整条覆盖 qwen + 追加私网中转站。
const userFileFixture = `[
  {"id":"qwen","displayName":"我的千问","docUrl":"https://my.example/",
   "protocols":{"openai":{"baseUrl":"https://my-qwen.example.com/v1","defaultModels":["qwen-custom"]}}},
  {"id":"myrelay","displayName":"内网中转","docUrl":"http://relay.local/",
   "protocols":{"openai":{"baseUrl":"http://192.168.1.10:3000/v1","defaultModels":["gpt-x"]}}}
]`

// TestUserFileOverrideAndAppend 覆盖 spec §4：同 id 整体覆盖（原位）+ 新 id
// 追加末尾；用户条目放宽校验（http 私网放行）不产生警告。
func TestUserFileOverrideAndAppend(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/presets.json"
	if err := os.WriteFile(path, []byte(userFileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	Load(DefaultJSON(), path)
	all := All()
	if len(all) != 21 { // 20 内嵌 + 1 追加
		t.Fatalf("All() len = %d, want 21", len(all))
	}
	// qwen 在原位（index 1）且为用户版整体覆盖（displayName/docUrl/protocols 全换）。
	if all[1].ID != "qwen" || all[1].DisplayName != "我的千问" {
		t.Fatalf("overridden qwen = %+v", all[1])
	}
	if all[1].Protocols["openai"].BaseURL != "https://my-qwen.example.com/v1" {
		t.Fatalf("qwen openai baseUrl = %+v", all[1].Protocols["openai"])
	}
	if _, has := all[1].Protocols["anthropic"]; has {
		t.Error("整体覆盖后 anthropic 键应被用户版替换掉（用户版无该协议）")
	}
	// myrelay 追加在末尾。
	if last := all[20]; last.ID != "myrelay" || last.Protocols["openai"].BaseURL != "http://192.168.1.10:3000/v1" {
		t.Fatalf("appended myrelay = %+v", last)
	}
	if w := Warning(); w != "" {
		t.Fatalf("私网用户条目不应告警, Warning() = %q", w)
	}
}

// TestUserFileGarbageDegrades 覆盖 spec §4：用户文件语法坏 → 整体降级用内嵌
// 默认表，警告非空。
func TestUserFileGarbageDegrades(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/presets.json"
	if err := os.WriteFile(path, []byte(`{{{`), 0o600); err != nil {
		t.Fatal(err)
	}
	Load(DefaultJSON(), path)
	if len(All()) != 20 {
		t.Fatalf("degraded table len = %d, want 20", len(All()))
	}
	if Warning() == "" {
		t.Fatal("降级必须有警告")
	}
}

// TestUserFileMissing 覆盖 spec §4：用户文件不存在 → 只用内嵌表，无警告。
func TestUserFileMissing(t *testing.T) {
	Load(DefaultJSON(), "/no/such/dir/presets.json")
	if len(All()) != 20 {
		t.Fatalf("All() len = %d, want 20", len(All()))
	}
	if w := Warning(); w != "" {
		t.Fatalf("文件缺失不算异常, Warning() = %q", w)
	}
}

// TestUserFileSkipBadEntries 覆盖 spec §4：用户文件单条坏（id 空）只跳过该条，
// 其余用户条目照常生效。
func TestUserFileSkipBadEntries(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/presets.json"
	fixture := `[
  {"id":"","displayName":"无 ID","protocols":{"openai":{"baseUrl":"https://a.example.com"}}},
  {"id":"good-relay","displayName":"好中转","protocols":{"openai":{"baseUrl":"http://10.0.0.5:8080/v1"}}}
]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	Load(DefaultJSON(), path)
	if _, ok := ById("good-relay"); !ok {
		t.Fatal("good-relay missing")
	}
	if len(All()) != 21 {
		t.Fatalf("All() len = %d, want 21", len(All()))
	}
	if !strings.Contains(Warning(), "已跳过") {
		t.Fatalf("Warning() = %q, want skip note", Warning())
	}
}
