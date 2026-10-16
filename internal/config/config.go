package config

import (
	"fmt"
	"strconv"
)

const (
	KeyPort         = "server.port"
	KeyTimeout      = "server.request_timeout_seconds"
	KeyGrace        = "server.shutdown_grace_seconds"
	KeyMaxBody      = "server.max_body_bytes"
	KeyMaxConc      = "server.max_concurrent"
	KeyToken        = "auth.gateway_token"
	KeyCredFallback = "credential.fallback_enabled"
)

type Settings struct {
	Port                  int
	RequestTimeoutSeconds int
	ShutdownGraceSeconds  int
	MaxBodyBytes          int64
	MaxConcurrent         int
	GatewayToken          string
	CredentialFallback    bool
}

func Default() Settings {
	return Settings{
		Port: 17890, RequestTimeoutSeconds: 120, ShutdownGraceSeconds: 15,
		MaxBodyBytes: 10 << 20, MaxConcurrent: 32,
	}
}

func (s Settings) Validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("server.port out of range: %d", s.Port)
	}
	if s.RequestTimeoutSeconds <= 0 {
		return fmt.Errorf("server.request_timeout_seconds must be > 0")
	}
	if s.ShutdownGraceSeconds < 0 {
		return fmt.Errorf("server.shutdown_grace_seconds must be >= 0")
	}
	if s.MaxBodyBytes <= 0 || s.MaxConcurrent <= 0 {
		return fmt.Errorf("limits must be > 0")
	}
	return nil
}

// Load 从 get 读键；空串视为缺失用默认值。
func Load(get func(key string) (string, error)) (Settings, error) {
	s := Default()
	readInt := func(key string, dst *int) error {
		v, err := get(key)
		if err != nil || v == "" {
			return err
		}
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", key, perr)
		}
		*dst = n
		return nil
	}
	if err := readInt(KeyPort, &s.Port); err != nil {
		return s, err
	}
	if err := readInt(KeyTimeout, &s.RequestTimeoutSeconds); err != nil {
		return s, err
	}
	if err := readInt(KeyGrace, &s.ShutdownGraceSeconds); err != nil {
		return s, err
	}
	if v, err := get(KeyMaxBody); err != nil {
		return s, err
	} else if v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return s, fmt.Errorf("parse %s: %w", KeyMaxBody, perr)
		}
		s.MaxBodyBytes = n
	}
	if err := readInt(KeyMaxConc, &s.MaxConcurrent); err != nil {
		return s, err
	}
	if v, err := get(KeyToken); err != nil {
		return s, err
	} else {
		s.GatewayToken = v
	}
	if v, err := get(KeyCredFallback); err != nil {
		return s, err
	} else {
		s.CredentialFallback = v == "true"
	}
	return s, s.Validate()
}

func (s Settings) Save(set func(key, value string) error) error {
	if err := s.Validate(); err != nil {
		return err
	}
	pairs := map[string]string{
		KeyPort: strconv.Itoa(s.Port), KeyTimeout: strconv.Itoa(s.RequestTimeoutSeconds),
		KeyGrace: strconv.Itoa(s.ShutdownGraceSeconds), KeyMaxBody: strconv.FormatInt(s.MaxBodyBytes, 10),
		KeyMaxConc: strconv.Itoa(s.MaxConcurrent), KeyToken: s.GatewayToken,
		KeyCredFallback: strconv.FormatBool(s.CredentialFallback),
	}
	for k, v := range pairs {
		if err := set(k, v); err != nil {
			return fmt.Errorf("write %s: %w", k, err)
		}
	}
	return nil
}
