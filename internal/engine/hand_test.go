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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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

func TestFirstActor(t *testing.T) {
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
	seats := [2]PlayerSeat{
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
