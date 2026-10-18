package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

// MasterKey 是 AES-256-GCM 主密钥：加密落库的 API key 由它 seal，文件存
// 数据库同目录 master.key（0600）。文件丢失时 LoadMasterKey 重新生成——
// 旧密文将不可解（Open 报 ErrDecrypt），用户重存渠道 key 即恢复。
type MasterKey struct {
	key []byte
}

const masterKeyLen = 32

// LoadMasterKey 读取 path 处主密钥；不存在则生成 32 字节随机密钥并以
// O_EXCL+0600 原子创建（并发首启只有一个赢家）。已存在但长度不对视为损坏。
func LoadMasterKey(path string) (*MasterKey, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != masterKeyLen {
			return nil, fmt.Errorf("master key file %s: want %d bytes, got %d", path, masterKeyLen, len(b))
		}
		return &MasterKey{key: b}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, masterKeyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// 并发竞争：另一路已创建，读它。
			return LoadMasterKey(path)
		}
		return nil, err
	}
	if _, err := f.Write(k); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &MasterKey{key: k}, nil
}

// Seal 以 AES-256-GCM 加密 plaintext，输出 nonce(12)||ciphertext+tag。
func (k *MasterKey) Seal(plaintext []byte) ([]byte, error) {
	gcm, err := k.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open 解密 Seal 的输出；密钥不匹配或密文损坏统一报 ErrDecrypt。
func (k *MasterKey) Open(sealed []byte) ([]byte, error) {
	gcm, err := k.gcm()
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

func (k *MasterKey) gcm() (cipher.AEAD, error) {
	if len(k.key) != masterKeyLen {
		return nil, fmt.Errorf("master key: want %d bytes, got %d", masterKeyLen, len(k.key))
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
