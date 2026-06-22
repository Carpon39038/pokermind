// Package providers 是 LLM HTTP 后端抽象。
// OpenAI 兼容格式打底,各 provider(DeepSeek/智谱)只是 base url + key 不同。
package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Provider 是一个 LLM HTTP 后端。
type Provider interface {
	// ChatComplete 发一轮 chat,返回 assistant 的文本内容。
	ChatComplete(ctx context.Context, req ChatRequest) (string, error)
}

// ChatRequest 是给 Provider 的请求参数。
type ChatRequest struct {
	Model              string
	Messages           []Message
	ResponseFormatJSON bool   // 要求严格 JSON 输出;provider 不支持时由实现自行忽略
	Temperature        float64
	MaxTokens          int
}

// Message 是 chat 的一条消息。
type Message struct {
	Role    string // system / user / assistant
	Content string
}

// DefaultHTTPClient 返回带超时的默认 http.Client。
func DefaultHTTPClient(timeoutSec int) *http.Client {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	return &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
}

// ByKind 按 kind 字符串构造 Provider。
// kind 取值(大小写不敏感):"openai" | "anthropic"。
func ByKind(kind, baseURL, apiKey string, http *http.Client) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "openai":
		return &OpenAICompatProvider{BaseURL: baseURL, APIKey: apiKey, HTTP: http}, nil
	case "anthropic":
		return &AnthropicProvider{BaseURL: baseURL, APIKey: apiKey, HTTP: http}, nil
	default:
		return nil, fmt.Errorf("ByKind: unknown kind %q (want openai or anthropic)", kind)
	}
}
