package storage

import (
	"database/sql"
	"errors"

	"AgentSwitch/internal/credential"
)

// SecretRepo 是 channel_secrets 表的加密凭据存取：Set 入库即 AES-256-GCM
// 密文，Get 解密回明文；key 为主密钥丢失等不可解时报 credential.ErrDecrypt，
// 缺失报 credential.ErrNotFound。与 ChannelRepo 同姿态：单语句、无显式事务
// （WAL + busy_timeout 覆盖 gateway/UI 并发）。
type SecretRepo interface {
	Set(key string, secret []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

type secretStore struct {
	db  *sql.DB
	key *credential.MasterKey
}

var _ credential.Store = (*secretStore)(nil)

func (s *secretStore) Set(key string, secret []byte) error {
	ct, err := s.key.Seal(secret)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO channel_secrets(credential_ref, ciphertext) VALUES (?1, ?2)
		ON CONFLICT(credential_ref) DO UPDATE SET ciphertext = excluded.ciphertext`, key, ct)
	return err
}

func (s *secretStore) Get(key string) ([]byte, error) {
	var ct []byte
	if err := s.db.QueryRow(`SELECT ciphertext FROM channel_secrets WHERE credential_ref = ?1`, key).Scan(&ct); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, credential.ErrNotFound
		}
		return nil, err
	}
	return s.key.Open(ct)
}

func (s *secretStore) Delete(key string) error {
	res, err := s.db.Exec(`DELETE FROM channel_secrets WHERE credential_ref = ?1`, key)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return credential.ErrNotFound
	}
	return nil
}
