package accesskit

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
)

// TabTitles 返回接入页四个 tab 的标题（字符串快照锚点）：三个受管
// Agent + 连接指南（第三方系统照指南手工接入，AgentSwitch 不写其配置）。
func TabTitles() (claude, codex, desktop, guide string) {
	return "Claude Code", "Codex", "Claude Desktop", "连接指南"
}

// TabTitle 返回 Agent 页签的显示名（未知 app 原样返回）。
func TabTitle(app string) string {
	claude, codex, desktop, guide := TabTitles()
	switch app {
	case "claude":
		return claude
	case "codex":
		return codex
	case "claude_desktop":
		return desktop
	case "guide":
		return guide
	}
	return app
}

// TabIndex 返回 Agent 页签下标（未知 app 落 0=Claude Code）。
func TabIndex(app string) int {
	switch app {
	case "codex":
		return 1
	case "claude_desktop":
		return 2
	case "guide":
		return 3
	default:
		return 0
	}
}

// TabApp 返回页签下标对应的 app 类型（越界落 "claude"）。
func TabApp(i int) string {
	switch i {
	case 1:
		return "codex"
	case 2:
		return "claude_desktop"
	case 3:
		return "guide"
	default:
		return "claude"
	}
}

// CloseEditor 关闭接入编辑器并回到列表；经 State.Go 走统一的未保存确认。
func CloseEditor(st *state.State) {
	st.Go("access", func() {
		st.Access.Form = state.AccessForm{}
		st.Access.DesktopForm = state.DesktopForm{}
	})
}

// RowBadge 返回配置行「使用中」徽标文本（非 current 为空串）。
func RowBadge(isCurrent bool) string {
	if isCurrent {
		return "使用中"
	}
	return ""
}

// ValidateGlobalRows 校验全局配置编辑行：任一行的键/值只填一半都算误操作
// （空行同样拦截），提示用户补全或删除该行；不再静默丢弃。
func ValidateGlobalRows(rows [][2]string) error {
	for i, r := range rows {
		k, v := strings.TrimSpace(r[0]), strings.TrimSpace(r[1])
		if k == "" || v == "" {
			return fmt.Errorf("第 %d 行：键和值都需要填写（或删除该行）", i+1)
		}
	}
	return nil
}

// OverridesFromRows 把键值行转成 overrides map（空键/空值都剔除
// ——半填行是误操作，清空请删行；同键后者胜——与合成层 overrides 优先
// 语义一致）。用于全局配置卡。
func OverridesFromRows(rows [][2]string) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		k := strings.TrimSpace(r[0])
		v := strings.TrimSpace(r[1])
		if k == "" || v == "" {
			continue
		}
		out[k] = r[1]
	}
	return out
}

// ModelLabels 返回 enabled 逻辑模型名列表（编辑器 Select 数据源）。
func ModelLabels(models []bridge.ModelView) []string {
	var out []string
	for _, m := range models {
		if m.Enabled {
			out = append(out, m.Name)
		}
	}
	return out
}

// ModelIDByLabel 按名映射回逻辑模型 ID（未命中返回空串；
// disabled 项也参与命中——编辑旧配置时回显不应丢引用）。
func ModelIDByLabel(models []bridge.ModelView, label string) string {
	for _, m := range models {
		if m.Name == label {
			return m.ID
		}
	}
	return ""
}

// GlobalRows 把全局 env map 转成有序编辑行（键字典序，UI 稳定渲染）。
func GlobalRows(env map[string]string) [][2]string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][2]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, [2]string{k, env[k]})
	}
	return rows
}

// GatewayGate 返回网关是否可用（应用/启动类操作的前提）与原因。网关运行
// 状态取实际服务状态（Status().State），不是按钮状态（架构不变量）。
func GatewayGate(gw bridge.Gateway) (bool, string) {
	st := gw.Status()
	if st.State != "running" && st.State != "degraded" {
		return false, "网关未运行——请先在侧栏底条启动网关"
	}
	return true, ""
}

// ModelSummary 返回接入配置行的模型摘要：Codex 且文档声明多个模型时显示
// 「首模型 等 N 个」（默认模型即首项），其余情况回退绑定的逻辑模型名。
func ModelSummary(app, settingsJSON, fallback string) string {
	if app != "codex" {
		return fallback
	}
	doc, err := DocObject(settingsJSON)
	if err != nil {
		return fallback
	}
	models, ok := DocModels(doc)
	if !ok || len(models) == 0 {
		return fallback
	}
	if len(models) == 1 {
		return models[0]
	}
	return fmt.Sprintf("%s 等 %d 个", models[0], len(models))
}

// ApplyAsync 把接入配置的「应用」移到后台任务（P0-2）：Codex 应用可能运行
// `codex debug models` 或写多个文件，不能占用渲染帧。应用中的行显示
// BusyRow，其余行的应用按钮禁用；完成后回主线程写 Access.Msg。
func ApplyAsync(st *state.State, acc bridge.AccessConfigs, v bridge.AccessConfigView) {
	if st.Access.ApplyTask.Running {
		return
	}
	if st.Access.App == "claude" && st.ClaudeCfg.Dirty {
		st.Access.Msg = "Claude Code 设置还有未保存修改；先保存或刷新放弃，再应用接入"
		return
	}
	st.Access.ApplyID = v.ID
	state.RunAsync(st, &st.Access.ApplyTask, "正在应用…", func(context.Context) (string, error) {
		if err := acc.Apply(v.ID); err != nil {
			return "", err
		}
		switch v.AppType {
		case "codex":
			return "已应用「" + v.Name + "」——重启 Codex 后生效（桌面版需完全退出后重新打开）", nil
		case "claude_desktop":
			return "已应用「" + v.Name + "」——重启 Claude Desktop 生效", nil
		default:
			return "已应用「" + v.Name + "」——Agent 下次启动生效", nil
		}
	}, func(msg string, err error) {
		st.Access.ApplyID = ""
		if err != nil {
			st.Access.Msg = "应用失败：" + err.Error()
			return
		}
		st.Access.Msg = msg
		st.ClaudeCfg.Loaded = false
		st.ClaudeCfg.Msg = "接入已更新，设置区已重新加载"
	})
}
