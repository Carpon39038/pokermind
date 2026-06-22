package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicProvider_ChatComplete_request_and_parse(t *testing.T) {
	var gotHeaders http.Header
	var gotBody map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)

		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "hello world"},
			},
		}
		b, _ := json.Marshal(resp)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		BaseURL: strings.TrimSuffix(srv.URL, "/v1"),
		APIKey:  "sk-test",
	}
	out, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:       "claude-3-5-sonnet-20241022",
		Temperature: 0.7,
		MaxTokens:   100,
		Messages: []Message{
			{Role: "system", Content: "be good"},
			{Role: "user", Content: "hi"},
		},
		ResponseFormatJSON: true, // Anthropic 应忽略此字段
	})
	if err != nil {
		t.Fatalf("ChatComplete err: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q want %q", out, "hello world")
	}

	// header 校验
	if gotHeaders.Get("x-api-key") != "sk-test" {
		t.Errorf("x-api-key header missing/wrong: %q", gotHeaders.Get("x-api-key"))
	}
	if gotHeaders.Get("anthropic-version") == "" {
		t.Errorf("anthropic-version header missing")
	}
	// path 校验
	if !strings.HasSuffix(gotPath, "/v1/messages") {
		t.Errorf("path = %q want suffix /v1/messages", gotPath)
	}
	// body 校验:system 应在顶层,不在 messages 里
	if sys, _ := gotBody["system"].(string); sys != "be good" {
		t.Errorf("system field = %v want %q", gotBody["system"], "be good")
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages len = %d want 1 (system 应拆出)", len(msgs))
	}
}

func TestAnthropicProvider_missing_baseurl_or_key(t *testing.T) {
	p := &AnthropicProvider{}
	if _, err := p.ChatComplete(context.Background(), ChatRequest{}); err == nil {
		t.Error("expected error for empty BaseURL")
	}
	p.BaseURL = "https://x"
	if _, err := p.ChatComplete(context.Background(), ChatRequest{}); err == nil {
		t.Error("expected error for empty APIKey")
	}
}

func TestAnthropicProvider_HTTP_non_2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	p := &AnthropicProvider{BaseURL: srv.URL, APIKey: "k"}
	_, err := p.ChatComplete(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("want HTTP 400 err, got %v", err)
	}
}
