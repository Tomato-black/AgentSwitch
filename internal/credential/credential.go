// Package credential 维护渠道密钥加密用的主密钥：库同目录 master.key
// （32 字节、0600、缺失自动生成）。密文本体由 storage.SecretRepo 承载；
// OS 钥匙串与明文降级文件已退役（2026-10-18 存储改造）。
package credential

import "errors"

// Sentinel errors of the package. Callers match them with errors.Is.
var (
	ErrNotFound = errors.New("secret not found")
	// ErrDecrypt：密文存在但无法用当前主密钥解开（master.key 丢失后重新生成等）。
	ErrDecrypt = errors.New("secret cannot be decrypted (master key mismatch)")
)

// Store is a place secrets live in — production impl is storage's encrypted
// SQLite store; tests use an in-memory map fake.
type Store interface {
	Set(key string, secret []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// ChannelKey returns the store key for a channel's credential (single key per
// channel; ref kept in channels.credential_ref).
func ChannelKey(channelID string) string { return "channel:" + channelID }
