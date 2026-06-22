package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// AnthropicProvider 走 Anthropic messages API。
// 端点:BaseURL + "/v1/messages"。system 放在顶层而非 messages。
type AnthropicProvider struct {
	BaseURL string // 不含末尾 /,如 "https://api.anthropic.com"
	APIKey  string
	HTTP    *http.Client
}

// anthropicRequest 是 Anthropic messages API 的请求体。
type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	MaxTokens   int                `json:"max_tokens,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse 是 Anthropic messages API 的响应体(只解析需要的字段)。
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// ChatComplete 实现 Provider。
func (p *AnthropicProvider) ChatComplete(ctx context.Context, req ChatRequest) (string, error) {
	if p.BaseURL == "" {
		return "", fmt.Errorf("AnthropicProvider: BaseURL empty")
	}
	if p.APIKey == "" {
		return "", fmt.Errorf("AnthropicProvider: APIKey empty")
	}

	// 把 system message 拆出来(Anthropic 不放 messages 数组)
	var systemText string
	body := anthropicRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			if systemText != "" {
				systemText += "\n"
			}
			systemText += m.Content
			continue
		}
		body.Messages = append(body.Messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	body.System = systemText

	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := p.HTTP
	if client == nil {
		client = DefaultHTTPClient(0)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, buf.String())
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	for _, block := range parsed.Content {
		if block.Type == "text" && block.Text != "" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic returned no text content")
}

// 编译期保证接口实现
var _ Provider = (*AnthropicProvider)(nil)
