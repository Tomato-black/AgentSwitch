package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"AgentSwitch/internal/credential"
)

func tempSecretStore(t *testing.T) (Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestSecretsRoundtripAndAtRestCipher(t *testing.T) {
	s, _ := tempSecretStore(t)
	sec := s.Secrets()
	if err := sec.Set("channel:c1", []byte("sk-plain-secret")); err != nil {
		t.Fatal(err)
	}
	got, err := sec.Get("channel:c1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "sk-plain-secret" {
		t.Fatalf("get %q", got)
	}
	// 静止态：DB 文件里绝不能出现明文字节。
	ds := s.(*dbStore)
	var blob []byte
	if err := ds.db.QueryRow(`SELECT ciphertext FROM channel_secrets WHERE credential_ref='channel:c1'`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("sk-plain-secret")) {
		t.Fatal("ciphertext at rest contains plaintext")
	}
}

func TestSecretsMissReturnsErrNotFound(t *testing.T) {
	s, _ := tempSecretStore(t)
	if _, err := s.Secrets().Get("channel:none"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("miss must be ErrNotFound, got %v", err)
	}
	if err := s.Secrets().Delete("channel:none"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("delete miss must be ErrNotFound, got %v", err)
	}
}

func TestSecretsOverwriteOnSet(t *testing.T) {
	s, _ := tempSecretStore(t)
	sec := s.Secrets()
	if err := sec.Set("channel:c1", []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("channel:c1", []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := sec.Get("channel:c1")
	if err != nil || string(got) != "new" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestSecretsDeleteRemovesRow(t *testing.T) {
	s, _ := tempSecretStore(t)
	sec := s.Secrets()
	if err := sec.Set("channel:c1", []byte("sk")); err != nil {
		t.Fatal(err)
	}
	if err := sec.Delete("channel:c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sec.Get("channel:c1"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("after delete must be ErrNotFound, got %v", err)
	}
}

func TestSecretsMasterKeyRotatedYieldsErrDecrypt(t *testing.T) {
	s, dbPath := tempSecretStore(t)
	if err := s.Secrets().Set("channel:c1", []byte("sk")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// 换主密钥（模拟 master.key 丢失后重新生成）。
	mkPath := filepath.Join(filepath.Dir(dbPath), "master.key")
	if err := os.Remove(mkPath); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	if _, err := s2.Secrets().Get("channel:c1"); !errors.Is(err, credential.ErrDecrypt) {
		t.Fatalf("rotated key must yield ErrDecrypt, got %v", err)
	}
}

func TestSecretsConcurrentSetGet(t *testing.T) {
	s, _ := tempSecretStore(t)
	sec := s.Secrets()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "channel:c" + string(rune('a'+i))
			if err := sec.Set(key, []byte("sk")); err != nil {
				t.Errorf("set: %v", err)
				return
			}
			if _, err := sec.Get(key); err != nil {
				t.Errorf("get: %v", err)
			}
		}(i)
	}
	wg.Wait()
}
