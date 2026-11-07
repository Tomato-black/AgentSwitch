// Package uitest 是页面子包的共享测试夹具：开一个临时 app、构造桥接服务，
// 返回可直接渲染页面函数（func Page(c, st)）的共享状态。只被 _test.go
// 引用，不参与生产构建路径。
package uitest

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
)

// New 返回临时库上的共享状态与 app 句柄（Cleanup 关闭 app）。
func New(t testing.TB) (*state.State, *app.App) {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "d.db"), LogPath: filepath.Join(t.TempDir(), "d.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	svc := state.Services{
		Gateway: bridge.Gateway{A: a},
		// 凭据走 app 的加密存储（库内 AES-256-GCM），Channels.Creds 留空走 a.Creds() 兜底。
		Channels: bridge.Channels{A: a},
		Logical:  &bridge.LogicalModels{A: a},
	}
	st := state.New(svc)
	// 页面测试必须与真实 ~/.claude 隔离，避免读取用户配置。
	st.ClaudeCfg.Home = t.TempDir()
	return st, a
}
