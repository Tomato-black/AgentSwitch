# 安全说明

AgentSwitch 在本机运行并持有你的供应商 API Key，安全设计围绕这一信任模型展开。

## 威胁模型

- **本机其他进程**：网关 API 仅监听 `127.0.0.1`，需独立访问令牌；密钥不落明文
- **恶意上游/配置注入**：渠道 base_url 做 SSRF / 重定向 / 私网校验，上游 3xx 不跟随
- **日志与诊断外泄**：密钥、Authorization、Cookie、完整 Prompt 禁止出现在日志、错误信息、UI 与诊断输出中（遥测只记脱敏元数据）

## 密钥存储

- 渠道 API Key 以 **AES-256-GCM** 加密存于 SQLite（`channel_secrets` 表）
- 主密钥为库同目录的 `master.key`（32 字节、0600 权限、缺失自动生成）——**删掉它等于废掉全部密文**
- 备份密钥 = 同时备份 `agentswitch.db` 与 `master.key`

## 网关访问

- 模型 API 默认 `127.0.0.1:<port>`，Bearer 令牌鉴权；令牌可在设置页随时重新生成（旧令牌立即作废，所有派生配置文件自动刷新）
- 令牌泄露的处置：设置页「重新生成令牌」→ 重新「应用」各接入配置（或依赖自动刷新）

## 用户预置文件

`~/.agentswitch/presets.json` 由用户本人编写：条目允许 http / 私网 / localhost（自托管中转站场景），权限等同渠道表单手填 + `allowPrivate`，不构成额外攻击面。内置预置则严格要求公网 https。

## 上游行为

- 不跟随上游重定向（3xx 原样交给错误分类）
- 出站透传客户端原始 `User-Agent`（部分订阅供应商条款要求）
- 流式响应截断时向客户端发 error 事件，不以残缺工具参数静默收尾

## 报告漏洞

请勿在公开 issue 中提交安全漏洞细节。通过 [Security Advisories](../../security/advisories/new) 私密报告，或联系维护者。
