package engine

import (
	"math/rand"
	"testing"
)

func TestStreetString(t *testing.T) {
	cases := []struct {
		s    Street
		want string
	}{
		{Preflop, "preflop"},
		{Flop, "flop"},
		{Turn, "turn"},
		{River, "river"},
		{Showdown, "showdown"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestActionTypeString(t *testing.T) {
	cases := []struct {
		a    ActionType
		want string
	}{
		{Fold, "fold"},
		{Call, "call"},
		{Raise, "raise"},
	}
	for _, tc := range cases {
		if got := tc.a.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.a, got, tc.want)
		}
	}
}

// makeRng 返回固定 seed 的 *rand.Rand,便于复现。
func makeRng(seed int) *rand.Rand { return rand.New(rand.NewSource(int64(seed))) }

// alwaysFold 总是弃牌。
func alwaysFold() func(obs Observation) Action {
	return func(obs Observation) Action { return Action{Type: Fold} }
}

// alwaysCall 总是跟注/check。
func alwaysCall() func(obs Observation) Action {
	return func(obs Observation) Action { return Action{Type: Call} }
}

func TestSetupHandBlinds(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, events := setupHand(seats, 0, cfg, makeRng(42), 1)

	if st.stacks[0] != 995 {
		t.Fatalf("SB stack = %d, want 995", st.stacks[0])
	}
	if st.stacks[1] != 990 {
		t.Fatalf("BB stack = %d, want 990", st.stacks[1])
	}
	if st.pot != 0 {
		t.Fatalf("pot = %d, want 0", st.pot)
	}
	if st.bets[0] != 5 || st.bets[1] != 10 {
		t.Fatalf("bets = %v, want [5 10]", st.bets)
	}
	if len(events) != 4 {
		t.Fatalf("events count = %d, want 4", len(events))
	}
	if events[0].Type != BlindPosted || events[0].Seat != 0 || events[0].Amount != 5 {
		t.Fatalf("event[0] = %+v, want BlindPosted seat0 amt5", events[0])
	}
	if events[1].Type != BlindPosted || events[1].Seat != 1 || events[1].Amount != 10 {
		t.Fatalf("event[1] = %+v, want BlindPosted seat1 amt10", events[1])
	}
	if len(st.hole[0]) != 2 || len(st.hole[1]) != 2 {
		t.Fatalf("hole cards len = %v %v, want 2 2", len(st.hole[0]), len(st.hole[1]))
	}
	if st.hole[0][0] == st.hole[1][0] {
		t.Fatalf("duplicate hole card across players")
	}
}

func TestSetupHandButtonIsSB(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, _ := setupHand(seats, 1, cfg, makeRng(7), 1)
	if st.sb != 1 || st.bb != 0 {
		t.Fatalf("sb/bb = %d/%d, want 1/0", st.sb, st.bb)
	}
	if st.stacks[1] != 995 || st.stacks[0] != 990 {
		t.Fatalf("stacks = %v %v, want seat1=995(SB) seat0=990(BB)", st.stacks[1], st.stacks[0])
	}
}

func TestSetupHandShortStackAllIn(t *testing.T) {
	// 起始 stack 不足 SB,BB 应只投剩余全部
	seats := []PlayerSeat{
		{ID: 0, Stack: 3, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 10, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 10}
	st, events := setupHand(seats, 0, cfg, makeRng(1), 1)
	// SB 应只投 3(all-in),event amount=3
	if st.stacks[0] != 0 || !st.allIn[0] {
		t.Fatalf("SB should be all-in, stack=%d allIn=%v", st.stacks[0], st.allIn[0])
	}
	if events[0].Amount != 3 {
		t.Fatalf("SB posted = %d, want 3", events[0].Amount)
	}
}

// TestSetupHandDeckIsShuffled 防回归:deck 必须在发牌前洗牌。
// 未洗的 deck 顶部 4 张是顺序的(2c 3c 4c 5c),洗牌后不同 seed 之间应不同。
func TestSetupHandDeckIsShuffled(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	st1, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	st2, _ := setupHand(seats, 0, cfg, makeRng(2), 1)

	// 不同 seed 必须发出不同的底牌(否则 deck 没洗)
	same := true
	for i := 0; i < 2; i++ {
		if st1.hole[0][i] != st2.hole[0][i] || st1.hole[1][i] != st2.hole[1][i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("two different seeds produced identical hole cards (deck not shuffled?): %v vs %v",
			st1.hole, st2.hole)
	}

	// 同一 seed 应可复现(确定性)
	st1b, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	if st1b.hole[0][0] != st1.hole[0][0] {
		t.Fatalf("same seed should produce same first card")
	}
}

func TestFirstActor(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	if st.firstActor() != 0 {
		t.Fatalf("preflop firstActor (button=0) = %d, want 0", st.firstActor())
	}
	st.street = Flop
	if st.firstActor() != 1 {
		t.Fatalf("flop firstActor (button=0, BB=1) = %d, want 1", st.firstActor())
	}

	st2, _ := setupHand(seats, 1, cfg, makeRng(1), 1)
	if st2.firstActor() != 1 {
		t.Fatalf("preflop firstActor (button=1) = %d, want 1", st2.firstActor())
	}
	st2.street = Flop
	if st2.firstActor() != 0 {
		t.Fatalf("flop firstActor (button=1, BB=0) = %d, want 0", st2.firstActor())
	}
}

func TestRunStreetPreflopBothCall(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)

	var events []Event
	events = st.runStreet(events)

	// SB(0)先 call(补 5),BB(1)再 call(已是 10,ToCall=0,check)
	if st.bets[0] != 10 || st.bets[1] != 10 {
		t.Fatalf("bets = %v, want [10 10]", st.bets)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Seat != 0 {
		t.Fatalf("first actor = %d, want 0 (SB)", events[0].Seat)
	}
	if events[1].Seat != 1 {
		t.Fatalf("second actor = %d, want 1 (BB)", events[1].Seat)
	}
}

func TestRunStreetFoldEndsStreet(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysFold())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)

	var events []Event
	events = st.runStreet(events)

	if !st.folded[0] {
		t.Fatalf("seat0 should have folded")
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (single fold)", len(events))
	}
}

func TestApplyActionRaiseToSemantics(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)

	// SB(0)raise-to 30
	_, ev := st.applyAction(0, Action{Type: Raise, Amount: 30})
	if st.bets[0] != 30 {
		t.Fatalf("after raise-to 30, bets[0] = %d, want 30", st.bets[0])
	}
	// SB 原 995,从 5 补到 30 = 补 25,剩 970
	if st.stacks[0] != 970 {
		t.Fatalf("stack[0] = %d, want 970", st.stacks[0])
	}
	if ev.Action.Type != Raise || ev.Action.Amount != 30 {
		t.Fatalf("event action = %+v, want raise-to 30", ev.Action)
	}
}

func TestApplyActionRaiseBelowMinPanics(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	// max=10(BB),minRaiseTo=20。SB raise-to 15(< 20,且非 all-in)应 panic
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for raise below min")
		}
	}()
	st.applyAction(0, Action{Type: Raise, Amount: 15})
}

func TestApplyActionRaiseAllInBelowMinAllowed(t *testing.T) {
	// 短筹 all-in:即使 raise-to 低于 min,只要是把剩余 stack 全押上,允许
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	// 调整 SB 的 stack 到 14:已投 SB 5,剩 9。raise-to 14(all-in,< min 20)
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	st.stacks[0] = 9 // 模拟 SB 剩 9
	st.bets[0] = 5   // 已投 5,raise-to 14 = all-in
	allIn, _ := st.applyAction(0, Action{Type: Raise, Amount: 14})
	if !allIn {
		t.Fatalf("expected all-in")
	}
	if st.bets[0] != 14 {
		t.Fatalf("bets[0] = %d, want 14", st.bets[0])
	}
}

func TestApplyActionCallAllInTruncates(t *testing.T) {
	// call 时筹码不足以匹配,只投剩余全部并 all-in
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	// 让 BB stacks 模拟不足:BB 已投 10,只剩 3;对手 max=30 时 call 需 20,但只有 3
	st.stacks[1] = 3
	st.bets[0] = 30 // 模拟对手已 raise-to 30
	allIn, _ := st.applyAction(1, Action{Type: Call})
	if !allIn {
		t.Fatalf("expected all-in")
	}
	if st.bets[1] != 13 {
		t.Fatalf("bets[1] = %d, want 13 (10 + 3)", st.bets[1])
	}
}

// raiseOnceThenCall:第一次行动 raise-to amt,之后都 call/check。
func raiseOnceThenCall(amt int) func(obs Observation) Action {
	called := false
	return func(obs Observation) Action {
		if !called {
			called = true
			return Action{Type: Raise, Amount: amt}
		}
		return Action{Type: Call}
	}
}

func TestPlayHandFoldOnPreflop(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysFold())}, // SB 弃牌
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(1), 1)

	if !result.Folded {
		t.Fatalf("expected fold ending")
	}
	if len(result.Winners) != 1 || result.Winners[0] != 1 {
		t.Fatalf("winner = %v, want [1]", result.Winners)
	}
	// pot = SB+BB = 15 (fold 路径: pot + bets[0] + bets[1] = 0 + 5 + 10 = 15)
	if result.PotWon != 15 {
		t.Fatalf("pot won = %d, want 15", result.PotWon)
	}
	if events[len(events)-1].Type != HandFinished {
		t.Fatalf("last event = %v, want HandFinished", events[len(events)-1].Type)
	}
}

func TestPlayHandBothCallToShowdown(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	if result.Folded {
		t.Fatalf("expected showdown, got fold")
	}
	if result.Showdown == nil {
		t.Fatalf("expected ShowdownInfo")
	}
	// showdown 路径: advanceStreet 把 bets 并入 pot,所以 pot=20(SB 投 10, BB 投 10)
	if result.PotWon != 20 {
		t.Fatalf("pot won = %d, want 20", result.PotWon)
	}
	// 街推进次数:flop/turn/river/showdown = 4
	advCount := 0
	for _, e := range events {
		if e.Type == StreetAdvanced {
			advCount++
		}
	}
	if advCount != 4 {
		t.Fatalf("street advance count = %d, want 4", advCount)
	}
	// 公共牌应有 5 张(3+1+1)
	if len(events) > 0 {
		// 找最后一个 StreetAdvanced 之前的 community 累积
		// 简化:从事件流中收集所有翻出的公共牌
		var community []Card
		for _, e := range events {
			if e.Type == StreetAdvanced && e.Cards != nil {
				community = append(community, e.Cards...)
			}
		}
		if len(community) != 5 {
			t.Fatalf("community cards from events = %d, want 5", len(community))
		}
	}
}

func TestPlayHandRaisePreflopThenShowdown(t *testing.T) {
	// SB raise-to 30,BB call,然后都 check 到摊牌
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(raiseOnceThenCall(30))},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	_, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	// pot = 30 + 30 = 60(preflop raise-to 30, BB call 30,后续街都 check)
	if result.PotWon != 60 {
		t.Fatalf("pot won = %d, want 60", result.PotWon)
	}
}

func TestPlayHandAllInRunsOutBoard(t *testing.T) {
	// SB all-in(把剩余 995 全押,raise-to 1000),BB call 全部
	// 之后应直接翻完剩余 street 到摊牌
	sbDecide := func(obs Observation) Action {
		// SB 第一次行动就 all-in:raise-to = bets + stack = 5 + 995 = 1000
		return Action{Type: Raise, Amount: 1000}
	}
	bbDecide := func(obs Observation) Action {
		return Action{Type: Call} // BB call(筹码刚好够:1000 - 10 = 990,需补 990,刚好)
	}
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(sbDecide)},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(bbDecide)},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	if result.Folded {
		t.Fatalf("expected showdown, got fold")
	}
	if result.Showdown == nil {
		t.Fatalf("expected ShowdownInfo")
	}
	// 应该有 4 个 StreetAdvanced(flop/turn/river/showdown)
	advCount := 0
	for _, e := range events {
		if e.Type == StreetAdvanced {
			advCount++
		}
	}
	if advCount != 4 {
		t.Fatalf("street advance count = %d, want 4 (all-in runs out board)", advCount)
	}
}

func TestPlayHandChipsConserved(t *testing.T) {
	// 筹码守恒:PlayHand 后两人 stack 之和应为 2000
	// 验证 showdown 路径 pot=20(SB 投 10 + BB 投 10)
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	_, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	// showdown 路径: advanceStreet 把 bets[0]=10, bets[1]=10 并入 pot,所以 pot=20
	if result.PotWon != 20 {
		t.Fatalf("pot won = %d, want 20 (SB 10 + BB 10)", result.PotWon)
	}
	// 筹码守恒:两人最终 stack 之和 = 起始之和(2000)
	if got := result.FinalStacks[0] + result.FinalStacks[1]; got != 2000 {
		t.Fatalf("final stacks sum = %d, want 2000 (chips not conserved)", got)
	}
	// 赢家拿走 pot(20),输家不变。两人都投入 10,所以:
	// 赢家 = 1000 - 10 + 20 = 1010;输家 = 1000 - 10 = 990
	if result.Winners[0] == 0 {
		if result.FinalStacks[0] != 1010 || result.FinalStacks[1] != 990 {
			t.Fatalf("stacks = %v, want [1010 990]", result.FinalStacks)
		}
	} else {
		if result.FinalStacks[0] != 990 || result.FinalStacks[1] != 1010 {
			t.Fatalf("stacks = %v, want [990 1010]", result.FinalStacks)
		}
	}
}

// TestPlayHandThreePlayersFoldToWinner 是 N=3 的烟雾测试:三个 RuleBot,
// 都倾向 preflop 弃牌弱牌。验证:
//   - 多人盲注位正确(SB/BB/button)
//   - runStreet 多人行动顺序不 panic、能终止
//   - 弃牌结算(只剩 1 个未弃牌)正确,筹码守恒
//
// 注意:RuleBot 在拿到口袋对时会 call,所以可能进 flop —— 但只要一路 fold
// 到摊牌前结束就用 soleContender 路径,不碰 settleShowdown(多人摊牌待 2e)。
func TestPlayHandThreePlayersFoldToWinner(t *testing.T) {
	// 找一个 seed:三个 RuleBot 都拿非口袋对的弱牌 → 都 fold → soleContender 结束
	// 用 alwaysFold 强制让三人 fold,保证不进摊牌(避开多人 settle)
	foldAlways := func(obs Observation) Action { return Action{Type: Fold} }
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(foldAlways)},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(foldAlways)},
		{ID: 2, Stack: 1000, Player: PlayerFromFunc(alwaysCall())}, // 这个不弃,躺赢
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(1), 1)

	if !result.Folded {
		t.Fatalf("expected fold ending, got showdown (multi-player settle not yet supported)")
	}
	if len(result.Winners) != 1 || result.Winners[0] != 2 {
		t.Fatalf("winner = %v, want [2] (only non-folder)", result.Winners)
	}
	// 筹码守恒:三人 stack 之和 = 3000
	sum := 0
	for _, s := range result.FinalStacks {
		sum += s
	}
	if sum != 3000 {
		t.Fatalf("chips sum = %d, want 3000 (conservation)", sum)
	}
	// 至少应有一个 BlindPosted 事件 + 几个 ActionTaken(Fold)
	blindCount, foldCount := 0, 0
	for _, ev := range events {
		if ev.Type == BlindPosted {
			blindCount++
		}
		if ev.Type == ActionTaken && ev.Action != nil && ev.Action.Type == Fold {
			foldCount++
		}
	}
	if blindCount != 2 {
		t.Fatalf("blind events = %d, want 2 (SB + BB)", blindCount)
	}
	if foldCount != 2 {
		t.Fatalf("fold events = %d, want 2 (two folders)", foldCount)
	}
	// 最终事件应是 HandFinished
	if events[len(events)-1].Type != HandFinished {
		t.Fatalf("last event = %v, want HandFinished", events[len(events)-1].Type)
	}
}

// TestPlayHandThreePlayersBlinds 验证 N=3 的盲注位:button=0 时
// sb=1, bb=2(标准多人规则 sb=(button+1)%N, bb=(button+2)%N)。
func TestPlayHandThreePlayersBlinds(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 2, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	// 不调用 PlayHand(三人 alwaysCall 会一路打到摊牌,触发 N>2 settle panic);
	// 只验 setupHand 的盲注位
	st, events := setupHand(seats, 0, cfg, makeRng(1), 1)
	if st.sb != 1 || st.bb != 2 {
		t.Fatalf("N=3 button=0: sb/bb = %d/%d, want 1/2", st.sb, st.bb)
	}
	// 三人都应被扣盲注或发底牌:事件流应有 2 BlindPosted + 3 DealtHole
	if len(events) != 5 {
		t.Fatalf("setupHand events = %d, want 5 (2 blinds + 3 deals)", len(events))
	}
	// sb(1)扣 5,bb(2)扣 10,seat0 不扣
	if st.stacks[0] != 1000 || st.stacks[1] != 995 || st.stacks[2] != 990 {
		t.Fatalf("stacks = %v, want [1000 995 990]", st.stacks)
	}
}

// TestPlayHandThreePlayersShowdown 三人 always-call 跑到摊牌,验证:
//   - 不再 panic(N>=3 settle 已用 sidepot)
//   - 筹码守恒
//   - ShowdownInfo 填了三个 seat 的 Ranks/Best5
//   - 总赢家拿到最多钱
func TestPlayHandThreePlayersShowdown(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 2, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	if result.Folded {
		t.Fatalf("expected showdown, got fold")
	}
	if result.Showdown == nil {
		t.Fatalf("expected ShowdownInfo")
	}
	if len(result.Showdown.Ranks) != 3 {
		t.Fatalf("Ranks len = %d, want 3", len(result.Showdown.Ranks))
	}
	if len(result.Showdown.Best5) != 3 {
		t.Fatalf("Best5 len = %d, want 3", len(result.Showdown.Best5))
	}
	// 筹码守恒
	sum := 0
	for _, s := range result.FinalStacks {
		sum += s
	}
	if sum != 3000 {
		t.Fatalf("chips sum = %d, want 3000", sum)
	}
	// 必须有赢家
	if len(result.Winners) == 0 {
		t.Fatalf("no winners")
	}
	// 街推进:flop/turn/river/showdown = 4
	advCount := 0
	for _, e := range events {
		if e.Type == StreetAdvanced {
			advCount++
		}
	}
	if advCount != 4 {
		t.Fatalf("street advance count = %d, want 4", advCount)
	}
}

// TestPlayHandThreePlayersSidePot 三人,SB 短筹 all-in、其他两人深入。
// 验证边池:短筹玩家只能赢主池(三人共同部分),深入的两人在边池里争。
func TestPlayHandThreePlayersSidePot(t *testing.T) {
	// seat0 (SB): stack=20,投 SB 5 后剩 15 → 第一次决策 all-in(call-to 20)
	// seat1 (BB): stack=1000,正常
	// seat2:    stack=1000,正常
	// 都 alwaysCall,seat0 all-in 后另两人继续,最后三人摊牌
	// 主池(三人各 20)= 60,边池(seat1+seat2 各补超出的部分)= 其余
	sbDecide := func(obs Observation) Action {
		if obs.MyBet < 20 && obs.MyStack > 0 {
			// 第一次行动把剩余全押(call/raise to 20)
			target := obs.MyBet + obs.MyStack
			if target < obs.ToCall+obs.MyBet {
				target = obs.ToCall + obs.MyBet
			}
			if target <= obs.MyStack+obs.MyBet {
				return Action{Type: Raise, Amount: obs.MyStack + obs.MyBet}
			}
		}
		return Action{Type: Call}
	}
	seats := []PlayerSeat{
		{ID: 0, Stack: 20, Player: PlayerFromFunc(sbDecide)},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 2, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	_, result := PlayHand(seats, 0, cfg, makeRng(7), 1)

	// 筹码守恒
	sum := 0
	for _, s := range result.FinalStacks {
		sum += s
	}
	if sum != 2020 {
		t.Fatalf("chips sum = %d, want 2020", sum)
	}
	// seat0 all-in 最多投 20,他即使赢也只能拿三人共同部分(60)中的他应得份额
	// 所以 seat0 最终 stack 应 <= 60(实际取决于是否赢主池)
	if result.FinalStacks[0] > 60 {
		t.Fatalf("seat0 (short stack all-in) final = %d, must be <= 60 (main pot only)", result.FinalStacks[0])
	}
	// seat1/seat2 不应负数
	if result.FinalStacks[1] < 0 || result.FinalStacks[2] < 0 {
		t.Fatalf("stacks should be non-negative: %v", result.FinalStacks)
	}
}

// TestPlayHandSixPlayersSmoke 6 人桌 always-call 跑到摊牌不 panic + 守恒。
func TestPlayHandSixPlayersSmoke(t *testing.T) {
	seats := []PlayerSeat{
		{ID: 0, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 1, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 2, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 3, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 4, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
		{ID: 5, Stack: 1000, Player: PlayerFromFunc(alwaysCall())},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	_, result := PlayHand(seats, 0, cfg, makeRng(99), 1)

	// 6 人各 1000,总 6000
	sum := 0
	for _, s := range result.FinalStacks {
		sum += s
	}
	if sum != 6000 {
		t.Fatalf("chips sum = %d, want 6000", sum)
	}
	if len(result.FinalStacks) != 6 {
		t.Fatalf("final stacks len = %d, want 6", len(result.FinalStacks))
	}
}
