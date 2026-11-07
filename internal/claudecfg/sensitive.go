package claudecfg

import (
	"math"
	"strings"
)

var sensitiveWords = []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "AUTH"}

var secretPrefixes = []string{
	"sk-", "sk_", "ghp_", "github_pat_", "xoxb-", "xoxp-", "AKIA", "AIza",
}

// IsSensitivePath 报告点分路径的末段是否像密钥字段。
func IsSensitivePath(path string) bool {
	parts := strings.Split(path, ".")
	name := strings.ToUpper(parts[len(parts)-1])
	for _, w := range sensitiveWords {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// MaskSecret 遮蔽敏感值，保留可辨认的前缀。
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "••••"
	}
	keep := 2
	if len(s) < 8 {
		keep = 1
	}
	return s[:keep] + strings.Repeat("•", 8)
}

// ContainsSecretLike 扫描文档中的字符串值，判断是否像密钥。
// 用于项目共享文件保存前的警告，不阻断保存。
func ContainsSecretLike(doc map[string]any) bool {
	return containsSecretLikeValue(doc)
}

func containsSecretLikeValue(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for _, item := range x {
			if containsSecretLikeValue(item) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if containsSecretLikeValue(item) {
				return true
			}
		}
	case string:
		return looksLikeSecret(x)
	}
	return false
}

func looksLikeSecret(s string) bool {
	if len(s) < 20 {
		return false
	}
	lower := strings.ToLower(s)
	for _, p := range secretPrefixes {
		if strings.HasPrefix(s, p) || strings.HasPrefix(lower, strings.ToLower(p)) {
			return true
		}
	}
	return shannonEntropy(s) >= 3.2
}

func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]float64{}
	for _, r := range s {
		counts[r]++
	}
	total := float64(len([]rune(s)))
	var e float64
	for _, c := range counts {
		p := c / total
		e -= p * math.Log2(p)
	}
	return e
}
