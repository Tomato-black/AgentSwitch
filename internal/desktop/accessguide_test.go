package desktop

import (
	"strings"
	"testing"
)

// TestMaskToken 覆盖令牌掩码：短值整体遮蔽，长值只留前 6 位（脱敏惯例与
// 编辑器/诊断一致；真值只经复制按钮进剪贴板，不落 UI 文本）。
func TestMaskToken(t *testing.T) {
	if got := maskToken(""); got != "（无）" {
		t.Fatalf("空令牌 = %q", got)
	}
	if got := maskToken("abc"); got != "****" {
		t.Fatalf("短令牌应整体遮蔽，got %q", got)
	}
	got := maskToken("9d8cd2abcdefgh")
	if got != "9d8cd2****" {
		t.Fatalf("掩码 = %q", got)
	}
	if strings.Contains(got, "abcdefgh") {
		t.Fatalf("掩码泄露了令牌尾部: %q", got)
	}
}

// TestGatewayGuideRows 覆盖通用连接信息行的拼接：三协议端点挂在同一 base
// 下、认证为 Bearer（网关 authorized 只认 Authorization: Bearer，已核实
// internal/gateway/server.go）、模型名以「、」连接、空清单有明确提示。
func TestGatewayGuideRows(t *testing.T) {
	rows := gatewayGuideRows(17890, "tok123456", []string{"my-code", "test"})
	got := map[string]string{}
	for _, r := range rows {
		got[r.label] = r.value
	}
	want := map[string]string{
		"地址":               "http://127.0.0.1:17890",
		"Anthropic":        "http://127.0.0.1:17890/v1/messages",
		"OpenAI Chat":      "http://127.0.0.1:17890/v1/chat/completions",
		"OpenAI Responses": "http://127.0.0.1:17890/v1/responses",
		"令牌":               "tok123****",
		"模型名":              "my-code、test",
	}
	for label, w := range want {
		if got[label] != w {
			t.Errorf("%s = %q, want %q", label, got[label], w)
		}
	}
	if !strings.HasPrefix(got["认证"], "Authorization: Bearer") {
		t.Errorf("认证 = %q", got["认证"])
	}
}

// TestGatewayGuideRowsEmptyModels 空模型清单时给出明确指引，不渲染空行。
func TestGatewayGuideRowsEmptyModels(t *testing.T) {
	rows := gatewayGuideRows(17890, "", nil)
	for _, r := range rows {
		if r.label == "模型名" && !strings.Contains(r.value, "逻辑模型") {
			t.Fatalf("空模型清单应提示先创建逻辑模型，got %q", r.value)
		}
	}
}
