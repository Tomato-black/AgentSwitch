// Package credential abstracts where the gateway keeps provider API keys
// and account secrets: the OS credential store first, a 0600 file second,
// chosen per call by Manager.
package credential

import "errors"

// Sentinel errors of the package. Callers match them with errors.Is.
var (
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("credential store unavailable")
)

// Store is a place secrets live in: the OS keychain, the fallback file,
// or Manager over both.
type Store interface {
	Set(key string, secret []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// AccountKey returns the store key for an account's credential.
func AccountKey(accountID string) string { return "account:" + accountID }

// Manager stores secrets in Primary, falling back to Fallback when Primary
// reports ErrUnavailable and AllowFallback is true.
type Manager struct {
	Primary       Store
	Fallback      Store
	AllowFallback bool
}

var _ Store = (*Manager)(nil)

// useFallback reports whether err from Primary diverts the call to Fallback.
func (m *Manager) useFallback(err error) bool {
	return errors.Is(err, ErrUnavailable) && m.AllowFallback && m.Fallback != nil
}

func (m *Manager) Set(key string, secret []byte) error {
	if err := m.Primary.Set(key, secret); err != nil {
		if m.useFallback(err) {
			return m.Fallback.Set(key, secret)
		}
		return err
	}
	return nil
}

func (m *Manager) Get(key string) ([]byte, error) {
	v, err := m.Primary.Get(key)
	if err != nil {
		if m.useFallback(err) {
			return m.Fallback.Get(key)
		}
		return nil, err
	}
	return v, nil
}

// Delete removes the key from both stores, tolerating a miss in either:
// a secret that only ever lived in the fallback file must still delete cleanly.
func (m *Manager) Delete(key string) error {
	err := m.Primary.Delete(key)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	if m.Fallback != nil {
		if ferr := m.Fallback.Delete(key); ferr != nil && !errors.Is(ferr, ErrNotFound) && err == nil {
			err = ferr
		}
	}
	return err
}
