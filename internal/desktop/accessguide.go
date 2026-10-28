package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// guideRow 是「通用连接信息」卡片的一行：说明 + 值。
type guideRow struct {
	label string
	value string
	// copyValue 非空时该行带复制按钮，复制的是真值（如令牌）；空则只展示。
	copyValue string
}

// maskToken 把令牌渲染为掩码：保留前 6 位，其余以 **** 结尾（与编辑器/诊断
// 的脱敏惯例一致；真值只经复制按钮进剪贴板，不落 UI 文本）。
func maskToken(tok string) string {
	if tok == "" {
		return "（无）"
	}
	if len(tok) <= 6 {
		return "****"
	}
	return tok[:6] + "****"
}

// gatewayGuideRows 生成通用连接信息行（接入页顶部卡片；纯函数便于测试）：
// 任何兼容 Anthropic / OpenAI 协议的客户端照此在目标系统里手工填写即可接入。
// 认证统一 Authorization: Bearer——网关 authorized 只认 Bearer（internal/
// gateway/server.go），Anthropic 入站也不接受 x-api-key。
func gatewayGuideRows(port int, token string, models []string) []guideRow {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	modelLine := strings.Join(models, "、")
	if modelLine == "" {
		modelLine = "（无启用的逻辑模型，请先在逻辑模型页创建）"
	}
	return []guideRow{
		{label: "地址", value: base, copyValue: base},
		{label: "Anthropic", value: base + "/v1/messages", copyValue: base + "/v1/messages"},
		{label: "OpenAI Chat", value: base + "/v1/chat/completions", copyValue: base + "/v1/chat/completions"},
		{label: "OpenAI Responses", value: base + "/v1/responses", copyValue: base + "/v1/responses"},
		{label: "认证", value: "Authorization: Bearer <令牌>（三个端点一致）"},
		{label: "令牌", value: maskToken(token), copyValue: token},
		{label: "模型名", value: modelLine},
	}
}

// guideCard 渲染「通用连接信息」卡片（接入页「连接指南」页签）：网关状态
// 提示 + 连接信息行（值可复制）。用户照卡片内容到目标系统（如 Kun 的
// Settings > Providers）自行填写——AgentSwitch 不写第三方系统的配置。
func (u *uiState) guideCard(c *ui.Context, t *ui.Theme, s softPalette) {
	st := u.svc.Gateway.Status()
	running := st.State == "running" || st.State == "degraded"
	var models []string
	for _, m := range u.svc.Logical.List() {
		if m.Enabled {
			models = append(models, m.Name)
		}
	}
	rows := gatewayGuideRows(st.Port, st.Token, models)
	card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "通用连接信息").Bold()
				ui.Text(c, "任何兼容 Anthropic / OpenAI 协议的客户端（如 Kun 等第三方 Agent）在目标系统里照此填写即可接入").TextColor(t.TextMuted)
			})
			if !running {
				ui.Text(c, "网关未运行——请先在侧栏底条启动网关，下列地址当前不可用").TextColor(t.TextMuted)
			}
			for _, r := range rows {
				row := r
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, row.label).TextColor(t.TextMuted).Width(110)
					ui.Text(c, row.value).Grow(1)
					if row.copyValue != "" {
						if softButton(c, t, s, "gray", "复制").Clicked() {
							mygo.Clipboard.Write(transfer.TextData(row.copyValue))
							u.guideMsg = "已复制：" + row.label
						}
					}
				})
			}
			if u.guideMsg != "" {
				ui.Text(c, u.guideMsg).TextColor(t.TextMuted)
			}
		})
	})
}
