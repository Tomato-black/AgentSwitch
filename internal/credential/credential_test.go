package credential

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestChannelKey(t *testing.T) {
	if ChannelKey("abc") != "channel:abc" {
		t.Fatal(ChannelKey("abc"))
	}
}

func TestFallbackRoundTripAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	f := NewFallbackFile(path)
	if err := f.Set("account:1", []byte("sk-secret")); err != nil {
		t.Fatal(err)
	}
	got, err := f.Get("account:1")
	if err != nil || string(got) != "sk-secret" {
		t.Fatalf("get: %q %v", got, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v want 0600", st.Mode().Perm())
	}
	if err := f.Delete("account:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("account:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

type fakeStore struct {
	m           map[string][]byte
	unavailable bool
}

func (f *fakeStore) Set(k string, v []byte) error {
	if f.unavailable {
		return ErrUnavailable
	}
	if f.m == nil {
		f.m = map[string][]byte{}
	}
	f.m[k] = v
	return nil
}
func (f *fakeStore) Get(k string) ([]byte, error) {
	if f.unavailable {
		return nil, ErrUnavailable
	}
	v, ok := f.m[k]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}
func (f *fakeStore) Delete(k string) error {
	if f.unavailable {
		return ErrUnavailable
	}
	delete(f.m, k)
	return nil
}

func TestManagerFallsBackWhenAllowed(t *testing.T) {
	primary := &fakeStore{unavailable: true}
	fb := NewFallbackFile(filepath.Join(t.TempDir(), "fb.json"))
	m := Manager{Primary: primary, Fallback: fb, AllowFallback: true}
	if err := m.Set("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Get("k"); err != nil || string(got) != "v" {
		t.Fatalf("fallback get: %q %v", got, err)
	}
}

func TestManagerRefusesFallbackWhenNotAllowed(t *testing.T) {
	primary := &fakeStore{unavailable: true}
	fb := NewFallbackFile(filepath.Join(t.TempDir(), "fb.json"))
	m := Manager{Primary: primary, Fallback: fb, AllowFallback: false}
	if err := m.Set("k", []byte("v")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestManagerDelete(t *testing.T) {
	t.Run("primary unavailable falls back when allowed", func(t *testing.T) {
		primary := &fakeStore{unavailable: true}
		fb := NewFallbackFile(filepath.Join(t.TempDir(), "fb.json"))
		if err := fb.Set("k", []byte("v")); err != nil {
			t.Fatal(err)
		}
		m := Manager{Primary: primary, Fallback: fb, AllowFallback: true}
		if err := m.Delete("k"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := fb.Get("k"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("fallback still holds key, get err = %v", err)
		}
	})

	t.Run("primary unavailable refused when fallback not allowed", func(t *testing.T) {
		primary := &fakeStore{unavailable: true}
		fb := NewFallbackFile(filepath.Join(t.TempDir(), "fb.json"))
		if err := fb.Set("k", []byte("v")); err != nil {
			t.Fatal(err)
		}
		m := Manager{Primary: primary, Fallback: fb, AllowFallback: false}
		if err := m.Delete("k"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("want ErrUnavailable, got %v", err)
		}
	})
}
