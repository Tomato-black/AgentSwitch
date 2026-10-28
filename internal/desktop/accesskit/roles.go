package accesskit

import (
	"strings"

	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/modelid"
)

// claudeRole 是 Claude 模型映射一行（spec 2026-10-19 §3.2）：角色 → 文档
// env 键。NameKey 为空的角色无「显示名称」列（Subagent 不进 /model 菜单）。
type claudeRole struct{ Label, EnvKey, NameKey string }

var claudeRoles = []claudeRole{
	{"Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"},
	{"Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"},
	{"Fable", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL_NAME"},
	{"Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME"},
	{"Subagent", "CLAUDE_CODE_SUBAGENT_MODEL", ""},
}

// FollowModel 是角色下拉「未设置」的显示值（回落主模型）。
const FollowModel = "（跟随主模型）"

// ClaudeRoles 返回 5 角色固定顺序（与 cc-switch 对齐）。
func ClaudeRoles() []claudeRole { return claudeRoles }

// roleIndexByEnvKey 返回角色下标（测试与补丁用）。
func roleIndexByEnvKey(envKey string) int {
	for i, r := range claudeRoles {
		if r.EnvKey == envKey {
			return i
		}
	}
	return -1
}

// SyncRolesFromDoc JSON→表单回填：解析成功才覆盖角色行（失败保留旧状态，
// cc-switch 同款「编辑到一半不闪烁」）。
func SyncRolesFromDoc(f *state.AccessForm) {
	env, err := DocEnvStrings(f.DocJSON)
	if err != nil {
		return
	}
	_, f.MainOneM = modelid.SplitOneM(env["ANTHROPIC_MODEL"])
	for len(f.RoleModels) < len(claudeRoles) {
		f.RoleModels = append(f.RoleModels, FollowModel)
		f.RoleNames = append(f.RoleNames, "")
		f.RoleOneM = append(f.RoleOneM, false)
	}
	for i, r := range claudeRoles {
		name, oneM := modelid.SplitOneM(env[r.EnvKey])
		if name == "" {
			f.RoleModels[i] = FollowModel
		} else {
			f.RoleModels[i] = name
		}
		f.RoleOneM[i] = oneM
		if r.NameKey != "" {
			f.RoleNames[i] = env[r.NameKey]
		}
	}
}

// PatchRole 表单→JSON 补丁：角色行变化写回文档 env（空/跟随=删键；
// 1M=值尾拼 "[1M]" 字面量）。
func PatchRole(f *state.AccessForm, i int) {
	r := claudeRoles[i]
	val := f.RoleModels[i]
	if val == FollowModel {
		val = ""
	}
	if val != "" {
		val = modelid.AppendOneM(val, f.RoleOneM[i])
	}
	if raw, err := DocSetEnv(f.DocJSON, r.EnvKey, val); err == nil {
		f.DocJSON = raw
	}
	if r.NameKey != "" {
		if raw, err := DocSetEnv(f.DocJSON, r.NameKey, strings.TrimSpace(f.RoleNames[i])); err == nil {
			f.DocJSON = raw
		}
	}
}

// PatchMainModel 主模型 1M 勾选 → 文档 env 补丁（ANTHROPIC_MODEL =
// 逻辑模型名[1M]；未勾选写纯名，保存时被归一化剔除、保持继承）。
func PatchMainModel(f *state.AccessForm) {
	val := f.ModelLabel
	if val == "" {
		return
	}
	val = modelid.AppendOneM(val, f.MainOneM)
	if raw, err := DocSetEnv(f.DocJSON, "ANTHROPIC_MODEL", val); err == nil {
		f.DocJSON = raw
	}
}
