# AgentSwitch 官网需求（2026-11-05）

## 目标

为 AgentSwitch 建立面向开发者的官方落地页，部署在 GitHub Pages：
`https://tomato-black.github.io/AgentSwitch/`。

核心转化：访客去 GitHub **Star** 或**阅读文档**；下载安装为次要动作。

## 受众

- 使用 Claude Code / Codex / Claude Desktop 的开发者
- 已经了解 API 网关、对「多供应商切换 / 故障转移」有实际痛点的人
- 能接受命令行与配置文件，不需要图形化教学

## 信息架构（单页）

1. 首屏：定位一句话、Star / 读文档双 CTA、桌面界面示意
2. 为什么：配置切换器 vs 本地网关的差异
3. 功能：8 个能力卡
4. 架构：Agent → 本地网关 → 多供应商
5. 快速开始：下载 → 建渠道/逻辑模型 → 应用接入
6. 平台支持
7. FAQ
8. 页脚：许可证、GitHub / Releases / Issues / Discussions

## 视觉与实现约束

- 沿用桌面端设计系统：深色锌灰 + 品牌橙，橙底深字；状态色不混用
- 无外部字体、无分析/追踪脚本、无第三方运行时依赖
- 代码块可复制；Star 数从 GitHub API 读取，失败时静默隐藏
- 响应式：桌面 / 平板 / 手机；支持键盘导航与 `prefers-reduced-motion`
- 页面静态部署，源码位于 `website/`，由 GitHub Actions 发布 Pages

## 验收标准

- [ ] `website/index.html` 在本地静态服务器直接打开无报错
- [ ] 所有 CTA 指向正确的 GitHub / Releases / README 地址
- [ ] 手机宽度（375px）无横向滚动，导航可展开
- [ ] 代码复制按钮可用，复制内容与展示一致
- [ ] Pages 工作流在 push 后成功部署，线上 URL 可访问
- [ ] 页面无密钥、无本机路径、无第三方请求（GitHub API 除外）

## 非目标

- 多语言切换、独立文档站、博客、登录或后台
- 自动生成截图（首屏为 CSS 界面示意）
