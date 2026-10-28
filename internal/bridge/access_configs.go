package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/storage"
)

// accessPlatform 是接入相关平台分支的注入点（默认当前平台）：测试在
// 非 Windows 主机上替换它，覆盖 Windows 路径/启动器/终端分派。
var accessPlatform = runtime.GOOS

// AccessConfigs 是接入配置管理面（agent-access-config spec）：把
// 「逻辑模型 × Agent」的配置合成与投影整理为桌面可调用的服务。
// 本文件只落合成纯函数 composeAccessEnv（Task 5/10 的 Apply/Export
// 复用）；Home 为 "" 时业务方法回落 os.UserHomeDir()（路径方法属后续任务）。
type AccessConfigs struct {
	A    *app.App
	Home string
}

// AccessConfigView 是接入配置卡片视图（UI 接线契约，字段名勿改）；
// LogicalModelName 为绑定逻辑模型的对外名（解析失败留空串）。
// Settings 为整份配置文档原文（含归一化后的 env）。
type AccessConfigView struct {
	ID, Name, AppType, LogicalModelID, LogicalModelName string
	Settings                                            string
	IsCurrent                                           bool
}

func (s AccessConfigs) view(c storage.AccessConfig) AccessConfigView {
	v := AccessConfigView{ID: c.ID, Name: c.Name, AppType: c.AppType,
		LogicalModelID: c.LogicalModelID, Settings: c.Settings, IsCurrent: c.IsCurrent}
	// 逐条解析逻辑模型名；Get 失败（如已删模型）留空串即可。
	if lm, err := s.A.Store().LogicalModels().Get(c.LogicalModelID); err == nil {
		v.LogicalModelName = lm.Name
	}
	return v
}

// List 返回该 app 的接入配置列表（含 LogicalModelName 解析与 IsCurrent 标记）。
func (s AccessConfigs) List(appType string) []AccessConfigView {
	if validateAccessAppType(appType) != nil {
		return nil
	}
	cs, err := s.A.Store().AccessConfigs().ListByApp(appType)
	if err != nil {
		return nil
	}
	out := make([]AccessConfigView, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.view(c))
	}
	return out
}

// Create 建接入配置：appType 白名单、name 非空（TrimSpace）、logicalModelID
// 必须存在；settingsJSON 经 prepareDoc 归一化后入库（锁定键剔除、未定制键
// 剔除、文档顶层键保留）；同 app 重名译「配置名已存在」。审计行绝不记录
// env/settings 值或 token（spec §9 脱敏）。
func (s AccessConfigs) Create(appType, name, logicalModelID, settingsJSON string) (AccessConfigView, error) {
	if err := validateAccessAppType(appType); err != nil {
		return AccessConfigView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AccessConfigView{}, fmt.Errorf("配置名不能为空")
	}
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return AccessConfigView{}, fmt.Errorf("逻辑模型不存在")
		}
		return AccessConfigView{}, err
	}
	raw, err := s.prepareDoc(appType, lm.Name, settingsJSON)
	if err != nil {
		return AccessConfigView{}, err
	}
	id := storage.NewID()
	saved, err := s.A.Store().AccessConfigs().Save(storage.AccessConfig{
		ID: id, AppType: appType, Name: name, LogicalModelID: logicalModelID, Settings: raw,
	})
	if err != nil {
		return AccessConfigView{}, uniqueErr(err, "配置名已存在")
	}
	s.A.Log().Info("access config created", "id", id, "app", appType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 新配置即落持久文件，其余派生文件同步刷新（spec「保存/更新时同步生成」）
	return s.view(saved), nil
}

// Update 改配置字段并保 ID（非 Remove+Create）：直接 repo.Save 覆盖，
// 保留原 app_type / is_current / sort_order；settingsJSON 经 prepareDoc
// 归一化后覆盖 Settings（语义同 Create）。
func (s AccessConfigs) Update(id, name, logicalModelID, settingsJSON string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("配置名不能为空")
	}
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("逻辑模型不存在")
		}
		return err
	}
	raw, err := s.prepareDoc(c.AppType, lm.Name, settingsJSON)
	if err != nil {
		return err
	}
	c.Name, c.LogicalModelID = name, logicalModelID
	c.Settings = raw
	if _, err := s.A.Store().AccessConfigs().Save(c); err != nil {
		return uniqueErr(err, "配置名已存在")
	}
	s.A.Log().Info("access config updated", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 覆盖项/模型变了，全部派生文件同步刷新（spec「保存/更新时同步生成」）
	return nil
}

// Remove 直接删行，**不触碰任何 live 文件**（删除使用中配置时 live 保持原样，
// 由用户自行决定后续处理）；is_current 行删除后 current 自然消失（部分唯一
// 索引允许）。删除后重写其余配置的持久文件（被删配置的持久文件保留——
// 用户可能仍持有其启动命令）。
func (s AccessConfigs) Remove(id string) error {
	if err := s.A.Store().AccessConfigs().Delete(id); err != nil {
		s.A.Log().Error("access config remove failed", "id", id, "err", err)
		return err
	}
	s.A.Log().Info("access config removed", "id", id, "actor", "ui", "result", "ok")
	s.RegenerateDerived()
	return nil
}

// Apply 把配置合成后投影到 Agent live 文件并置为 current：Get(id) →
// composeFor → backupLive(appType) → 按 appType 写 live（claude→
// writeClaudeLive(doc, env)；codex→writeCodexLive(env, model, doc)）→ SetCurrent
// (appType, id)。任一步失败即停——含备份失败（不写 live、不置 current），
// 失败路径不产生半状态；composeFor 的逻辑模型缺失错误直接透传。
// 审计行绝不记录 env/overrides 值或 token（spec §9 脱敏）。
func (s AccessConfigs) Apply(id string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	if c.AppType == "claude_desktop" {
		// Desktop 无 env 合成层，独立分流（composeFor 不适用）。
		if err := s.backupLive(c.AppType); err != nil {
			return err
		}
		if err := s.applyDesktop(c); err != nil {
			return err
		}
		if err := s.A.Store().AccessConfigs().SetCurrent(c.AppType, id); err != nil {
			return err
		}
		s.A.Log().Info("access config applied", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
		return nil
	}
	env, doc, model, err := s.composeFor(c)
	if err != nil {
		return err
	}
	if err := s.backupLive(c.AppType); err != nil {
		return err
	}
	switch c.AppType {
	case "claude":
		err = s.writeClaudeLive(doc, env)
	case "codex":
		err = s.writeCodexLive(env, model, doc)
	}
	if err != nil {
		return err
	}
	if err := s.A.Store().AccessConfigs().SetCurrent(c.AppType, id); err != nil {
		return err
	}
	s.A.Log().Info("access config applied", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
	return nil
}

// GlobalConfig 读全局 env（settings 键 access.global.<app_type>）；缺失视为
// 空 map。审计/返回不涉及 token。
func (s AccessConfigs) GlobalConfig(appType string) (map[string]string, error) {
	if err := validateAccessAppType(appType); err != nil {
		return nil, err
	}
	raw, err := s.A.Store().Settings().Get("access.global." + appType)
	if err != nil {
		return nil, err
	}
	if raw == "" { // Settings Get 缺失返回 ("", nil) → 无全局配置
		return map[string]string{}, nil
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	return env, nil
}

// SaveGlobalConfig 写全局 env（JSON 编码存 settings 键 access.global.<app_type>）。
// 审计行绝不记录 env 值或 token。
func (s AccessConfigs) SaveGlobalConfig(appType string, env map[string]string) error {
	if err := validateAccessAppType(appType); err != nil {
		return err
	}
	if env == nil {
		env = map[string]string{}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := s.A.Store().Settings().Set("access.global."+appType, string(raw)); err != nil {
		return err
	}
	s.A.Log().Info("access global config saved", "app", appType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 全局配置变了，全部派生文件同步刷新（spec 验收场景④）
	return nil
}

// persistFiles 是 writePersistEnv 的产物：claude/codex 为对应平台持久文件，
// launcher 为 Windows .cmd 启动器（非 Windows 为空）。按 appType 只生成对应
// 一项持久文件，其余为空串。
type persistFiles struct {
	claude   string
	codex    string
	launcher string
}

// writeWindowsLauncher 生成 Windows .cmd 启动器（CRLF）：
//
//	claude → claude --settings "<json>" %*
//	codex  → set "KEY=VALUE"（字典序）+ codex -m "<model>" %*
//
// RegenerateDerived 令牌/端口变更后的联动重写（spec「令牌/端口联动」）：
// 遍历两个 appType 的全部配置重写持久文件；对 Current(appType) 命中的
// 配置额外重写 live 文件。任一配置失败只记 Error 日志并继续其余
// （err 可能含路径，不回显 env/令牌值）；无任何配置时零写入、零日志
// （既有 Gateway 测试不触碰用户目录的前提）。由 Gateway.RegenerateToken
// 与 SaveSettings（端口变更）成功后调用。
func (s AccessConfigs) RegenerateDerived() {
	for _, appType := range []string{"claude", "codex"} {
		cfgs, err := s.A.Store().AccessConfigs().ListByApp(appType)
		if err != nil {
			s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "list", "err", err)
			continue
		}
		if len(cfgs) == 0 {
			continue
		}
		cur, _ := s.A.Store().AccessConfigs().Current(appType)
		for _, c := range cfgs {
			env, doc, model, err := s.composeFor(c)
			if err != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "compose", "err", err)
				continue
			}
			// 持久文件失败不跳过 current 的 live 重写——令牌轮换后 live
			// 仍是 Agent 实际使用的文件，陈旧令牌比持久文件缺失更糟
			// （评审 Minor#8）。
			if _, perr := s.writePersistEnv(c, env, doc, model); perr != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "persist", "err", perr)
			}
			if cur.ID != c.ID {
				continue
			}
			if appType == "claude" {
				err = s.writeClaudeLive(doc, env)
			} else {
				err = s.writeCodexLive(env, model, doc)
			}
			if err != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "live", "err", err)
			}
		}
	}
}

// openTerminalExec 是 OpenTerminal 的终端拉起注入点（测试替换断言参数，
// 不真开终端）；默认执行平台终端命令。
var openTerminalExec = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
