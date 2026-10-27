package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Codex 桌面版的模型选择器只读 config.toml 的 model_catalog_json 指向的
// 文件，不读网关的 /v1/models（2026-10-27 实测：没有目录时只显示 Codex
// 内置 GPT 模型）。这里按 cc-switch 的做法，用 Codex 自带模型条目做模板，
// 克隆出 AgentSwitch 逻辑模型条目，否则桌面版会把 model 回写成旧目录里的
// 模型名，网关收到未登记模型后直接 404。
const codexModelCatalogFileName = "agentswitch-model-catalog.json"

// codexModelCatalogTemplateLoader 是模板来源的注入点：生产实现会找 Codex
// 可执行文件跑 `debug models --bundled`；测试替换为固定模板，保持离线。
var codexModelCatalogTemplateLoader = loadCodexModelCatalogTemplate

// codexLogicalModelsForCatalog 返回目录应包含的逻辑模型名：绑定模型恒在
// 第一位（桌面版默认选它），其余启用的按名排序；禁用的不收录。绑定模型即使
// 被禁用也保留——接入配置仍引用它，目录里缺了它桌面版无法选中。
func (s AccessConfigs) codexLogicalModelsForCatalog(bound string) ([]string, error) {
	bound = strings.TrimSpace(bound)
	if bound == "" {
		return nil, fmt.Errorf("逻辑模型名为空")
	}
	lms, err := s.A.Store().LogicalModels().List()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{bound: true}
	var others []string
	for _, lm := range lms {
		name := strings.TrimSpace(lm.Name)
		if !lm.Enabled || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		others = append(others, name)
	}
	sort.Strings(others)
	return append([]string{bound}, others...), nil
}

// buildCodexModelCatalog 为每个逻辑模型克隆一条模板，替换可变的展示字段。
// 模板里的 upgrade/upgrade_info 指向官方 GPT 模型，必须去掉，否则桌面版会
// 提示升级到网关不认识的模型。
func buildCodexModelCatalog(template map[string]any, models []string) ([]byte, error) {
	entries := make([]any, 0, len(models))
	for i, name := range models {
		entry, err := cloneCodexCatalogEntry(template)
		if err != nil {
			return nil, err
		}
		entry["slug"] = name
		entry["display_name"] = name
		entry["description"] = "AgentSwitch 逻辑模型：" + name
		entry["priority"] = 1000 + i
		entry["visibility"] = "list"
		for _, k := range []string{"upgrade", "upgrade_info", "availability_nux"} {
			delete(entry, k)
		}
		entries = append(entries, entry)
	}
	return json.MarshalIndent(map[string]any{"models": entries}, "", "  ")
}

// cloneCodexCatalogEntry 深拷贝模板条目；UseNumber 保留模板里的数字原文。
func cloneCodexCatalogEntry(entry map[string]any) (map[string]any, error) {
	b, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// parseCodexCatalogTemplate 解析 `codex debug models --bundled` / 本地目录
// 文件，取第一条带提示词的模型（codex-auto-review 这类条目没有提示词）。
func parseCodexCatalogTemplate(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	for _, m := range doc.Models {
		if codexCatalogEntryHasInstructions(m) {
			return m, nil
		}
	}
	return nil, errors.New("模型目录中没有带提示词的条目")
}

func codexCatalogEntryHasInstructions(entry map[string]any) bool {
	if s, _ := entry["base_instructions"].(string); strings.TrimSpace(s) != "" {
		return true
	}
	if mm, ok := entry["model_messages"].(map[string]any); ok {
		if s, _ := mm["instructions_template"].(string); strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// loadCodexModelCatalogTemplate 先找 Codex 可执行文件取内置模型，再退回
// 本地已有目录（cc-switch 或上一次 AgentSwitch 生成的）——迁移用户即使
// 暂时找不到 Codex 二进制也能生成目录。
func loadCodexModelCatalogTemplate() (map[string]any, error) {
	var lastErr error
	for _, bin := range codexBinaryCandidates() {
		entry, err := readBundledCodexModelTemplate(bin)
		if err == nil {
			return entry, nil
		}
		lastErr = err
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if entry, err := loadLocalCodexCatalogTemplate(home); err == nil {
			return entry, nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("无法生成 Codex 桌面版模型目录：%v", lastErr)
	}
	return nil, errors.New("无法生成 Codex 桌面版模型目录：未找到 Codex 内置模型模板（请确认 Codex CLI 或桌面版已安装）")
}

// readBundledCodexModelTemplate 跑 `codex debug models --bundled` 取模板。
func readBundledCodexModelTemplate(bin string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if accessPlatform == "windows" && isBatchFile(bin) {
		// CreateProcess 不能直接执行 .cmd/.bat，经 cmd /c 包装。
		cmd = exec.CommandContext(ctx, "cmd", "/c", bin, "debug", "models", "--bundled")
	} else {
		cmd = exec.CommandContext(ctx, bin, "debug", "models", "--bundled")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseCodexCatalogTemplate(out)
}

func isBatchFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")
}

// codexBinaryCandidates 返回可能带内置模型表的 Codex 可执行文件：桌面版
// bundle 优先（版本与正在运行的桌面版一致），其次 PATH 上的独立 CLI。
func codexBinaryCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	home, _ := os.UserHomeDir()
	switch accessPlatform {
	case "darwin":
		for _, app := range []string{"ChatGPT.app", "Codex.app"} {
			for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
				for _, rel := range []string{"Contents/Resources/codex-cli/bin/codex", "Contents/Resources/codex"} {
					add(filepath.Join(base, app, rel))
				}
			}
		}
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			for _, p := range newestCodexExeCandidates(filepath.Join(local, "OpenAI", "Codex", "bin")) {
				add(p)
			}
		}
	default:
		for _, p := range []string{
			"/usr/lib/chatgpt/resources/codex",
			"/opt/chatgpt/resources/codex",
			"/usr/lib/codex/resources/codex",
			"/usr/share/chatgpt/resources/codex",
			"/usr/local/lib/chatgpt/resources/codex",
		} {
			add(p)
		}
	}
	if p, err := exec.LookPath("codex"); err == nil {
		add(p)
	}
	return out
}

// newestCodexExeCandidates 返回 %LOCALAPPDATA%\OpenAI\Codex\bin 下各版本
// 目录里的 codex.exe，按修改时间从新到旧（版本号字符串排序不可靠）。
func newestCodexExeCandidates(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var cs []candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "codex.exe")
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		cs = append(cs, candidate{p, fi.ModTime()})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].mod.Equal(cs[j].mod) {
			return cs[i].path > cs[j].path
		}
		return cs[i].mod.After(cs[j].mod)
	})
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.path
	}
	return out
}

// loadLocalCodexCatalogTemplate 从 ~/.codex 下已有目录文件取模板兜底。
func loadLocalCodexCatalogTemplate(home string) (map[string]any, error) {
	if home == "" {
		return nil, errors.New("用户目录为空")
	}
	dir := filepath.Join(home, ".codex")
	var lastErr error
	for _, name := range []string{codexModelCatalogFileName, "cc-switch-model-catalog.json", "models_cache.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			lastErr = err
			continue
		}
		entry, err := parseCodexCatalogTemplate(b)
		if err == nil {
			return entry, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("没有本地 Codex 模型目录")
}
