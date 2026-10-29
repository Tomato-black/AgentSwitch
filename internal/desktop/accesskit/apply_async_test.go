package accesskit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestApplyAsyncCompletes 覆盖 P0-2：接入「应用」在后台执行，完成后经
// Dispatch 回主线程写结果提示；测试用临时 HOME，不触碰真实配置。
func TestApplyAsyncCompletes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	lm, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a}
	v, err := acc.Create("claude", "cfg", lm.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	st.Dispatch = func(fn func()) { fn(); close(done) }
	accesskit.ApplyAsync(st, acc, v)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("apply did not finish")
	}
	if st.Access.ApplyTask.Running || st.Access.ApplyID != "" {
		t.Fatalf("task state not cleared: %+v id=%q", st.Access.ApplyTask, st.Access.ApplyID)
	}
	if !strings.Contains(st.Access.Msg, "已应用") {
		t.Fatalf("msg = %q", st.Access.Msg)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ANTHROPIC_MODEL") || !strings.Contains(string(b), "my-code") {
		t.Fatalf("live file not written: %s", b)
	}
}
