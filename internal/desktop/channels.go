// channels.go 占位：旧多密钥渠道页依赖的 AddKeys/KeyCount/PoolStatus 等已随
// 渠道单 Key 重构退役。本 Task 只保留仍被消费的纯助手（预设解析、遥测统计、
// 分段控件），页面本体由 Task 14 重写、死代码由 Task 16 清理。
package desktop

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/telemetry"
)

// presetLabels 供 Select 展示；返回 labels 与 label→id 映射。
func presetLabels() ([]string, map[string]string) {
	ps := provider.Presets()
	labels := make([]string, 0, len(ps))
	byLabel := map[string]string{}
	for _, p := range ps {
		labels = append(labels, p.Label)
		byLabel[p.Label] = p.ID
	}
	return labels, byLabel
}

func presetOf(typeID string) provider.Preset {
	p, _ := provider.LookupPreset(typeID)
	return p
}

// resolveTypeID 把表单里的类型值解析回预设 id：mygo Select 把选中的选项
// 原文（label）写回 *selected，而初始化/载入所选/测试直赋的是 id——
// label 经映射还原为 id，其余（含裸 id）原样返回。预填、描述与提交共用。
func resolveTypeID(byLabel map[string]string, v string) string {
	if id, ok := byLabel[v]; ok {
		return id
	}
	return v
}

// channelStats 统计遥测环里该渠道名的近况：ok=2xx、fail=Status==0||>=400
// （与概览 recentStats 的失败口径一致），按 rec.Channel（渠道名）过滤。
func channelStats(recs []telemetry.RequestRecord, channel string) (ok, fail int) {
	for _, r := range recs {
		if r.Channel != channel {
			continue
		}
		switch {
		case r.Status == 0 || r.Status >= 400:
			fail++
		case r.Status >= 200 && r.Status < 300:
			ok++
		}
	}
	return ok, fail
}

// keyStats 统计遥测环里该渠道+密钥名的近况：口径同 channelStats。
func keyStats(recs []telemetry.RequestRecord, channel, account string) (ok, fail int) {
	for _, r := range recs {
		if r.Channel != channel || r.Account != account {
			continue
		}
		switch {
		case r.Status == 0 || r.Status >= 400:
			fail++
		case r.Status >= 200 && r.Status < 300:
			ok++
		}
	}
	return ok, fail
}

// segmented 是自组分段控件（spec §4）：SegmentedBase 的框架默认选中片取
// pressedColor——浅色下是 Background 白片，白底隐身（toggle.go:45-54），
// 故选中片显式上应用级选中灰 selectedColor，Track 上 SurfacePressed。
func segmented(c *ui.Context, t *ui.Theme, selected *int, labels []string) {
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.Background(t.SurfacePressed).Radius(8).Padding(3).Gap(3).Children(func() {
		for i, label := range labels {
			seg := parts.Segment(i)
			if i == *selected {
				seg.Background(selectedColor(t))
			}
			seg.Radius(6).Padding(6, 12).Center().Children(func() {
				ui.Text(c, label).SingleLine()
			})
		}
	})
}

// labelsOf 保证当前值在 Select 选项内（mygo Select 对未知值的行为未验证，
// 不假定——把当前 typeID 的 label 兜底拼接进选项）。
func labelsOf(labels []string, byLabel map[string]string, typeID string) []string {
	for _, l := range labels {
		if byLabel[l] == typeID {
			return labels
		}
	}
	p, _ := provider.LookupPreset(typeID)
	if p.ID != "" {
		return append([]string{p.Label}, labels...)
	}
	return labels
}
