package credential

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMasterKeyCreates0600AndStableReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	k1, err := LoadMasterKey(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("master.key mode %v want 0600", fi.Mode().Perm())
	}
	k2, err := LoadMasterKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1.key, k2.key) {
		t.Fatal("reload must return the same key")
	}
}

func TestSealOpenRoundtrip(t *testing.T) {
	k, err := LoadMasterKey(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("sk-test-超 secret")
	sealed, err := k.Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	got, err := k.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("roundtrip %q want %q", got, secret)
	}
}

func TestOpenWithWrongKeyFailsDecrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	k1, err := LoadMasterKey(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := k1.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// 换一把主密钥（模拟 master.key 被替换/丢失后重新生成）。
	k2 := &MasterKey{key: make([]byte, 32)}
	if _, err := rand.Read(k2.key); err != nil {
		t.Fatal(err)
	}
	if _, err := k2.Open(sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong key must yield ErrDecrypt, got %v", err)
	}
}

func TestOpenShortBlobFailsDecrypt(t *testing.T) {
	k, err := LoadMasterKey(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Open([]byte("short")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("short blob must yield ErrDecrypt, got %v", err)
	}
}

func TestLoadMasterKeyRejectsWrongLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, make([]byte, 31), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMasterKey(path); err == nil {
		t.Fatal("31-byte key file must be rejected")
	}
}
