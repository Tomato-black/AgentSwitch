package credential

import (
	"errors"

	"github.com/egoist/mygo"
)

// OSKeychain stores secrets in the OS credential store via mygo.Secrets,
// mapping its errors onto this package's sentinels.
type OSKeychain struct{}

var _ Store = OSKeychain{}

func (OSKeychain) Set(key string, secret []byte) error {
	if err := mygo.Secrets.Set(key, secret); err != nil {
		return wrapOSErr(err)
	}
	return nil
}

func (OSKeychain) Get(key string) ([]byte, error) {
	v, err := mygo.Secrets.Get(key)
	if err != nil {
		return nil, wrapOSErr(err)
	}
	return v, nil
}

func (OSKeychain) Delete(key string) error {
	if err := mygo.Secrets.Delete(key); err != nil {
		return wrapOSErr(err)
	}
	return nil
}

// wrapOSErr maps mygo's secret errors onto ErrNotFound / ErrUnavailable,
// passing anything else through unchanged.
func wrapOSErr(err error) error {
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return ErrUnavailable
	}
	return err
}

// OSKeychainAvailable reports whether the current environment has a usable
// system credential store, probed by writing and removing a throwaway key.
func OSKeychainAvailable() bool {
	err := OSKeychain{}.Set("agentswitch.probe", []byte("1"))
	if err == nil {
		OSKeychain{}.Delete("agentswitch.probe")
		return true
	}
	return !errors.Is(err, ErrUnavailable)
}
