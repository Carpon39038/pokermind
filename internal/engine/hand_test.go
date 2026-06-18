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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	st, events := setupHand(seats, 0, cfg, makeRng(42), 1)

	if st.stacks[0] != 995 {
		t.Fatalf("SB stack = %d, want 995", st.stacks[0])
	}
	if st.stacks[1] != 990 {
		t.Fatalf("BB stack = %d, want 990", st.stacks[1])
	}
	if st.pot != 15 {
		t.Fatalf("pot = %d, want 15", st.pot)
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 3, Decide: alwaysCall()},
		{ID: 1, Stack: 10, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysFold()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
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
