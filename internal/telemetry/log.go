// Package telemetry 是事件遥测与日志：内存环记录最近请求、按大小轮转的
// 文件日志（gzip 归档）、敏感值打码（redact）。
package telemetry

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// 日志轮转默认值（2026-10-21 用户决策）：单文件 5 MiB 轮转一次，旧文件
// gzip 归档、最多保留 2 份。磁盘上限 ≈ 5 MB + 少量压缩归档，长期运行不膨胀。
const (
	defaultMaxBytes = 5 << 20
	defaultKeep     = 2
)

// NewLogger 返回写入 path 的 JSON slog.Logger 与关闭句柄。path 为空时写 stderr。
// 非空路径走 rotatingWriter：超过 defaultMaxBytes 轮转，归档保留 defaultKeep 份。
func NewLogger(path string) (*slog.Logger, io.Closer, error) {
	if path == "" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil)), io.NopCloser(nil), nil
	}
	// 0700 与 storage/credential 的目录口径一致：日志目录不给 group/other 读。
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	w, err := newRotatingWriter(path, defaultMaxBytes, defaultKeep)
	if err != nil {
		return nil, nil, err
	}
	return slog.New(slog.NewJSONHandler(w, nil)), w, nil
}

// rotatingWriter 是带大小轮转的日志文件写入器（并发安全）：写满 maxBytes
// 前把当前文件 gzip 到 <path>.1.gz，旧归档依次后移，只保留 keep 份。
// 轮转失败不中断日志——错误写 stderr，继续追加当前文件。
type rotatingWriter struct {
	path     string
	maxBytes int64
	keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

func newRotatingWriter(path string, maxBytes int64, keep int) (*rotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if keep < 0 {
		keep = 0
	}
	w := &rotatingWriter{path: path, maxBytes: maxBytes, keep: keep}
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	// 上次运行遗留的超限文件：启动即归档，避免旧大文件一直滚不出去。
	if w.size >= w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "telemetry: rotate existing log failed: %v\n", err)
		}
	}
	return w, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "telemetry: log rotate failed: %v\n", err)
		}
		if w.f == nil { // 轮转与恢复都失败，不能静默丢日志
			return 0, os.ErrClosed
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// openLocked 以追加模式打开当前日志并同步真实大小；调用方持锁
// （构造期尚未发布，无需持锁）。
func (w *rotatingWriter) openLocked() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f = f
	w.size = fi.Size()
	return nil
}

// rotateLocked 关闭当前文件 → 归档为 .1.gz → 重开空文件；调用方持锁。
func (w *rotatingWriter) rotateLocked() error {
	var closeErr error
	if w.f != nil {
		closeErr = w.f.Close()
		w.f = nil
	}
	if err := w.archiveLocked(); err != nil {
		// 归档失败：恢复写句柄继续追加（宁可单文件超限，也不丢日志）。
		if oerr := w.openLocked(); oerr != nil {
			return errors.Join(err, oerr)
		}
		return err
	}
	return errors.Join(closeErr, w.openLocked())
}

// archiveLocked 把当前文件压缩为 .1.gz 并滚动旧归档（.1→.2…，超出 keep 删除）。
func (w *rotatingWriter) archiveLocked() error {
	if w.keep <= 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(fmt.Sprintf("%s.%d.gz", w.path, w.keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for i := w.keep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d.gz", w.path, i)
		to := fmt.Sprintf("%s.%d.gz", w.path, i+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	// 先写临时文件再改名：进程中途退出也不会留下半截归档。
	dst := w.path + ".1.gz"
	tmp := dst + ".tmp"
	if err := gzipFile(w.path, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// gzipFile 以 0600 把 src 压缩写入 dst。
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
