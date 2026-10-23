# 渠道预置配置文件化 + 双协议 base_url 联动 设计

日期：2026-10-23
状态：待评审
参考：cc-switch（/Users/caozhang/work/java_work/cc/cc-switch-main）——其预设同样硬编码于
TS 源码（`src/config/*ProviderPresets.ts`），无用户可扩展机制；本设计是明确的进一步做法。

## 1. 背景与动机

现状：14 家供应商预置硬编码在 `internal/presets/channels.go` 的 Go 静态表
（`presets.All()`），每条只有**单一** protocol + base_url，经 `bridge.Presets` 透传到
桌面预设网格（`applyPreset` 回填表单）。

三个问题：

1. **不可配置**：加/改一家预置要改 Go 源码重新编译；用户的中转站等私有供应商无法做成模板。
2. **单协议假设过时**：2026 年主流供应商普遍同时提供 OpenAI Chat 兼容与 Anthropic
   Messages 兼容两种端点（各自 base_url 不同）。现在用户选了千问预置只能得到 openai
   协议的 base_url，想用 anthropic 协议接入就得自己查文档手填。
3. **数据疑似过时**：部分 base_url 与默认模型名（moonshot-v1 系列、step-2-16k、o3 等）
   是编写时的快照，未经官网核实。

## 2. 目标 / 非目标

**目标**

- 预置数据外置为通用 JSON 配置文件；应用内嵌一份默认（embed，开箱即用），用户可在
  `~/.agentswitch/presets.json` 放自己的文件扩展/覆盖（**用户决策 2026-10-23**）。
- 每家预置按协议组织：`openai` / `anthropic` 各自独立的 base_url 与默认模型建议；
  表单切换协议时自动带出对应值。
- 逐家核实 14 家供应商官网文档，修正 base_url 与模型名。
- **订阅制编程套餐（Coding Plan）端点一并维护（用户决策 2026-10-23）**：供应商的
  包月编程套餐与按量付费是不同计费通道、不同 base_url（智谱 GLM Coding Plan、豆包
  Coding Plan、Kimi For Coding、千问 Coding 套餐、阶跃 Step Plan、小米 Token Plan 等）。
  方案：**作为独立预置条目**（id 后缀 `-coding`，如 `zhipu-coding`；displayName 标注
  套餐名，如「智谱 GLM Coding Plan」），与按量版并列预设网格——预置结构不变、UI 零
  改动，与 cc-switch 套餐站独立卡片的做法一致。无订阅套餐的供应商不强行添加。

**非目标**

- 不扩充/删减供应商名单（维持 14 家；用户想加自己写 `~/.agentswitch/presets.json`）。
- 不做预置编辑器 UI——用户直接用文本编辑器改文件。
- API Key 永不进预置配置文件（预置只是模板，Key 只在 UI 表单录入）。
- 不改渠道运行时行为：路由/故障转移/冷却与预置无关。

**反目标**

- 用户配置文件写坏导致应用无法启动或预设网格空白且无提示——预置是辅助功能，必须降级容错。

## 3. 配置文件格式

`presets.json`（内嵌默认与用户文件同 schema）：

```json
[
  {
    "id": "qwen",
    "displayName": "阿里千问",
    "docUrl": "https://bailian.console.aliyun.com/",
    "protocols": {
      "openai": {
        "baseUrl": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "defaultModels": ["qwen3.8-max", "qwen3.8-flash"]
      },
      "anthropic": {
        "baseUrl": "https://dashscope.aliyuncs.com/apps/anthropic",
        "defaultModels": ["qwen3.8-max", "qwen3.8-flash"]
      }
    }
  }
]
```

- 顶层是数组，顺序即 UI 网格展示序（内嵌默认延续现状：国内优先，再海外）。
- `protocols` 至少含一个键；某家无某协议端点则省略该键（如 OpenAI 官方无 anthropic）。
- `defaultModels` 允许为空数组（该协议端点模型需用户自填时）。
- 协议键只认 `openai` / `anthropic`，未知键跳过该条并记日志（与渠道协议枚举一致）。
- 旧单协议字段（顶层 `protocol`/`baseUrl`/`defaultModels`）不兼容、不迁移——预置是
  无状态模板，无存量数据要保。

## 4. 加载与合并语义

- **内嵌默认**：`internal/presets/default.json` 经 `//go:embed` 打进二进制，解析失败
  属构建期缺陷（测试断言），运行时不再容错处理内嵌文件。
- **用户文件**：`~/.agentswitch/presets.json`（无 home 则跳过用户合并）。
  - 合并按 `id`：同 id **整体覆盖**（含 displayName/docUrl/protocols），覆盖后保持在
    内嵌默认中的原位置；新 id 追加到末尾。**不支持删除内嵌预置**（无墓碑语法；
    不想要的预置覆盖之即可）。
  - 解析失败（语法错误/类型错误）：**降级用内嵌默认**，记日志；bridge 提供最近一次
    加载警告文本，UI 预设网格顶部展示一行错误提示（不弹窗、不阻塞）。
  - 单条校验失败（id 空、protocols 为空、协议键未知）：**跳过该条**，其余照常；
    同样计入加载警告。
  - **baseUrl 校验分档（用户决策 2026-10-23）**：内嵌默认必须过
    `provider.ValidateBaseURL(_, false)`（公网 https，测试断言）；**用户文件放宽**——
    允许 http / 私网 / localhost（自托管中转站模板是本设计的动机之一；文件由用户
    本人编写、非远程下发，权限等同表单手填 + allowPrivate 开关，不提升攻击面），
    仅要求 URL 可解析（url.Parse 成功且带 scheme/host）。
- **加载时机**：应用启动时加载一次进内存（`presets` 包持有合并结果，只读）；不监听
  文件变更，用户改文件后重启生效（预置低频变更，不做热重载——避免引文件 watcher）。
- 测试通过注入内嵌字节与用户文件路径覆盖，不依赖真实 `~`。

## 5. 桌面 UI 联动（`internal/desktop/channels.go`）

- **预设网格点选**（`applyPreset`）：回填 `presetID`、`displayName→name`（仅当 name 为空）、
  以及该预置**第一个可用协议**（`openai` 优先于 `anthropic`，按固定序）的 base_url 与
  建议模型。
- **切换协议下拉**（`ui.Select`，现 `channels.go:310`）：当 `presetID` 非空且该预置含
  目标协议时，自动替换 base_url 与建议模型；**手改保护**——若当前 base_url 与**任一**
  协议的预设值都不一致且非空（用户已手改），切换时不覆盖，保留用户输入（含任一协议
  预设值的判定使往返切换对称，且手敲恰好等于另一协议预设值时替换结果与所敲字符串相
  同、无可观察差异）；**空值视为未修改**（从未填过 = 切回时恢复预设值）。建议模型与
  base_url 各自独立判定。
- **切到预置未提供的协议**：base_url 与建议模型在**未手改**（空或等于任一协议预设值）
  时清空待手填；已手改的值保留不清（清掉手填值是破坏性的）。预设网格选中的高亮保持。
- **「自定义」格**：行为不变（清预设字段，protocol 落默认 openai）。
- **编辑已有渠道**：详情页协议保持静态文本、**不加协议控件**（协议创建后不可变是现有
  语义，`bridge.Channels.Update` 不接受协议参数——本设计不改这一点）；presetID 从渠道
  记录读回仅作展示标识，`ById` 查不到（如用户文件覆盖后 id 变更）时视同自定义，不影响
  渠道本身。渠道创建后 protocol/base_url 是快照。
- **建议模型用原生名**：anthropic 协议的建议模型与 openai 协议一致，用供应商原生模型名
  （如 DeepSeek 给 `deepseek-flash`）；端点自带的 claude-\* 别名映射（DeepSeek/千问等）
  只是上游便利特性，不入建议列表。

## 6. base_url 查证结果（2026-10-23，官方文档）

<!-- 查证 agent 结果待填；每行附来源 URL；「无」= 官方无该协议端点 -->

| id | 供应商 | openai baseUrl | anthropic baseUrl | 默认模型核实 |
|----|--------|----------------|-------------------|--------------|
| xiaomi | 小米 MiMo | `https://api.xiaomimimo.com/v1` | `https://api.xiaomimimo.com/anthropic` | mimo-v2.6-pro / mimo-v2.6-flash / mimo-v2.6-pro-ultraspeed |
| qwen | 阿里千问 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `https://dashscope.aliyuncs.com/apps/anthropic` | qwen3.8-max / qwen3.8-flash / qwen3.7-plus |
| deepseek | DeepSeek | `https://api.deepseek.com` | `https://api.deepseek.com/anthropic` | deepseek-flash / deepseek-v4-pro |
| kimi | Kimi 月之暗面 | `https://api.moonshot.cn/v1` | `https://api.moonshot.cn/anthropic` | kimi-k3 / kimi-k2.7-code / kimi-k2.6 |
| zhipu | 智谱 GLM | `https://open.bigmodel.cn/api/paas/v4` | `https://open.bigmodel.cn/api/anthropic` | glm-5.3 / glm-5.3-flash / glm-5.2 |
| siliconflow | 硅基流动 | `https://api.siliconflow.cn/v1` | `https://api.siliconflow.cn` | deepseek-ai/DeepSeek-V4.1-Flash / zai-org/GLM-5.3 / moonshotai/Kimi-K2.7-Code |
| stepfun | 阶跃星辰 | `https://api.stepfun.com/v1` | `https://api.stepfun.com` | step-5-preview / step-3.7-flash / step-3.5-flash |
| doubao | 豆包·火山方舟 | `https://ark.cn-beijing.volces.com/api/v3` | `https://ark.cn-beijing.volces.com/api/compatible` | doubao-seed-evolving / doubao-seed-2-1-pro-260915 / doubao-seed-2-1-lite-260915 |
| anthropic | Anthropic | `https://api.anthropic.com/v1` | `https://api.anthropic.com` | claude-fable-5-1 / claude-opus-5-5 / claude-sonnet-5-5 / claude-haiku-5-5 |
| openai | OpenAI | `https://api.openai.com` | 无 | gpt-5.6-terra / gpt-5.6-luna |
| google | Google AI Studio | `https://generativelanguage.googleapis.com/v1beta/openai` | 无 | gemini-4-argon / gemini-3.8-flash |
| groq | Groq | `https://api.groq.com/openai/v1` | 无 | openai/gpt-oss-120b / qwen/qwen3.8-27b / openai/gpt-oss-20b |
| together | Together AI | `https://api.together.ai/v1` | 无 | moonshotai/Kimi-K3 / zai-org/GLM-5.3 / Qwen/Qwen3.8-27B |
| openrouter | OpenRouter | `https://openrouter.ai/api/v1` | `https://openrouter.ai/api/v1` | anthropic/claude-sonnet-5.5 / anthropic/claude-opus-5.5 / openai/gpt-6.1-sol |

<!-- 查证来源（2026-10-23，全部官方一手文档）：
- 小米：现有配置端点全错（api.xiaomi.com 非 MiMo 平台）；文档站 mimo.mi.com
  （first-api-call / model / anthropic-api）；docUrl 应改 https://mimo.mi.com/ ；
  mimo-7b 系列与 v2.5 系列均停用，现役 v2.6。
- 千问：compatible-mode/v1 有效；anthropic 端点只提供 /v1/messages 不提供 /v1/models；
  qwen-max/turbo 旧名不在当前目录（qwen-plus/flash 别名仍可调用）。
- DeepSeek：deepseek-chat / deepseek-reasoner 已于 2026-07-24 停用；anthropic 端点内置
  claude-* → deepseek 模型名映射；openai 协议无需 /v1 后缀。
- Kimi：openai base_url 必须带 /v1；moonshot-v1 / kimi-k2 / k2.5 均已停用；另支持
  OpenAI Responses 协议（与本项目 Responses 入站相关，仅备注不入预置）。
- Anthropic：官方已提供 OpenAI SDK 兼容层（platform.claude.com 文档
  openai-sdk 页），定位「评测/试验」，不支持 prompt caching / web search / 部分
  参数；anthropic 原生端点不变。模型补旗舰档 claude-fable-5-1。
- OpenAI：**gpt-4o / gpt-4o-mini 已标 Deprecated**。GPT-6 系列（astra/6.1-sol）虽为
  当前旗舰，但官方文档明确 Chat Completions 下工具调用要求走 Responses API——本网关
  openai 渠道协议为 chat completions、Claude Code 场景几乎全是工具调用，故默认模型
  **稳妥选 gpt-5.6 系**（chat 协议下完整工具调用），gpt-6 系待本网关未来增加
  openai-responses 出站协议后再上。这是本设计的判断，非官网原文。
- Google：现有 base_url 等价有效；gemini-2.5 系未关停但已非主推，升级为
  gemini-4-argon / gemini-3.8-flash（注意 ID 是点号 3.8）。
- 智谱：传闻的 /api/paas/v4/anthropic 不正确，官方是独立路径 /api/anthropic；
  glm-4 系只剩迁移条目，现役 glm-5.x。
- 硅基流动：两协议后缀规则不同——openai 必须带 /v1，anthropic 不带（SDK 追加
  /v1/messages）；官方 Claude Code 指南 ANTHROPIC_BASE_URL 即裸域名。
- 阶跃：step-2-16k / step-1-8k 已下线；step-5-preview 旗舰 1M 上下文；另有官方
  Responses API 与订阅制 /step_plan 前缀（后者不入预置）。
- 豆包方舟：/api/compatible 为按量付费 Anthropic 兼容端点；Coding Plan 订阅另有
  /api/coding 专用端点（不入预置）；doubao-seed-1-6 / doubao-1-5-pro-32k 已过时。
- Groq：现有两个模型**均已停服**（llama-3.3-70b-versatile 2026-08-16 关停），必须
  整体更换为 gpt-oss / qwen3.8 系。
- Together：现有 api.together.xyz 是**旧域名**，官方现用 api.together.ai 且必须带
  /v1；无公开的固定 Anthropic 兼容 base_url（Together Link 是托管网关注入地址，
  不适合预置）——官方 docs 存在一页 anthropic-messages-api 参考但被 Cloudflare
  拦截无法核实，按「无」处理。
- OpenRouter：官方 Anthropic Messages 端点存在（POST /api/v1/messages），与 openai
  协议共用同一 base_url；默认模型升级到 claude-5.5 系 / gpt-6.1-sol。
-->

### 订阅制 Coding Plan 预置（独立条目，id 后缀 -coding）

| id | 供应商 | openai baseUrl | anthropic baseUrl | 默认模型 |
|----|--------|----------------|-------------------|----------|
| zhipu-coding | 智谱 GLM Coding Plan | `https://open.bigmodel.cn/api/coding/paas/v4` | `https://open.bigmodel.cn/api/anthropic`（与按量同址） | glm-5.3 / glm-5.3-flash |
| doubao-coding | 豆包 Coding Plan | `https://ark.cn-beijing.volces.com/api/coding/v3` | `https://ark.cn-beijing.volces.com/api/coding` | doubao-seed-evolving / doubao-seed-2-1-pro-260915 / doubao-seed-2-1-lite-260915 |
| kimi-coding | Kimi Code | `https://api.kimi.com/coding/v1` | `https://api.kimi.com/coding/` | k3 / kimi-for-coding / kimi-for-coding-highspeed |
| qwen-coding | 百炼 Coding Plan | `https://coding.dashscope.aliyuncs.com/v1` | `https://coding.dashscope.aliyuncs.com/apps/anthropic` | qwen3.7-plus / kimi-k2.5 / glm-5 |
| stepfun-coding | 阶跃 Step Plan | `https://api.stepfun.com/step_plan/v1` | `https://api.stepfun.com/step_plan` | step-router-v1 / step-5-preview / step-3.7-flash |
| xiaomi-coding | 小米 MiMo Token Plan | `https://token-plan-cn.xiaomimimo.com/v1` | `https://token-plan-cn.xiaomimimo.com/anthropic` | mimo-v2.6-pro / mimo-v2.6-flash |

<!-- 订阅端点查证来源（2026-10-23，官方文档原文抓取核实）：
- 智谱：docs.bigmodel.cn/cn/coding-plan/latest-model 等。anthropic 端点与按量付费
  同址（/api/anthropic），计费靠 Key 类型区分（个人/团队套餐 Key 与标准 API Key
  三种互不通用）。模型名支持小写 [1m] 后缀开 1M 上下文。另有 Codex 专用
  Responses 端点 https://open.bigmodel.cn/api/v1（不入预置——本网关无
  openai-responses 出站协议）。
- 豆包：volcengine.com/docs/ark/coding-plan-personal-get-started。URL 路径决定
  计费通道——误用 /api/v3 不消耗套餐额度而按量扣费。特殊模型 ark-code-latest
  （控制台切换模型，不支持写 Auto）。
- Kimi：kimi.com/code/docs/benefits.html。与开放平台（api.moonshot.cn）完全不同
  体系：域名 api.kimi.com、Key 前缀 sk-kimi-（控制台创建，最多 5 个）、URL 与 Key
  必须配对。模型 ID 与开放平台不同（k3 / kimi-for-coding，非 kimi-k2.7-code）。
  anthropic base 官方原文带结尾斜杠 /coding/。频控按档位（5 小时 300–1200 次）。
- 千问：help.aliyun.com/zh/model-studio/coding-plan 与 claude-code 页。openai 端
  点是 coding 域名下直接 /v1（不是 compatible-mode/v1）。套餐 Key 前缀 sk-sp-，
  与按量 Key 严格隔离互不通用。每月 9 万次上限；官方条款严禁非交互式批量调用
  （编程工具前置网关属允许场景，渠道备注提醒用户勿用于脚本化批量调用）。
- 阶跃：platform.stepfun.com/docs/zh/step-plan/quick-start。同一 Step API Key 通用，
  路径决定计费（/step_plan 扣套餐 Credit，按量地址扣余额）；step-router-v1 仅
  Step Plan 通道可用（deepseek-v4-pro 与 step-3.7-flash 间自动分流）。
- 小米：mimo.mi.com/docs/tokenplan/integration/claudecode。Token Plan 专属域名
  token-plan-cn.xiaomimimo.com，Key 前缀 tp-（个人）/ttp-（团队）与按量不通用；
  分层限额（Lite 30 请求/5 分钟 … Max 200）。
- DeepSeek / 硅基流动：**无订阅制套餐**（官方仅按量计费，已核实定价页与全站文档），
  不加 -coding 条目；二者的 anthropic 端点与按量同域，已在主表覆盖。
-->

### ⚠️ 订阅端点引出的两个网关检查项（已核实）

1. **`[1m]` 小写后缀——已兼容，无需改动**：智谱 Coding Plan 模型名用小写 `[1m]`
   （如 `glm-5.3-flash[1m]`）开 1M 上下文。经查，网关 `gateway.stripOneM`
   （`internal/gateway/handlers.go:32`）与 bridge `accessSplitOneM`
   （`internal/bridge/accessconfigs.go:80`）均用 `strings.EqualFold`，大小写不敏感，
   `my-code[1m]` 可正常命中逻辑模型。实施时补一条小写断言测试即可。
2. **User-Agent 透传——需实施改动**：Kimi Code 条款明确「篡改客户端 UA 视为违规可
   暂停权益」。经查，`internal/provider`（openai.go / anthropic.go）出站请求只设
   Content-Type/Authorization/X-Api-Key/Anthropic-Version/Accept，**未透传客户端
   原始 UA**（Go 默认发 `Go-http-client/1.1`）。实施任务：gateway 收取入站
   `User-Agent` 传入 provider 请求上下文，出站请求透传该值（入站缺 UA 时不设置，
   保持 Go 默认）；Fake Provider 测试断言透传。

### 查证汇总

- 预置总量：14 条按量 + 6 条订阅（Coding Plan）= **20 条**（原 spec §7「15～25 家」范围内）。
- 按量双协议：10 家（小米/千问/DeepSeek/Kimi/智谱/硅基流动/阶跃/豆包/Anthropic/OpenRouter）。
- 按量仅 openai：4 家（OpenAI/Google/Groq/Together）。
- 订阅套餐：6 家（智谱/豆包/Kimi/千问/阶跃/小米）；DeepSeek、硅基流动核实无套餐。
- 现有配置错误/过时清单：小米端点全错、Together 域名废弃、Groq 模型全部停服、
  DeepSeek/Kimi/智谱/阶跃/豆包/Groq/OpenAI/Google/OpenRouter 模型名过时或换代、
  Kimi/硅基流动/阶跃 openai base 缺 /v1 后缀。

### ⚠️ JoinAPIPath 拼接验证（实施必查项）

查证核实后的 anthropic 端点 base 形态（依据各家官方文档的完整请求地址）：

- **base 无 `/v1`、请求路径 = `{base}/v1/messages`**（Anthropic SDK 约定，绝大多数）：
  小米 `…/anthropic`、千问 `…/apps/anthropic`、DeepSeek `…/anthropic`、Kimi `…/anthropic`、
  智谱 `…/api/anthropic`、硅基流动裸域名、阶跃裸域名、豆包 `…/api/compatible`、
  Anthropic 官方裸域名。
- **base 已含 `/v1`、请求路径 = `{base}/messages`**：仅 OpenRouter
  （`https://openrouter.ai/api/v1` + `POST /messages`）。

现有 `provider.JoinAPIPath`/`anthropicEndpoint` 按路径格式区分这两种形态，与上表一致
（`internal/provider/anthropic.go:21` 记录了 2026-10-22 小米 `/anthropic` 形态的实测修复）。
实施第一个任务仍须用表内**全部** anthropic base_url 逐条断言拼接结果，作为防回归护栏
——数据错了这是最先暴露的地方。

## 7. 影响面与代码结构

| 位置 | 变更 |
|------|------|
| `internal/presets/channels.go` | 静态表 → `ChannelPreset` 双协议结构体 + embed 加载 + 用户文件合并 + 校验；`All()/ById()` 签名不变（返回值结构变） |
| `internal/presets/default.json`（新增） | 14 家默认预置（查证结果） |
| ~~`internal/bridge/presets.go`~~ | 实施删除：desktop 直读 `internal/presets`（2026-10-17 UI 决策后的直连模式），bridge.PresetView 为 frontend 时代遗留死代码、从未 Bind |
| `internal/desktop/channels.go` | `applyPreset` 双协议回填；协议下拉联动 + 手改保护 |
| `internal/gateway` + `internal/provider` | UA 透传：入站 User-Agent 传至出站请求（Kimi Code 合规要求，见 §6 检查项 2） |
| 测试 | presets 加载/合并/校验/降级；applyPreset 与协议联动（纯函数级）；bridge 透传；JoinAPIPath 全表断言；UA 透传断言；`[1m]` 小写剥离断言 |

不涉及：storage schema（无迁移）、gateway/router、access、credential。

## 8. 已确认的实现机制

- **协议切换检测**：MyGo v0.6.2 `ui.Select(...)` 返回 Element，`Element.Changed() bool`
  在值变化的当帧返回 true（`ui/api_gen.go:3019`）——联动逻辑写在 `Changed()` 为真的
  分支，无需自记上一帧值。
- 加载警告从 presets 包到 UI 的传递路径（presets 持最近加载警告字符串，UI 直读
  `presets.Warning()` 在网格顶部一行展示；原拟经 bridge 透传，实施时因 desktop
  直连 internal 包的既有模式而直读，bridge 遗留死代码已删）。
