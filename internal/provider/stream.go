package provider

import (
	"context"
	"io"
	"strings"
)

// WrapPassthroughStream 包装直通 SSE 流：字节原样转发；跟踪终止标记
// （terminals 任一出现即视为正常结束）。EOF/读错误且未见终止标记时追加
// onTruncate；ctx 取消时静默关闭。src 由泵关闭。
func WrapPassthroughStream(ctx context.Context, src io.ReadCloser, onTruncate []byte, terminals ...string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer src.Close()
		seen := false
		maxTail := 0
		for _, m := range terminals {
			if len(m)-1 > maxTail {
				maxTail = len(m) - 1
			}
		}
		tail := "" // 保留 marker 长度的尾部，防止终止标记被 chunk 边界切开
		scan := func(p []byte) {
			window := tail + string(p)
			for _, m := range terminals {
				if strings.Contains(window, m) {
					seen = true
				}
			}
			if len(window) > maxTail {
				window = window[len(window)-maxTail:]
			}
			tail = window
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
			if ctx.Err() == nil && (!seen || rerr != io.EOF) {
				_, _ = pw.Write(onTruncate)
			}
			pw.Close()
			return
		}
	}()
	return pr
}
