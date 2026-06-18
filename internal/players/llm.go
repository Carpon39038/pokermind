// Package players 包含实现 engine.Player 的具体玩家(LLM/规则 bot 等)。
package players

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"pokermind/internal/engine"
	"pokermind/internal/players/providers"
)

// LLMPlayer 用一个 LLM provider 实现 engine.Player。
// 每个决策点拼 prompt 调 provider,解析结构化 JSON(含内心戏),失败重试,
// 全失败 fallback 为 fold。
type LLMPlayer struct {
	Provider   providers.Provider
	Model      string
	MaxRetries int          // JSON 解析/校验失败重试次数,默认 2
	Timeout    time.Duration // 单次调用超时,默认 60s
}

// defaultMaxRetries 是 MaxRetries 为 0 时的默认值。
const defaultMaxRetries = 2

// defaultTimeout 是 Timeout 为 0 时的默认值。
const defaultTimeout = 60 * time.Second

// systemPromptText 是 system message,定义角色与输出格式。
const systemPromptText = `You are a Heads-up No-Limit Texas Hold'em poker player.
Decide the best action for the situation described.

Output STRICT JSON, no markdown, no extra text:
{
  "reasoning": "1-3 sentences explaining your read",
  "hand_strength": <float 0.0-1.0, your self-assessed hand strength>,
  "estimated_equity": <float 0.0-1.0, your estimated probability of winning>,
  "is_bluffing": <boolean, true if you're trying to make opponent fold a better hand>,
  "action": {
    "type": "fold" | "call" | "raise",
    "amount": <integer, ONLY for raise: the total amount you raise TO (raise-to), must be >= MinRaise unless going all-in>
  }
}

Rules:
- "call" matches the current bet. When ToCall is 0, "call" means check (free).
- "raise" amount is the TOTAL bet you commit this street (raise-to), NOT the increment.
- "raise" amount must be >= MinRaise, unless you are going all-in (putting all remaining stack in).
- "fold" gives up the hand. You lose what you've already bet.
- Do not include "amount" for fold/call (it will be ignored).`

// Decide 实现 engine.Player。
func (p *LLMPlayer) Decide(obs engine.Observation) engine.Action {
	maxRetries := p.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	prompt := buildUserPrompt(obs)
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		text, err := p.Provider.ChatComplete(ctx, providers.ChatRequest{
			Model: p.Model,
			Messages: []providers.Message{
				{Role: "system", Content: systemPromptText},
				{Role: "user", Content: prompt},
			},
			ResponseFormatJSON: true,
			Temperature:        0.7,
		})
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("attempt %d provider: %w", attempt, err)
			continue
		}

		decision, err := parseDecision(text)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d parse: %w", attempt, err)
			continue
		}

		action, err := validateDecision(decision, obs)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d validate: %w", attempt, err)
			continue
		}
		return action
	}

	// 所有重试用尽:fallback fold
	return engine.Action{
		Type: engine.Fold,
		SelfReport: &engine.SelfReport{
			Reasoning: fmt.Sprintf("LLM failed after %d attempts: %v", maxRetries+1, lastErr),
		},
	}
}

// rawDecision 是 provider 返回内容的 JSON 结构。
type rawDecision struct {
	Reasoning       string  `json:"reasoning"`
	HandStrength    float64 `json:"hand_strength"`
	EstimatedEquity float64 `json:"estimated_equity"`
	IsBluffing      bool    `json:"is_bluffing"`
	Action          struct {
		Type   string `json:"type"`
		Amount int    `json:"amount"`
	} `json:"action"`
}

// parseDecision 把 provider 文本解析为 rawDecision。
// 容错:模型可能把 JSON 包在 ```json ... ``` 里,先剥离。
func parseDecision(text string) (rawDecision, error) {
	var d rawDecision
	cleaned := stripCodeFences(text)
	if err := json.Unmarshal([]byte(cleaned), &d); err != nil {
		return d, fmt.Errorf("unmarshal: %w (text: %q)", err, truncate(text, 200))
	}
	return d, nil
}

// stripCodeFences 去掉模型可能加的 ```json ... ``` 包裹。
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	for _, fence := range []string{"```json", "```"} {
		if strings.HasPrefix(s, fence) {
			s = strings.TrimPrefix(s, fence)
			s = strings.TrimSuffix(strings.TrimSpace(s), "```")
			s = strings.TrimSpace(s)
		}
	}
	return s
}

// truncate 截断字符串到 n 字符(用于错误信息)。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// validateDecision 把 rawDecision 转为引擎可用的 Action 并校验合法性。
// 返回的 Action 带 SelfReport(内心戏)。
// 校验失败返回 error(由调用方决定重试还是 fallback)。
func validateDecision(d rawDecision, obs engine.Observation) (engine.Action, error) {
	var t engine.ActionType
	switch strings.ToLower(strings.TrimSpace(d.Action.Type)) {
	case "fold":
		t = engine.Fold
	case "call", "check":
		t = engine.Call
	case "raise", "bet":
		t = engine.Raise
	default:
		return engine.Action{}, fmt.Errorf("unknown action type %q", d.Action.Type)
	}

	sr := &engine.SelfReport{
		HandStrength:    clamp01(d.HandStrength),
		EstimatedEquity: clamp01(d.EstimatedEquity),
		IsBluffing:      d.IsBluffing,
		Reasoning:       d.Reasoning,
	}

	if t == engine.Raise {
		// raise-to 合法性:必须 > 当前已投;必须 >= MinRaise(除非 all-in)
		if d.Action.Amount <= obs.MyBet {
			return engine.Action{}, fmt.Errorf("raise amount %d must be > current bet %d", d.Action.Amount, obs.MyBet)
		}
		delta := d.Action.Amount - obs.MyBet
		if delta > obs.MyStack {
			return engine.Action{}, fmt.Errorf("raise amount %d exceeds stack (delta %d > stack %d)", d.Action.Amount, delta, obs.MyStack)
		}
		// 不足 minRaise 仅在 all-in 时允许
		isAllIn := delta == obs.MyStack
		if d.Action.Amount < obs.MinRaise && !isAllIn {
			return engine.Action{}, fmt.Errorf("raise amount %d below minRaise %d (and not all-in)", d.Action.Amount, obs.MinRaise)
		}
	}

	return engine.Action{Type: t, Amount: d.Action.Amount, SelfReport: sr}, nil
}

// clamp01 把 v 夹到 [0,1]。
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// buildUserPrompt 把 Observation 拼成给模型的 user message。
func buildUserPrompt(obs engine.Observation) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Hand #%d, Street: %s\n", obs.HandID, obs.Street)
	fmt.Fprintf(&sb, "Your hole cards: %s\n", cardsStr(obs.HoleCards))
	fmt.Fprintf(&sb, "Community: %s\n", orNone(cardsStr(obs.Community)))
	fmt.Fprintf(&sb, "Pot: %d\n", obs.Pot)
	fmt.Fprintf(&sb, "ToCall (amount needed to call): %d\n", obs.ToCall)
	fmt.Fprintf(&sb, "MinRaise (minimum raise-to total): %d\n", obs.MinRaise)
	fmt.Fprintf(&sb, "Your stack: %d\n", obs.MyStack)
	fmt.Fprintf(&sb, "Your bet this street: %d\n", obs.MyBet)
	fmt.Fprintf(&sb, "Opponent bet this street: %d\n", obs.OpponentBet)
	if obs.IsButton {
		fmt.Fprintf(&sb, "Position: button (small blind)\n")
	} else {
		fmt.Fprintf(&sb, "Position: big blind\n")
	}
	sb.WriteString("\nReturn JSON now.")
	return sb.String()
}

// cardsStr 把 []Card 拼成 "As Th 5d" 形式。
func cardsStr(cs []engine.Card) string {
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

// orNone 空串返回 "(none)"。
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
