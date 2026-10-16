package credential

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// FallbackFile keeps secrets as a JSON map in one file, written 0600 under
// a 0700 directory. It is the Store used where the OS keychain is missing.
type FallbackFile struct {
	path string
	mu   sync.Mutex
}

var _ Store = (*FallbackFile)(nil)

func NewFallbackFile(path string) *FallbackFile { return &FallbackFile{path: path} }

func (f *FallbackFile) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (f *FallbackFile) flush(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, b, 0o600)
}

func (f *FallbackFile) Set(key string, secret []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = string(secret)
	return f.flush(m)
}

func (f *FallbackFile) Get(key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return nil, err
	}
	v, ok := m[key]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(v), nil
}

func (f *FallbackFile) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return ErrNotFound
	}
	delete(m, key)
	return f.flush(m)
}
