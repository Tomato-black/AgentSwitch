package modelid

import "testing"

// TestSplitOneM 覆盖后缀剥离：大小写不敏感（智谱官方为小写 [1m]）、只有
// 尾部生效、纯后缀剥成空串（随后未登记模型 404，行为明确）。
func TestSplitOneM(t *testing.T) {
	cases := map[string]struct {
		name string
		oneM bool
	}{
		"pub-1[1M]":         {"pub-1", true},
		"pub-1[1m]":         {"pub-1", true},
		"pub-1":             {"pub-1", false},
		"[]":                {"[]", false},
		"[1M]":              {"", true},
		"a[1M]x":            {"a[1M]x", false},
		"glm-5.3-flash[1m]": {"glm-5.3-flash", true},
	}
	for in, want := range cases {
		name, oneM := SplitOneM(in)
		if name != want.name || oneM != want.oneM {
			t.Fatalf("SplitOneM(%q)=%q,%v want %q,%v", in, name, oneM, want.name, want.oneM)
		}
	}
}

// TestAppendOneM 覆盖追加：oneM 时追加规范大写后缀，否则原样返回。
func TestAppendOneM(t *testing.T) {
	if got := AppendOneM("my-code", true); got != "my-code[1M]" {
		t.Fatalf("AppendOneM = %q", got)
	}
	if got := AppendOneM("my-code", false); got != "my-code" {
		t.Fatalf("AppendOneM = %q", got)
	}
	// 往返：追加后剥回原名。
	name, oneM := SplitOneM(AppendOneM("m", true))
	if name != "m" || !oneM {
		t.Fatalf("round trip = %q,%v", name, oneM)
	}
}
