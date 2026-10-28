package channels

import (
	"fmt"
	"strings"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/presets"
	"AgentSwitch/internal/telemetry"
)

// applyPreset 把预设回填进表单（spec §5）：presetID、第一可用协议（openai
// 优先）的 base_url 与建议模型；name 空时回填 DisplayName（已填不覆盖）；
// apiKey 等其余字段不动（预设只带连接默认值）。
func applyPreset(form *state.ChannelForm, p presets.ChannelPreset) {
	form.PresetID = p.ID
	proto, ep, ok := presets.FirstProtocol(p)
	if !ok {
		return
	}
	form.Protocol = proto
	form.BaseURL = ep.BaseURL
	form.Suggestions = strings.Join(ep.DefaultModels, "\n")
	if strings.TrimSpace(form.Name) == "" {
		form.Name = p.DisplayName
	}
}

// switchProtocol 是协议下拉联动（spec §5）：目标协议在预置中→当当前值
// 为空或等于任一协议预设值（未手改）时替换为目标协议预设值；目标协议不在
// 预置中→同条件下清空待手填。空值视为未修改（切走再切回可恢复预设）。
// baseURL 与建议模型各自独立判定，互不牵连。
func switchProtocol(form *state.ChannelForm, p presets.ChannelPreset, newProto string) {
	form.Protocol = newProto
	replace := func(cur string, cands []string, next string) string {
		if cur == "" {
			return next
		}
		for _, c := range cands {
			if cur == c {
				return next
			}
		}
		return cur // 用户已手改，保留
	}
	var baseCands, modelCands []string
	for _, ep := range p.Protocols {
		baseCands = append(baseCands, ep.BaseURL)
		modelCands = append(modelCands, strings.Join(ep.DefaultModels, "\n"))
	}
	ep, has := p.Protocols[newProto]
	nextBase, nextModels := "", ""
	if has {
		nextBase = ep.BaseURL
		nextModels = strings.Join(ep.DefaultModels, "\n")
	}
	form.BaseURL = replace(form.BaseURL, baseCands, nextBase)
	form.Suggestions = replace(form.Suggestions, modelCands, nextModels)
}

// validate 校验表单：缺 name/key/base_url 依次报中文错误；编辑态
// （editID 非空）Key 留空 = 不改，不拦。
func validate(f state.ChannelForm) error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("需要填写渠道名称")
	}
	if f.EditID == "" && strings.TrimSpace(f.APIKey) == "" {
		return fmt.Errorf("需要填写 API Key")
	}
	if strings.TrimSpace(f.BaseURL) == "" {
		return fmt.Errorf("需要填写 Base URL")
	}
	return nil
}

// parseSuggestions 把建议模型多行/逗号分隔文本拆成列表（去空白、空串与重复，
// 保序）；SetSuggestions 侧亦有剔除，双保险。
func parseSuggestions(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// referencedBy 列出目标链引用了该渠道的逻辑模型名（删除警告用）；同一模型
// 多次引用只记一次，顺序即 List 顺序。
func referencedBy(models []bridge.ModelView, channelID string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range models {
		if seen[m.Name] {
			continue
		}
		for _, t := range m.Targets {
			if t.ChannelID == channelID {
				names = append(names, m.Name)
				seen[m.Name] = true
				break
			}
		}
	}
	return names
}

// channelRefNames 是 referencedBy 的桌面侧取数（Services.Logical 接线，
// 未挂时返回空）。
func refNames(st *state.State, channelID string) []string {
	if st.Svc.Logical == nil {
		return nil
	}
	return referencedBy(st.Svc.Logical.List(), channelID)
}

// channelStats 统计遥测环里该渠道名的近况：ok=2xx、fail=Status==0||>=400，
// 按 rec.Channel（渠道名）过滤。
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

// openChannelNew 进新建页并复位表单（预设网格从头选）。
func openNew(st *state.State) {
	resetForm(st)
	st.Channels.View = "new"
}

// openChannelDetail 进详情页并回显表单：Key 框恒空（留空=不改），建议模型
// 回显到多行文本。
func openDetail(st *state.State, id string) {
	st.Channels.View = "detail"
	st.Channels.DetailID = id
	st.Channels.TestMsg = ""
	st.Channels.KeyInput = ""
	st.Channels.SuggText = ""
	st.Channels.Form = state.ChannelForm{EditID: id}
	d, err := st.Svc.Channels.Get(id)
	if err != nil {
		st.Channels.Form.Msg = "渠道不存在或已删除"
		return
	}
	st.Channels.Form = state.ChannelForm{
		EditID: id, PresetID: d.PresetID, Protocol: d.Protocol,
		Name: d.Name, BaseURL: d.BaseURL,
		Enabled: d.Enabled, AllowPrivate: d.AllowPrivate,
	}
	st.Channels.SuggText = strings.Join(d.Suggestions, "\n")
}

// detailProtocolSwitched 是详情页协议切换联动：与新建页共用 switchProtocol
// 的手改保护语义，唯一差别是详情页的建议模型承载在 chSuggText 而非
// chForm.suggestions——联动前同步进来、联动后写回去，保证「未手改才替换」
// 的判定基于用户实际看到的文本。presetID 查表（ById）由调用方完成。
func detailProtocolSwitched(st *state.State, p presets.ChannelPreset) {
	st.Channels.Form.Suggestions = st.Channels.SuggText
	switchProtocol(&st.Channels.Form, p, st.Channels.Form.Protocol)
	st.Channels.SuggText = st.Channels.Form.Suggestions
}

// resetChannelForm 复位新建表单：协议默认 openai、启用；预设字段清空待选。
func resetForm(st *state.State) {
	st.Channels.Form = state.ChannelForm{Protocol: "openai", Enabled: true}
	st.Channels.TestMsg = ""
	st.Channels.KeyInput = ""
	st.Channels.SuggText = ""
}

// createChannel 落「创建渠道」：校验 → Create（单 Key）→ 建议模型随建随写
// → 进详情（核对 Key 与建议）。
func create(st *state.State) {
	if err := validate(st.Channels.Form); err != nil {
		st.Channels.Form.Msg = err.Error()
		return
	}
	v, err := st.Svc.Channels.Create(st.Channels.Form.PresetID, st.Channels.Form.Name, st.Channels.Form.BaseURL,
		st.Channels.Form.Protocol, st.Channels.Form.APIKey, true, st.Channels.Form.AllowPrivate)
	if err != nil {
		st.Channels.Form.Msg = err.Error()
		return
	}
	if sugg := parseSuggestions(st.Channels.Form.Suggestions); len(sugg) > 0 {
		_ = st.Svc.Channels.SetSuggestions(v.ID, sugg)
	}
	openDetail(st, v.ID)
}

// saveChannelDetail 落详情「保存」：校验 → Update（Key 框留空=不改；协议
// 可编辑，2026-10-28 修订）→ 建议模型全量替换。
func saveDetail(st *state.State) {
	if err := validate(st.Channels.Form); err != nil {
		st.Channels.TestMsg = err.Error()
		return
	}
	if err := st.Svc.Channels.Update(st.Channels.Form.EditID, st.Channels.Form.Name, st.Channels.Form.BaseURL,
		st.Channels.Form.Protocol, st.Channels.KeyInput, st.Channels.Form.Enabled, st.Channels.Form.AllowPrivate); err != nil {
		st.Channels.TestMsg = err.Error()
		return
	}
	if err := st.Svc.Channels.SetSuggestions(st.Channels.Form.EditID, parseSuggestions(st.Channels.SuggText)); err != nil {
		st.Channels.TestMsg = err.Error()
		return
	}
	st.Channels.KeyInput = ""
	st.Channels.TestMsg = "已保存"
}

// testChannelDetail 落「测试连通」：结果进 chTestMsg 信息位。
func testDetail(st *state.State) {
	res := st.Svc.Channels.Test(st.Channels.DetailID)
	if res.OK {
		st.Channels.TestMsg = "连通正常 (" + res.Stage + ")"
		return
	}
	st.Channels.TestMsg = res.Stage + " 失败: " + res.Error
}

// applyFetchedModels 把拉取结果写进指定文本框（覆盖）并给信息位；空列表
// 与错误都不覆盖（用户已填内容优先保留）。
func applyFetchedModels(dst *string, msg *string, ids []string, err error, suffix string) {
	if err != nil {
		*msg = "拉取模型失败: " + err.Error()
		return
	}
	if len(ids) == 0 {
		*msg = "上游未返回模型"
		return
	}
	*dst = strings.Join(ids, "\n")
	*msg = fmt.Sprintf("已从上游拉取 %d 个模型（%s）", len(ids), suffix)
}

// fetchChannelModels 落「拉取模型」（详情页）：用已保存渠道的 Base URL/协议/
// Key 拉上游模型列表，覆盖建议文本框；结果经 applyFetchedModels 统一处理。
func fetchModels(st *state.State) {
	ids, err := st.Svc.Channels.FetchModels(st.Channels.DetailID)
	applyFetchedModels(&st.Channels.SuggText, &st.Channels.TestMsg, ids, err, "保存后生效")
}

// fetchChannelModelsDraft 同上，但用新建表单的 baseURL/protocol/key——渠道
// 尚未入库，没有可读的 Key，只能走草稿拉取。
func fetchModelsDraft(st *state.State) {
	f := &st.Channels.Form
	ids, err := st.Svc.Channels.FetchModelsFromDraft(f.BaseURL, f.Protocol, f.APIKey, f.AllowPrivate)
	applyFetchedModels(&f.Suggestions, &f.Msg, ids, err, "创建后生效")
}
