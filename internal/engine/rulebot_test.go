package engine

import "testing"

// mkObs 是测试辅助:快速构造 Observation。
func mkObs(hole []Card, community []Card, toCall int) Observation {
	return Observation{
		Street:    Flop, // 默认;具体测试按需覆盖
		HoleCards: hole,
		Community: community,
		ToCall:    toCall,
	}
}

func TestRuleBotSatisfiesPlayerInterface(t *testing.T) {
	// 编译期断言 RuleBot 实现 Player
	var _ Player = RuleBot{}
}

func TestRuleBotPreflopPocketPairCalls(t *testing.T) {
	bot := RuleBot{}
	// 底牌 K♠ K♥,无公共牌,ToCall=10
	obs := Observation{
		Street:    Preflop,
		HoleCards: []Card{{Rank: 13, Suit: 0}, {Rank: 13, Suit: 1}},
		Community: nil,
		ToCall:    10,
	}
	if got := bot.Decide(obs); got.Type != Call {
		t.Fatalf("pocket pair preflop = %v, want Call", got.Type)
	}
}

func TestRuleBotPreflopNoPairFoldsWithCost(t *testing.T) {
	bot := RuleBot{}
	obs := Observation{
		Street:    Preflop,
		HoleCards: []Card{{Rank: 13, Suit: 0}, {Rank: 2, Suit: 1}}, // K 2 不同对
		Community: nil,
		ToCall:    10,
	}
	if got := bot.Decide(obs); got.Type != Fold {
		t.Fatalf("no pair preflop with cost = %v, want Fold", got.Type)
	}
}

func TestRuleBotPreflopNoPairChecksFree(t *testing.T) {
	bot := RuleBot{}
	obs := Observation{
		Street:    Preflop,
		HoleCards: []Card{{Rank: 13, Suit: 0}, {Rank: 2, Suit: 1}},
		Community: nil,
		ToCall:    0, // 免费
	}
	if got := bot.Decide(obs); got.Type != Call {
		t.Fatalf("no pair preflop free = %v, want Call (check)", got.Type)
	}
}

func TestRuleBotFlopHitsPairCalls(t *testing.T) {
	bot := RuleBot{}
	// 底牌 A 5,flop 5 2 9 → 一对 5
	obs := mkObs(
		[]Card{{Rank: 14, Suit: 0}, {Rank: 5, Suit: 1}},
		[]Card{{Rank: 5, Suit: 2}, {Rank: 2, Suit: 3}, {Rank: 9, Suit: 0}},
		20,
	)
	if got := bot.Decide(obs); got.Type != Call {
		t.Fatalf("flop pair = %v, want Call", got.Type)
	}
}

func TestRuleBotFlopNoPairFoldsWithCost(t *testing.T) {
	bot := RuleBot{}
	// 底牌 A K,flop 2 5 9 → 高牌,无对
	obs := mkObs(
		[]Card{{Rank: 14, Suit: 0}, {Rank: 13, Suit: 1}},
		[]Card{{Rank: 2, Suit: 2}, {Rank: 5, Suit: 3}, {Rank: 9, Suit: 0}},
		20,
	)
	if got := bot.Decide(obs); got.Type != Fold {
		t.Fatalf("flop no pair with cost = %v, want Fold", got.Type)
	}
}

func TestRuleBotNeverRaises(t *testing.T) {
	bot := RuleBot{}
	// 即便强牌(满堂红),也只是 Call,不 Raise
	obs := mkObs(
		[]Card{{Rank: 14, Suit: 0}, {Rank: 14, Suit: 1}}, // AA
		[]Card{{Rank: 14, Suit: 2}, {Rank: 7, Suit: 0}, {Rank: 7, Suit: 1}}, // AAA 77 = full house
		50,
	)
	if got := bot.Decide(obs); got.Type != Call {
		t.Fatalf("strong hand = %v, want Call (RuleBot never raises)", got.Type)
	}
}

func TestRuleBotEndToEndPlayHand(t *testing.T) {
	// RuleBot vs 总是 call 的对手,固定 seed,确保能跑完不 panic
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: RuleBot{}},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(123), 1)

	// 筹码守恒
	if got := result.FinalStacks[0] + result.FinalStacks[1]; got != 2000 {
		t.Fatalf("chips not conserved: %d", got)
	}
	// 最后一个事件必须是 HandFinished
	if events[len(events)-1].Type != HandFinished {
		t.Fatalf("last event = %v, want HandFinished", events[len(events)-1].Type)
	}
	// 必须有赢家
	if len(result.Winners) == 0 {
		t.Fatalf("no winners")
	}
}
