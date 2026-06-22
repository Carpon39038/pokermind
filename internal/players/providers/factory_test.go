package providers

import (
	"fmt"
	"testing"
)

func TestByKind(t *testing.T) {
	cases := []struct {
		kind    string
		wantErr bool
		wantTyp string
	}{
		{"openai", false, "*providers.OpenAICompatProvider"},
		{"anthropic", false, "*providers.AnthropicProvider"},
		{"", true, ""},
		{"unknown", true, ""},
		{"OPENAI", false, "*providers.OpenAICompatProvider"}, // 大小写容错
	}
	for _, c := range cases {
		p, err := ByKind(c.kind, "https://x", "k", nil)
		if c.wantErr {
			if err == nil {
				t.Errorf("kind %q: want err got nil (%T)", c.kind, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("kind %q: unexpected err %v", c.kind, err)
			continue
		}
		typ := fmt.Sprintf("%T", p)
		if typ != c.wantTyp {
			t.Errorf("kind %q: type = %s want %s", c.kind, typ, c.wantTyp)
		}
	}
}
