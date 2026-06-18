package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatChatCompleteSuccess(t *testing.T) {
	// 模拟上游:校验请求体,返回固定 content
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("auth header = %q, want Bearer ...", got)
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("model = %s, want test-model", req.Model)
		}
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Errorf("response_format = %v, want json_object", req.ResponseFormat)
		}
		// 回一个标准 OpenAI 兼容响应
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "{\"action\":{\"type\":\"call\"}}"}},
			},
		})
	}))
	defer srv.Close()

	p := &OpenAICompatProvider{BaseURL: srv.URL, APIKey: "test-key", HTTP: srv.Client()}
	got, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:              "test-model",
		Messages:           []Message{{Role: "user", Content: "hi"}},
		ResponseFormatJSON: true,
		Temperature:        0.5,
	})
	if err != nil {
		t.Fatalf("ChatComplete err: %v", err)
	}
	if !strings.Contains(got, "call") {
		t.Fatalf("content = %q, want contains 'call'", got)
	}
}

func TestOpenAICompatErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := &OpenAICompatProvider{BaseURL: srv.URL, APIKey: "bad", HTTP: srv.Client()}
	_, err := p.ChatComplete(context.Background(), ChatRequest{Model: "x"})
	if err == nil {
		t.Fatalf("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want contains 401", err)
	}
}

func TestOpenAICompatErrorOnEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()

	p := &OpenAICompatProvider{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	_, err := p.ChatComplete(context.Background(), ChatRequest{Model: "x"})
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("err = %v, want 'no choices'", err)
	}
}

func TestOpenAICompatValidatesConfig(t *testing.T) {
	p := &OpenAICompatProvider{}
	_, err := p.ChatComplete(context.Background(), ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "BaseURL") {
		t.Fatalf("err = %v, want BaseURL empty", err)
	}
	p.BaseURL = "http://x"
	_, err = p.ChatComplete(context.Background(), ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "APIKey") {
		t.Fatalf("err = %v, want APIKey empty", err)
	}
}
