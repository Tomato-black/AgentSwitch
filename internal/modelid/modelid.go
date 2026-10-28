// Package modelid 是模型名 [1M] 标记的纯助手：大小写不敏感的后缀剥离与
// 追加。网关路由（入站模型名）、bridge 接入合成与桌面编辑器共用同一实现，
// 避免多处语义漂移（项目曾专门处理智谱小写 [1m] 兼容）。
package modelid

import "strings"

// Suffix 是 1M 上下文声明的规范后缀（写入时统一大写）。
const Suffix = "[1M]"

// SplitOneM 剥离模型名尾部 "[1M]"（大小写不敏感），返回基名与是否带标记。
// 非尾部或空值的 "[]" 之类不受影响。
func SplitOneM(v string) (string, bool) {
	if len(v) >= len(Suffix) && strings.EqualFold(v[len(v)-len(Suffix):], Suffix) {
		return v[:len(v)-len(Suffix)], true
	}
	return v, false
}

// AppendOneM 在 oneM 为真时给模型名追加规范后缀 "[1M]"。
func AppendOneM(name string, oneM bool) string {
	if oneM {
		return name + Suffix
	}
	return name
}
