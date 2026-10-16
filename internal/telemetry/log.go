package telemetry

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// NewLogger 返回写入 path 的 JSON slog.Logger 与关闭句柄。path 为空时写 stderr。
func NewLogger(path string) (*slog.Logger, io.Closer, error) {
	if path == "" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil)), io.NopCloser(nil), nil
	}
	// 0700 与 storage/credential 的目录口径一致：日志目录不给 group/other 读。
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return slog.New(slog.NewJSONHandler(f, nil)), f, nil
}
