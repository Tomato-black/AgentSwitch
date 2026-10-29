package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const configExportVersion = 1

// ExportSummary 是一次配置导出的结果。
type ExportSummary struct {
	Path                                   string
	Channels, LogicalModels, AccessConfigs int
}

// ImportPreview 是导入前的预览：计数、同名冲突与安全提示。
type ImportPreview struct {
	Version                                int
	Channels, LogicalModels, AccessConfigs int
	Conflicts, Warnings                    []string
}

// ImportSummary 是实际导入结果：只统计新增，冲突/缺失记入 Skipped/Errors。
type ImportSummary struct {
	AddedChannels, AddedLogicalModels, AddedAccessConfigs int
	Skipped, Errors                                       []string
}

type exportBundle struct {
	Version         int                          `json:"version"`
	ExportedAt      time.Time                    `json:"exported_at"`
	AppVersion      string                       `json:"app_version"`
	ContainsSecrets bool                         `json:"contains_secrets"`
	Channels        []exportChannel              `json:"channels"`
	LogicalModels   []exportLogicalModel         `json:"logical_models"`
	AccessConfigs   []exportAccessConfig         `json:"access_configs"`
	GlobalEnv       map[string]map[string]string `json:"global_env,omitempty"`
}

type exportChannel struct {
	Name         string   `json:"name"`
	Protocol     string   `json:"protocol"`
	BaseURL      string   `json:"base_url"`
	PresetID     string   `json:"preset_id,omitempty"`
	Enabled      bool     `json:"enabled"`
	AllowPrivate bool     `json:"allow_private"`
	Models       []string `json:"models,omitempty"`
}

type exportLogicalModel struct {
	Name                  string         `json:"name"`
	Description           string         `json:"description,omitempty"`
	DisplayName           string         `json:"display_name,omitempty"`
	ContextWindow         int64          `json:"context_window,omitempty"`
	ReasoningLevels       []string       `json:"reasoning_levels,omitempty"`
	DefaultReasoningLevel string         `json:"default_reasoning_level,omitempty"`
	Enabled               bool           `json:"enabled"`
	Targets               []exportTarget `json:"targets"`
}

type exportTarget struct {
	Channel  string `json:"channel"`
	Model    string `json:"model"`
	Priority int    `json:"priority"`
}

type exportAccessConfig struct {
	AppType      string          `json:"app_type"`
	Name         string          `json:"name"`
	LogicalModel string          `json:"logical_model"`
	Settings     json.RawMessage `json:"settings"`
}

// ExportConfig 导出版本化配置 JSON；默认不含渠道 Key。
func (g Gateway) ExportConfig(path string) (ExportSummary, error) {
	bundle, sum, err := g.buildExport()
	if err != nil {
		return sum, err
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return sum, err
		}
		path = filepath.Join(home, ".agentswitch", "exports",
			fmt.Sprintf("agentswitch-config-%d.json", time.Now().UnixMilli()))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return sum, err
	}
	b, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return sum, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return sum, err
	}
	sum.Path = path
	return sum, nil
}

func (g Gateway) buildExport() (exportBundle, ExportSummary, error) {
	st := g.A.Store()
	bundle := exportBundle{
		Version: configExportVersion, ExportedAt: time.Now(),
		AppVersion: g.A.State().Version, ContainsSecrets: false,
		GlobalEnv: map[string]map[string]string{},
	}
	var sum ExportSummary

	channels, err := st.Channels().List()
	if err != nil {
		return bundle, sum, err
	}
	chNames := map[string]string{}
	for _, ch := range channels {
		chNames[ch.ID] = ch.Name
		models, _ := st.ChannelModels().ListByChannel(ch.ID)
		names := make([]string, 0, len(models))
		for _, m := range models {
			names = append(names, m.ModelName)
		}
		bundle.Channels = append(bundle.Channels, exportChannel{
			Name: ch.Name, Protocol: ch.Protocol, BaseURL: ch.BaseURL,
			PresetID: ch.PresetID, Enabled: ch.Enabled, AllowPrivate: ch.AllowPrivateUpstream,
			Models: names,
		})
	}
	sum.Channels = len(bundle.Channels)

	models, err := st.LogicalModels().List()
	if err != nil {
		return bundle, sum, err
	}
	lmNames := map[string]string{}
	for _, lm := range models {
		lmNames[lm.ID] = lm.Name
		targets, _ := st.LogicalModelTargets().ListByModel(lm.ID)
		tg := make([]exportTarget, 0, len(targets))
		for _, t := range targets {
			name := chNames[t.ChannelID]
			if name == "" {
				continue
			}
			tg = append(tg, exportTarget{Channel: name, Model: t.ModelName, Priority: t.Priority})
		}
		bundle.LogicalModels = append(bundle.LogicalModels, exportLogicalModel{
			Name: lm.Name, Description: lm.Description, DisplayName: lm.DisplayName,
			ContextWindow: lm.ContextWindow, ReasoningLevels: lm.ReasoningLevels,
			DefaultReasoningLevel: lm.DefaultReasoningLevel, Enabled: lm.Enabled, Targets: tg,
		})
	}
	sum.LogicalModels = len(bundle.LogicalModels)

	for _, app := range []string{"claude", "codex", "claude_desktop"} {
		cs, err := st.AccessConfigs().ListByApp(app)
		if err != nil {
			return bundle, sum, err
		}
		for _, c := range cs {
			settings := json.RawMessage(c.Settings)
			if !json.Valid(settings) {
				settings = json.RawMessage("{}")
			}
			bundle.AccessConfigs = append(bundle.AccessConfigs, exportAccessConfig{
				AppType: c.AppType, Name: c.Name, LogicalModel: lmNames[c.LogicalModelID],
				Settings: settings,
			})
		}
	}
	sum.AccessConfigs = len(bundle.AccessConfigs)

	for _, app := range []string{"claude", "codex"} {
		raw, err := st.Settings().Get("access.global." + app)
		if err != nil || raw == "" {
			continue
		}
		var env map[string]string
		if json.Unmarshal([]byte(raw), &env) == nil && len(env) > 0 {
			bundle.GlobalEnv[app] = env
		}
	}
	return bundle, sum, nil
}

// PreviewImport 解析导出文件并计算冲突/安全提示，不写库。
func (g Gateway) PreviewImport(path string) (ImportPreview, error) {
	bundle, err := loadConfigBundle(path)
	if err != nil {
		return ImportPreview{}, err
	}
	st := g.A.Store()
	pre := ImportPreview{
		Version: bundle.Version, Channels: len(bundle.Channels),
		LogicalModels: len(bundle.LogicalModels), AccessConfigs: len(bundle.AccessConfigs),
	}
	existingCh := map[string]bool{}
	if chs, err := st.Channels().List(); err == nil {
		for _, ch := range chs {
			existingCh[ch.Name] = true
		}
	}
	for _, ch := range bundle.Channels {
		if existingCh[ch.Name] {
			pre.Conflicts = append(pre.Conflicts, "渠道「"+ch.Name+"」已存在")
		}
	}
	existingLM := map[string]bool{}
	if lms, err := st.LogicalModels().List(); err == nil {
		for _, lm := range lms {
			existingLM[lm.Name] = true
		}
	}
	for _, lm := range bundle.LogicalModels {
		if existingLM[lm.Name] {
			pre.Conflicts = append(pre.Conflicts, "逻辑模型「"+lm.Name+"」已存在")
		}
	}
	for _, app := range []string{"claude", "codex", "claude_desktop"} {
		if cs, err := st.AccessConfigs().ListByApp(app); err == nil {
			for _, c := range cs {
				for _, ec := range bundle.AccessConfigs {
					if ec.AppType == app && ec.Name == c.Name {
						pre.Conflicts = append(pre.Conflicts, "接入配置「"+c.Name+"」（"+app+"）已存在")
					}
				}
			}
		}
	}
	if len(bundle.Channels) > 0 {
		pre.Warnings = append(pre.Warnings, "渠道 Key 不在导出文件中，导入后需重新填写。")
	}
	if len(bundle.AccessConfigs) > 0 {
		pre.Warnings = append(pre.Warnings, "接入配置可能包含自定义 env，请检查后再导入/分享。")
	}
	return pre, nil
}

// ApplyImport 只新增不覆盖：同名渠道/逻辑模型/接入配置跳过并记入 Skipped。
func (g Gateway) ApplyImport(path string) (ImportSummary, error) {
	bundle, err := loadConfigBundle(path)
	if err != nil {
		return ImportSummary{}, err
	}
	st := g.A.Store()
	var res ImportSummary
	existingCh := map[string]string{}
	if chs, err := st.Channels().List(); err == nil {
		for _, ch := range chs {
			existingCh[ch.Name] = ch.ID
		}
	}
	channelIDs := map[string]string{}
	for _, ec := range bundle.Channels {
		if _, ok := existingCh[ec.Name]; ok {
			res.Skipped = append(res.Skipped, "渠道「"+ec.Name+"」已存在")
			channelIDs[ec.Name] = existingCh[ec.Name]
			continue
		}
		v, err := (Channels{A: g.A}).Create(ec.PresetID, ec.Name, ec.BaseURL, ec.Protocol, "", ec.Enabled, ec.AllowPrivate)
		if err != nil {
			res.Errors = append(res.Errors, "渠道「"+ec.Name+"」："+err.Error())
			continue
		}
		channelIDs[ec.Name] = v.ID
		if len(ec.Models) > 0 {
			if err := (Channels{A: g.A}).SetSuggestions(v.ID, ec.Models); err != nil {
				res.Errors = append(res.Errors, "渠道「"+ec.Name+"」模型建议："+err.Error())
			}
		}
		res.AddedChannels++
	}

	existingLM := map[string]string{}
	if lms, err := st.LogicalModels().List(); err == nil {
		for _, lm := range lms {
			existingLM[lm.Name] = lm.ID
		}
	}
	modelIDs := map[string]string{}
	for _, el := range bundle.LogicalModels {
		if _, ok := existingLM[el.Name]; ok {
			res.Skipped = append(res.Skipped, "逻辑模型「"+el.Name+"」已存在")
			modelIDs[el.Name] = existingLM[el.Name]
			continue
		}
		targets := make([]TargetSpec, 0, len(el.Targets))
		missing := ""
		for _, t := range el.Targets {
			id := channelIDs[t.Channel]
			if id == "" {
				missing = t.Channel
				break
			}
			targets = append(targets, TargetSpec{ChannelID: id, ModelName: t.Model, Priority: t.Priority})
		}
		if missing != "" {
			res.Errors = append(res.Errors, "逻辑模型「"+el.Name+"」目标渠道「"+missing+"」不存在，已跳过")
			continue
		}
		if len(targets) == 0 {
			res.Errors = append(res.Errors, "逻辑模型「"+el.Name+"」没有目标，已跳过")
			continue
		}
		v, err := (LogicalModels{A: g.A}).Create(el.Name, el.Description, ModelMeta{
			DisplayName: el.DisplayName, ContextWindow: el.ContextWindow,
			ReasoningLevels: el.ReasoningLevels, DefaultReasoningLevel: el.DefaultReasoningLevel,
		}, targets)
		if err != nil {
			res.Errors = append(res.Errors, "逻辑模型「"+el.Name+"」："+err.Error())
			continue
		}
		modelIDs[el.Name] = v.ID
		res.AddedLogicalModels++
	}

	existingAccess := map[string]bool{}
	for _, app := range []string{"claude", "codex", "claude_desktop"} {
		if cs, err := st.AccessConfigs().ListByApp(app); err == nil {
			for _, c := range cs {
				existingAccess[app+"\x00"+c.Name] = true
			}
		}
	}
	for _, ea := range bundle.AccessConfigs {
		key := ea.AppType + "\x00" + ea.Name
		if existingAccess[key] {
			res.Skipped = append(res.Skipped, "接入配置「"+ea.Name+"」已存在")
			continue
		}
		modelID := modelIDs[ea.LogicalModel]
		if modelID == "" {
			res.Errors = append(res.Errors, "接入配置「"+ea.Name+"」绑定的逻辑模型「"+ea.LogicalModel+"」不存在，已跳过")
			continue
		}
		settings := string(ea.Settings)
		if settings == "" {
			settings = "{}"
		}
		if _, err := (AccessConfigs{A: g.A}).Create(ea.AppType, ea.Name, modelID, settings); err != nil {
			res.Errors = append(res.Errors, "接入配置「"+ea.Name+"」："+err.Error())
			continue
		}
		res.AddedAccessConfigs++
	}

	for app, env := range bundle.GlobalEnv {
		if err := (AccessConfigs{A: g.A}).SaveGlobalConfig(app, env); err != nil {
			res.Errors = append(res.Errors, "全局配置（"+app+"）："+err.Error())
		}
	}
	return res, nil
}

func loadConfigBundle(path string) (exportBundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return exportBundle{}, err
	}
	var bundle exportBundle
	if err := json.Unmarshal(b, &bundle); err != nil {
		return exportBundle{}, fmt.Errorf("配置 JSON 无效：%w", err)
	}
	if bundle.Version != configExportVersion {
		return exportBundle{}, fmt.Errorf("不支持的配置版本 %d（当前支持 %d）", bundle.Version, configExportVersion)
	}
	return bundle, nil
}
