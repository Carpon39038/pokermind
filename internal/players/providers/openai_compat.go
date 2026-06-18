package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// OpenAICompatProvider 是 OpenAI 兼容的 HTTP 后端。
// DeepSeek(https://api.deepseek.com/v1)与智谱(https://open.bigmodel.cn/api/paas/v4)
// 共用此实现,只是 BaseURL/APIKey/Model 不同。
type OpenAICompatProvider struct {
	BaseURL string           // 不含末尾 /,如 "https://api.deepseek.com/v1"
	APIKey  string
	HTTP    *http.Client
}

// chatMessage 是 wire format(对外 JSON 用),与内部 Message 区分。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest 是 OpenAI 兼容的请求体。
type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature,omitempty"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
}

type respFormat struct {
	Type string `json:"type"` // "json_object"
}

// chatResponse 是 OpenAI 兼容的响应体(只解析需要的字段)。
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	// Error 字段:不同 provider 错误体不同,这里只在 HTTP 非 2xx 时报错(见下方)
}

// ChatComplete 实现 Provider。
func (p *OpenAICompatProvider) ChatComplete(ctx context.Context, req ChatRequest) (string, error) {
	if p.BaseURL == "" {
		return "", fmt.Errorf("OpenAICompatProvider: BaseURL empty")
	}
	if p.APIKey == "" {
		return "", fmt.Errorf("OpenAICompatProvider: APIKey empty")
	}

	body := chatRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	if req.ResponseFormatJSON {
		body.ResponseFormat = &respFormat{Type: "json_object"}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

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
		// 读 body 用于错误信息
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, buf.String())
	}

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("provider returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}
