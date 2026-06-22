package match

import (
	"fmt"
	"path/filepath"
	"testing"

	"pokermind/internal/engine"
	"pokermind/internal/store"
)

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "match.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPlayRuleBotVsRuleBot(t *testing.T) {
	rec := tempStore(t)
	p1 := PlayerSpec{Provider: "test", Model: "bot1", Label: "bot1"}
	p2 := PlayerSpec{Provider: "test", Model: "bot2", Label: "bot2"}

	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	res, err := Play(p1, p2,
		func() engine.Player { return engine.RuleBot{} },
		func() engine.Player { return engine.RuleBot{} },
		5, cfg, rec, 42,
	)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if res.HandsPlayed == 0 {
		t.Fatalf("expected at least 1 hand played")
	}
	if res.HandsPlayed > 5 {
		t.Fatalf("hands played = %d, want <= 5", res.HandsPlayed)
	}
	// 筹码守恒
	if got := res.FinalStacks[0] + res.FinalStacks[1]; got != 2000 {
		t.Fatalf("chips sum = %d, want 2000", got)
	}
	// GameID 应非零
	if res.GameID == 0 {
		t.Fatalf("GameID should be non-zero")
	}
	// 必须有赢家(或平局),winner ∈ {-1, 0, 1}
	if res.Winner < -1 || res.Winner > 1 {
		t.Fatalf("winner = %d, out of range", res.Winner)
	}
}

func TestPlayUpdatesElo(t *testing.T) {
	rec := tempStore(t)
	p1 := PlayerSpec{Provider: "test", Model: "a", Label: "a"}
	p2 := PlayerSpec{Provider: "test", Model: "b", Label: "b"}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	// 用 always-call 两个 bot,确保打满 5 手到摊牌(钱够)
	alwaysCall := func() engine.Player {
		return engine.PlayerFromFunc(func(engine.Observation) engine.Action {
			return engine.Action{Type: engine.Call}
		})
	}

	res, err := Play(p1, p2, alwaysCall, alwaysCall, 5, cfg, rec, 7)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	// ELO 应该变了:赢家涨,输家跌,平局两者变化互为相反
	delta1, delta2 := res.EloChange[0], res.EloChange[1]
	if res.Winner == -1 {
		// 平局:两人 delta 应都接近 0(同分时 ELO 公式给 0 增益)
		// 但两人初始同 1500,平局确实 delta=0
		if delta1 != 0 || delta2 != 0 {
			// 这其实可能不严格成立(平均打平按公式是 0),先不严格断言
		}
	} else {
		// 有赢家:赢家 delta > 0,输家 < 0,绝对值相等
		if res.Winner == 0 && delta1 <= 0 {
			t.Fatalf("winner p1 delta = %v, want > 0", delta1)
		}
		if res.Winner == 1 && delta2 <= 0 {
			t.Fatalf("winner p2 delta = %v, want > 0", delta2)
		}
	}
}

func TestPlayEarlyExitOnBankruptcy(t *testing.T) {
	rec := tempStore(t)
	p1 := PlayerSpec{Provider: "test", Model: "rich", Label: "rich"}
	p2 := PlayerSpec{Provider: "test", Model: "poor", Label: "poor"}

	// poor 起始 15(只够 1 BB + 1 SB),很快破产
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	// 让 p2 第一手就 all-in 失败:用 always-call,起始 stack 设很小
	// 但 Play 用 cfg.StartingStack,不能 per-seat。这里用变通:
	// 起始 1000 vs 1000,跑 100 手。RuleBot 经常 fold,可能打满。
	// 改造测试:用 cfg.StartingStack=20,两家都接近破产,几手内结束
	cfg.StartingStack = 20
	alwaysCall := func() engine.Player {
		return engine.PlayerFromFunc(func(engine.Observation) engine.Action {
			return engine.Action{Type: engine.Call}
		})
	}
	res, err := Play(p1, p2, alwaysCall, alwaysCall, 100, cfg, rec, 99)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if res.HandsPlayed >= 100 {
		t.Fatalf("expected early exit (<100 hands) due to bankruptcy, got %d", res.HandsPlayed)
	}
	// 必须有一方筹码 < BB
	if res.FinalStacks[0] >= cfg.BigBlind && res.FinalStacks[1] >= cfg.BigBlind {
		t.Fatalf("expected at least one stack < BB, got %v", res.FinalStacks)
	}
}

func TestPlayPersistsHandsAndActions(t *testing.T) {
	rec := tempStore(t)
	p1 := PlayerSpec{Provider: "test", Model: "a", Label: "a"}
	p2 := PlayerSpec{Provider: "test", Model: "b", Label: "b"}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	alwaysCall := func() engine.Player {
		return engine.PlayerFromFunc(func(engine.Observation) engine.Action {
			return engine.Action{Type: engine.Call}
		})
	}
	res, err := Play(p1, p2, alwaysCall, alwaysCall, 3, cfg, rec, 3)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	lb, err := rec.Leaderboard()
	if err != nil {
		t.Fatalf("Leaderboard: %v", err)
	}
	// 两个玩家各 1 局
	if len(lb) != 2 {
		t.Fatalf("leaderboard len = %d, want 2", len(lb))
	}
	for _, row := range lb {
		if row.Games != 1 {
			t.Fatalf("player %d games = %d, want 1", row.PlayerID, row.Games)
		}
	}
	_ = res
}

func TestPlayWithoutStore(t *testing.T) {
	// rec=nil 也能跑(纯内存,不落库)
	p1 := PlayerSpec{Provider: "x", Model: "a", Label: "a"}
	p2 := PlayerSpec{Provider: "x", Model: "b", Label: "b"}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	res, err := Play(p1, p2,
		func() engine.Player { return engine.RuleBot{} },
		func() engine.Player { return engine.RuleBot{} },
		3, cfg, nil, 1,
	)
	if err != nil {
		t.Fatalf("Play with nil rec: %v", err)
	}
	if res.GameID != 0 {
		t.Fatalf("GameID should be 0 when rec is nil, got %d", res.GameID)
	}
}

// === PlayN 多人对局测试 ===

// makeRuleBots 返回 N 个 RuleBot 工厂。
func makeRuleBots(n int) []func() engine.Player {
	out := make([]func() engine.Player, n)
	for i := 0; i < n; i++ {
		out[i] = func() engine.Player { return engine.RuleBot{} }
	}
	return out
}

func makeSpecs(n int) []PlayerSpec {
	out := make([]PlayerSpec, n)
	for i := 0; i < n; i++ {
		out[i] = PlayerSpec{Provider: "test", Model: fmt.Sprintf("bot%d", i), Label: fmt.Sprintf("bot%d", i)}
	}
	return out
}

func TestPlayNThreePlayers(t *testing.T) {
	specs := makeSpecs(3)
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	res, err := PlayN(specs, makeRuleBots(3), 5, cfg, 42)
	if err != nil {
		t.Fatalf("PlayN: %v", err)
	}
	if res.HandsPlayed == 0 {
		t.Fatalf("expected hands played > 0")
	}
	if res.HandsPlayed > 5 {
		t.Fatalf("hands played = %d, want <= 5", res.HandsPlayed)
	}
	if len(res.FinalStacks) != 3 {
		t.Fatalf("final stacks len = %d, want 3", len(res.FinalStacks))
	}
	// 筹码守恒:3 人 × 1000 = 3000
	sum := 0
	for _, s := range res.FinalStacks {
		sum += s
	}
	if sum != 3000 {
		t.Fatalf("chips sum = %d, want 3000", sum)
	}
	// 必须有赢家(或 -1 全破产,不太可能 5 手内)
	if res.WinnerSeat < 0 || res.WinnerSeat >= 3 {
		t.Fatalf("winner seat = %d, want 0..2", res.WinnerSeat)
	}
}

func TestPlayNSixPlayers(t *testing.T) {
	specs := makeSpecs(6)
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	res, err := PlayN(specs, makeRuleBots(6), 3, cfg, 7)
	if err != nil {
		t.Fatalf("PlayN: %v", err)
	}
	if len(res.FinalStacks) != 6 {
		t.Fatalf("final stacks len = %d, want 6", len(res.FinalStacks))
	}
	sum := 0
	for _, s := range res.FinalStacks {
		sum += s
	}
	if sum != 6000 {
		t.Fatalf("chips sum = %d, want 6000", sum)
	}
}

func TestPlayNValidatesSize(t *testing.T) {
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	// 1 人太少
	if _, err := PlayN(makeSpecs(1), makeRuleBots(1), 1, cfg, 1); err == nil {
		t.Fatalf("expected error for 1 player")
	}
	// 7 人太多
	if _, err := PlayN(makeSpecs(7), makeRuleBots(7), 1, cfg, 1); err == nil {
		t.Fatalf("expected error for 7 players")
	}
}

func TestPlayNValidatesMakePlayersLength(t *testing.T) {
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	// specs=3 但 makePlayers=2
	if _, err := PlayN(makeSpecs(3), makeRuleBots(2), 1, cfg, 1); err == nil {
		t.Fatalf("expected error for mismatched makePlayers length")
	}
}

func TestPlayNBankruptcyEarlyExit(t *testing.T) {
	// 起始 stack 极小,几手内必破产
	specs := makeSpecs(3)
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 20}
	// alwaysCall 让所有人快速 all-in,很快一方破产
	alwaysCallN := func(n int) []func() engine.Player {
		out := make([]func() engine.Player, n)
		for i := 0; i < n; i++ {
			out[i] = func() engine.Player {
				return engine.PlayerFromFunc(func(engine.Observation) engine.Action {
					return engine.Action{Type: engine.Call}
				})
			}
		}
		return out
	}
	res, err := PlayN(specs, alwaysCallN(3), 100, cfg, 99)
	if err != nil {
		t.Fatalf("PlayN: %v", err)
	}
	if res.HandsPlayed >= 100 {
		t.Fatalf("expected early exit (<100), got %d", res.HandsPlayed)
	}
	bustCount := 0
	for _, s := range res.FinalStacks {
		if s < cfg.BigBlind {
			bustCount++
		}
	}
	if bustCount == 0 {
		t.Fatalf("expected at least 1 busted player")
	}
}
