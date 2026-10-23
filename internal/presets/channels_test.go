package presets

import (
	"strings"
	"testing"
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
