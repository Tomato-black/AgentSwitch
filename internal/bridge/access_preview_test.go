package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"AgentSwitch/internal/storage"
)

// TestPreviewClaudeLiveMergesLiveFile：预览以 live 文件为底合并合成 env 与
// 文档顶层键（保留用户其他键），且不写盘。
func TestPreviewClaudeLiveMergesLiveFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(dir, "settings.json")
	original := `{"permissions":{"allow":["Bash"]},"env":{"FOO":"bar"}}`
	if err := os.WriteFile(livePath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: home}
	out, err := acc.PreviewClaudeLive("lm1", `{"env":{"ANTHROPIC_MODEL":"my-code[1M]"}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bash", "FOO", "ANTHROPIC_BASE_URL", "my-code[1M]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview missing %q: %s", want, out)
		}
	}
	tok, _ := a.Store().Settings().Get("auth.gateway_token")
	if tok == "" || !strings.Contains(out, tok) {
		t.Fatalf("preview should carry gateway token (same as editor)")
	}
	b, _ := os.ReadFile(livePath)
	if string(b) != original {
		t.Fatal("preview must not write live file")
	}
}

// TestPreviewDesktopProfile：预览网关 profile JSON（含网关地址/令牌/角色
// 路由），校验绑定的逻辑模型存在；不写任何文件。
func TestPreviewDesktopProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: home}
	out, err := acc.PreviewDesktopProfile(`{"sonnet":{"model":"my-code","oneM":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"inferenceProvider", "gateway", "inferenceGatewayBaseUrl", "127.0.0.1", "claude-sonnet-5", "supports1m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("profile preview missing %q: %s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatal("preview must not create config files")
	}
	// 未绑定的模型应报「请改绑」。
	if _, err := acc.PreviewDesktopProfile(`{"sonnet":{"model":"gone"}}`); err == nil {
		t.Fatal("unknown model must fail")
	}
}
