package provider

import (
	"bytes"
	"context"
	"io"
	"strings"
)

// WrapPassthroughStream 包装直通 SSE 流：字节原样转发。终止标记（terminals
// 任一出现即视为正常结束）只在**完整 SSE 事件**内匹配（事件以 \n\n 或
// \r\n\r\n 分隔），避免把事件中途的残缺字节或正文里的同名字面量误判为正常
// 结束。EOF/读错误且未见终止标记时追加 onTruncate；若最后转发的字节停在
// 事件中途，先补 \n\n 使追加的错误事件自成一条 SSE 事件。ctx 取消或已见
// 终止标记时不再追加；ctx 取消不会主动解除阻塞中的 src.Read——解除依赖
// 调用方关闭返回的 reader 或取消上游请求。src 由泵关闭。
func WrapPassthroughStream(ctx context.Context, src io.ReadCloser, onTruncate []byte, terminals ...string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer src.Close()
		defer pw.Close()
		seen := false
		var pending []byte // 最后一个完整事件之后的字节（可能为空 = 停在事件边界）
		scan := func(p []byte) {
			pending = append(pending, p...)
			for {
				event, consumed, ok := splitSSEEvent(pending)
				if !ok {
					return
				}
				if eventHasTerminal(event, terminals) {
					seen = true
				}
				pending = pending[consumed:]
			}
		}
		buf := make([]byte, 32<<10)
		for {
			n, rerr := src.Read(buf)
			if n > 0 {
				scan(buf[:n])
				if _, werr := pw.Write(buf[:n]); werr != nil {
					return
				}
			}
			if rerr == nil {
				continue
			}
			// 已见终止标记后的读错误不再补第二条 error 事件（正常收尾）；
			// 客户端取消保持静默。
			if ctx.Err() == nil && !seen && len(onTruncate) > 0 {
				if len(pending) > 0 {
					if _, werr := pw.Write([]byte("\n\n")); werr != nil {
						return
					}
					pending = nil
				}
				_, _ = pw.Write(onTruncate)
			}
			return
		}
	}()
	return pr
}

// splitSSEEvent 取出第一个完整 SSE 事件（不含分隔符）及其消费的字节数。
// 与 protocol.findEvent 同语义（provider 不得依赖未导出 helper）。
func splitSSEEvent(raw []byte) (event []byte, consumed int, ok bool) {
	i1 := bytes.Index(raw, []byte("\n\n"))
	i2 := bytes.Index(raw, []byte("\r\n\r\n"))
	switch {
	case i1 >= 0 && (i2 < 0 || i1 < i2):
		return raw[:i1], i1 + 2, true
	case i2 >= 0:
		return raw[:i2], i2 + 4, true
	default:
		return nil, 0, false
	}
}

// eventHasTerminal 在单个完整 SSE 事件内匹配终止标记：
//   - "event: xxx" 只与 event 行全等匹配，避免正文里出现该文本时误判；
//   - "data: xxx" 只与 data 行 payload 全等匹配；"[DONE]" 是前者的简写；
//   - 其他标记（如 `"finish_reason":"`）按原始子串匹配——JSON 字符串内的
//     引号会被转义，故该形态只可能出现在结构键位置。
func eventHasTerminal(event []byte, terminals []string) bool {
	lines := strings.Split(string(event), "\n")
	for _, m := range terminals {
		if m == "" {
			continue
		}
		switch {
		case strings.HasPrefix(m, "data:") || m == "[DONE]":
			want := strings.TrimSpace(strings.TrimPrefix(m, "data:"))
			for _, line := range lines {
				line = strings.TrimSuffix(line, "\r")
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				if strings.TrimSpace(strings.TrimPrefix(line, "data:")) == want {
					return true
				}
			}
		case strings.HasPrefix(m, "event:"):
			want := strings.TrimSpace(strings.TrimPrefix(m, "event:"))
			for _, line := range lines {
				line = strings.TrimSuffix(line, "\r")
				if !strings.HasPrefix(line, "event:") {
					continue
				}
				if strings.TrimSpace(strings.TrimPrefix(line, "event:")) == want {
					return true
				}
			}
		default:
			if bytes.Contains(event, []byte(m)) {
				return true
			}
		}
	}
	return false
}
