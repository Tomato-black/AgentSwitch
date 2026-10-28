package main

import "AgentSwitch/internal/provider"

func providerAnthropicEndpoint(base string) (string, error) {
	// 复用 provider 的端点推导：加 /v1/messages 后缀。
	return provider.JoinAPIPath(base, "/v1/messages")
}
