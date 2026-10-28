package bridge

import (
	"errors"
	"strings"
)

// repo 层原样返回存储错误，友好化是 bridge 的约定：以下助手把 SQLite 的
// UNIQUE 约束冲突译成面向用户的中文文案，其余错误原样透传。

// isUniqueViolation 判断错误是否为 UNIQUE 约束冲突（SQLite 文案）。
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

// uniqueErr 把 UNIQUE 冲突译成给定中文文案；非冲突错误原样返回。
func uniqueErr(err error, msg string) error {
	if isUniqueViolation(err) {
		return errors.New(msg)
	}
	return err
}
