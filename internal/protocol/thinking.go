package protocol

import "encoding/json"

// StripThinking 去掉请求顶层的 thinking 配置，以及 messages[].content 中的
// thinking / redacted_thinking 块。用于兼容部分 Anthropic 兼容上游在
// 「thinking 模式历史不完整」时返回的 400：
//
//	The `content[].thinking` in the thinking mode must be passed back to the API.
//
// 返回 changed=false 表示请求里没有 thinking，无需重试。只改结构，不改其他字段。
func StripThinking(body []byte) ([]byte, bool, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, false, err
	}
	changed := false
	if _, ok := top["thinking"]; ok {
		delete(top, "thinking")
		changed = true
	}
	if raw, ok := top["messages"]; ok {
		var msgs []map[string]json.RawMessage
		if json.Unmarshal(raw, &msgs) == nil {
			out := make([]map[string]json.RawMessage, 0, len(msgs))
			for _, m := range msgs {
				craw, ok := m["content"]
				if !ok {
					out = append(out, m)
					continue
				}
				var blocks []map[string]json.RawMessage
				if json.Unmarshal(craw, &blocks) != nil {
					out = append(out, m) // string content：原样保留
					continue
				}
				filtered := blocks[:0]
				removed := false
				for _, b := range blocks {
					var typ string
					_ = json.Unmarshal(b["type"], &typ)
					if typ == "thinking" || typ == "redacted_thinking" {
						removed = true
						changed = true
						continue
					}
					filtered = append(filtered, b)
				}
				if removed {
					var role string
					_ = json.Unmarshal(m["role"], &role)
					if len(filtered) == 0 && role == "assistant" {
						continue // 空 assistant 消息会同样被上游拒绝
					}
					cb, err := json.Marshal(filtered)
					if err != nil {
						return nil, false, err
					}
					m["content"] = cb
				}
				out = append(out, m)
			}
			if changed {
				mb, err := json.Marshal(out)
				if err != nil {
					return nil, false, err
				}
				top["messages"] = mb
			}
		}
	}
	if !changed {
		return body, false, nil
	}
	b, err := json.Marshal(top)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
