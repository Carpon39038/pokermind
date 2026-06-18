package players

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pokermind/internal/engine"
	"pokermind/internal/players/providers"
)

// fakeProvider 是测试用的 Provider,按预置队列返回内容或错误。
type fakeProvider struct {
	responses []string
	errs      []error
	calls     int
	lastReq   providers.ChatRequest
}

func (f *fakeProvider) ChatComplete(_ context.Context, req providers.ChatRequest) (string, error) {
	f.lastReq = req
	idx := f.calls
	f.calls++
	var err error
	if idx < len(f.errs) {
		err = f.errs[idx]
	}
	if err != nil {
		return "", err
	}
	if idx < len(f.responses) {
		return f.responses[idx], nil
	}
	return "", errors.New("fakeProvider: no more responses")
}

// mkObs 构造一个最小可用的 Observation。
func mkObs() engine.Observation {
	return engine.Observation{
		HandID:    1,
		Street:    engine.Preflop,
		HoleCards: []engine.Card{{Rank: 14, Suit: 0}, {Rank: 14, Suit: 1}}, // AA
		Community: nil,
		Pot:       15,
		ToCall:    5,
		MinRaise:  20,
		MyStack:   985,
		MyBet:     5,
		OpponentBet: 10,
		IsButton:  true,
	}
}

func TestBuildUserPromptContainsKeyFields(t *testing.T) {
	s := buildUserPrompt(mkObs())
	for _, want := range []string{"Hand #1", "preflop", "As Ah", "Pot: 15", "ToCall", "MinRaise (minimum raise-to total): 20", "button"} {
		if !strings.Contains(s, want) {
			t.Errorf("prompt missing %q\ngot:\n%s", want, s)
		}
	}
}

func TestParseDecisionValid(t *testing.T) {
	in := `{"reasoning":"strong pair","hand_strength":0.85,"estimated_equity":0.8,"is_bluffing":false,"action":{"type":"raise","amount":30}}`
	d, err := parseDecision(in)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if d.Action.Type != "raise" || d.Action.Amount != 30 {
		t.Fatalf("got %+v", d)
	}
	if d.HandStrength != 0.85 {
		t.Fatalf("hs = %v", d.HandStrength)
	}
}

func TestParseDecisionStripsCodeFences(t *testing.T) {
	in := "```json\n{\"reasoning\":\"x\",\"action\":{\"type\":\"call\"}}\n```"
	if _, err := parseDecision(in); err != nil {
		t.Fatalf("expected ok with fences, got %v", err)
	}
}

func TestParseDecisionInvalidJSON(t *testing.T) {
	if _, err := parseDecision("not json at all"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateDecisionCall(t *testing.T) {
	d := rawDecision{}
	d.Action.Type = "call"
	d.Reasoning = "ok"
	a, err := validateDecision(d, mkObs())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.Type != engine.Call {
		t.Fatalf("type = %v", a.Type)
	}
	if a.SelfReport == nil || a.SelfReport.Reasoning != "ok" {
		t.Fatalf("self report = %+v", a.SelfReport)
	}
}

func TestValidateDecisionRaiseBelowMinPanics(t *testing.T) {
	// (实际不是 panic,而是返回 error 由 LLMPlayer 决定重试/fallback)
	obs := mkObs() // MinRaise=20, MyBet=5, MyStack=985
	d := rawDecision{}
	d.Action.Type = "raise"
	d.Action.Amount = 15 // < MinRaise 20,且不是 all-in(delta=10 != stack 985)
	if _, err := validateDecision(d, obs); err == nil {
		t.Fatalf("expected error for raise below min")
	}
}

func TestValidateDecisionRaiseAllInBelowMinAllowed(t *testing.T) {
	obs := mkObs()
	obs.MyStack = 10 // 模拟短筹
	obs.MyBet = 5
	// raise-to 15:delta=10=stack,all-in,虽低于 MinRaise 20 但允许
	d := rawDecision{}
	d.Action.Type = "raise"
	d.Action.Amount = 15
	a, err := validateDecision(d, obs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.Type != engine.Raise || a.Amount != 15 {
		t.Fatalf("got %+v", a)
	}
}

func TestValidateDecisionUnknownType(t *testing.T) {
	d := rawDecision{}
	d.Action.Type = "allin" // 不识别
	if _, err := validateDecision(d, mkObs()); err == nil {
		t.Fatalf("expected error for unknown type")
	}
}

func TestValidateDecisionClampsSelfReport(t *testing.T) {
	d := rawDecision{HandStrength: 1.5, EstimatedEquity: -0.3}
	d.Action.Type = "call"
	a, _ := validateDecision(d, mkObs())
	if a.SelfReport.HandStrength != 1.0 {
		t.Errorf("hs = %v, want 1.0", a.SelfReport.HandStrength)
	}
	if a.SelfReport.EstimatedEquity != 0 {
		t.Errorf("eq = %v, want 0", a.SelfReport.EstimatedEquity)
	}
}

func TestLLMPlayerSuccessFirstTry(t *testing.T) {
	fp := &fakeProvider{
		responses: []string{
			`{"reasoning":"aces","hand_strength":0.9,"estimated_equity":0.85,"is_bluffing":false,"action":{"type":"raise","amount":30}}`,
		},
	}
	p := &LLMPlayer{Provider: fp, Model: "test"}
	a := p.Decide(mkObs())
	if a.Type != engine.Raise || a.Amount != 30 {
		t.Fatalf("got %+v", a)
	}
	if a.SelfReport == nil || a.SelfReport.HandStrength != 0.9 {
		t.Fatalf("self report = %+v", a.SelfReport)
	}
	if fp.calls != 1 {
		t.Fatalf("calls = %d, want 1", fp.calls)
	}
}

func TestLLMPlayerRetriesThenSucceeds(t *testing.T) {
	fp := &fakeProvider{
		responses: []string{
			"garbage", // attempt 0: parse fail
			`{"reasoning":"x","action":{"type":"raise","amount":5}}`, // attempt 1: validate fail (< MyBet)
			`{"reasoning":"ok","action":{"type":"call"}}`,            // attempt 2: success
		},
	}
	p := &LLMPlayer{Provider: fp, Model: "test", MaxRetries: 3}
	a := p.Decide(mkObs())
	if a.Type != engine.Call {
		t.Fatalf("got %+v", a.Type)
	}
	if fp.calls != 3 {
		t.Fatalf("calls = %d, want 3", fp.calls)
	}
}

func TestLLMPlayerFallbackFoldOnAllFail(t *testing.T) {
	fp := &fakeProvider{
		responses: []string{"junk1", "junk2", "junk3"},
	}
	p := &LLMPlayer{Provider: fp, Model: "test", MaxRetries: 2}
	a := p.Decide(mkObs())
	if a.Type != engine.Fold {
		t.Fatalf("got %+v, want Fold fallback", a.Type)
	}
	if a.SelfReport == nil || !strings.Contains(a.SelfReport.Reasoning, "failed") {
		t.Fatalf("fallback reasoning = %+v", a.SelfReport)
	}
	if fp.calls != 3 { // 1 + 2 retries
		t.Fatalf("calls = %d, want 3", fp.calls)
	}
}

func TestLLMPlayerProviderErrorTriggersRetry(t *testing.T) {
	fp := &fakeProvider{
		errs:      []error{errors.New("network down")},
		responses: []string{"", `{"reasoning":"ok","action":{"type":"call"}}`},
	}
	p := &LLMPlayer{Provider: fp, Model: "test", MaxRetries: 2}
	a := p.Decide(mkObs())
	if a.Type != engine.Call {
		t.Fatalf("got %+v", a.Type)
	}
}

// 编译期断言:LLMPlayer 实现 engine.Player
func TestLLMPlayerImplementsEnginePlayer(t *testing.T) {
	var _ engine.Player = (*LLMPlayer)(nil)
}
