package bridge

import "testing"

func TestComposeAccessEnvClaudeMergePrecedence(t *testing.T) {
	st := Status{Port: 17890, Token: "tok123"}
	env, err := composeAccessEnv("claude", st, "my-code",
		map[string]string{"HTTPS_PROXY": "http://p:1"},
		map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": "fast"})
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" {
		t.Fatal(env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok123" {
		t.Fatal("token 未带入")
	}
	if env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_SMALL_FAST_MODEL"] != "fast" {
		t.Fatal(env)
	}
	if env["HTTPS_PROXY"] != "http://p:1" {
		t.Fatal("全局 env 未合并")
	}
}

func TestComposeAccessEnvBaseKeysImmutable(t *testing.T) {
	st := Status{Port: 17890, Token: "tok123"}
	env, err := composeAccessEnv("claude", st, "m",
		map[string]string{"ANTHROPIC_BASE_URL": "http://evil:1"},
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "x", "ANTHROPIC_MODEL": "evil"})
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" {
		t.Fatal("基础键被全局覆盖")
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok123" || env["ANTHROPIC_MODEL"] != "m" {
		t.Fatal("基础键被 overrides 覆盖")
	}
}

func TestComposeAccessEnvRejectsUnknownAppType(t *testing.T) {
	if _, err := composeAccessEnv("gemini", Status{}, "m", nil, nil); err == nil {
		t.Fatal("白名单外 appType 应报错")
	}
}
