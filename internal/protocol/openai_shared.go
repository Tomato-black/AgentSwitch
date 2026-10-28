// 本文件是 OpenAI 上游方向的公共类型形状：两侧（请求转换与响应/流式
// 转换）共用的块与工具调用结构。
package protocol

import (
	"encoding/json"
)

// ---------- 公共形状 ----------

type anthxBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type oaixToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
