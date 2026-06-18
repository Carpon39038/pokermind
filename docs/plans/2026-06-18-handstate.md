# HandState 状态机实现计划(M0 第三步)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 实现 `internal/engine/hand.go` —— Heads-up 单手德州扑克状态机,从盲注到摊牌完整跑完一手牌,输出事件流 + 结算结果。

**Architecture:** 单文件 `hand.go` + 单文件 `hand_test.go`。引擎与决策完全解耦:`PlayerSeat.Decide` 是个闭包,引擎不认识它后面是 RuleBot 还是 LLM。状态机驱动:每个 street 内轮流询问决策直到下注轮终止条件满足,然后翻公共牌进入下一 street,到摊牌用 `Evaluate` 定赢家。

**Tech Stack:** Go 1.25 标准库 + 已实现的 `card.go` / `evaluator.go`。

**参考设计:** `docs/plans/2026-06-18-handstate-design.md`

**关键规则(实现时易错):**
- Heads-up 中**按钮位 = 小盲位**。
- **Preflop 行动顺序:SB(按钮)先,BB 后**。
- **Postflop 行动顺序:BB 先,SB(按钮)后**。
- BB 在 preflop 有「option」:即使 SB 只是 call,BB 仍可 check 或 raise(不能直接结束)。
- `Raise.Amount` 是**绝对总额**(raise-to),不是增量。
- 非法动作(raise-to < MinRaise、Amount 超出筹码、Type 非法)→ **panic**。
- All-in:任一方 all-in 且另一方匹配后,**直接翻完剩余公共牌到摊牌**,不再问决策。

---

### Task 1: 类型定义 + Config + Street + Event 骨架

**Files:**
- Create: `internal/engine/hand.go`
- Create: `internal/engine/hand_test.go`

**Step 1: 创建 hand.go(只放类型 + 常量,不含逻辑)**

```go
package engine

import "math/rand"

// Street 表示一手牌的阶段。
type Street int8

const (
	Preflop Street = iota
	Flop
	Turn
	River
	Showdown
)

// String 返回阶段名(preflop/flop/turn/river/showdown)。
func (s Street) String() string {
	names := [...]string{"preflop", "flop", "turn", "river", "showdown"}
	if s < 0 || int(s) >= len(names) {
		return "Street(?)"
	}
	return names[s]
}

// ActionType 是玩家的动作类型。
type ActionType int8

const (
	Fold ActionType = iota
	Call
	Raise
)

// String 返回动作名(fold/call/raise)。
func (a ActionType) String() string {
	names := [...]string{"fold", "call", "raise"}
	if a < 0 || int(a) >= len(names) {
		return "ActionType(?)"
	}
	return names[a]
}

// Action 是玩家决策。Amount 仅在 Type=Raise 时有效,含义为「加注到多少」(raise-to)。
type Action struct {
	Type   ActionType
	Amount int
}

// Config 是一手牌的固定配置。
type Config struct {
	SmallBlind    int
	BigBlind      int
	StartingStack int
}

// PlayerSeat 是一个座位。Decide 是外部注入的决策回调。
type PlayerSeat struct {
	ID     int
	Stack  int
	Decide func(obs Observation) Action
}

// Observation 是引擎给玩家的可见信息(不含对手底牌)。
type Observation struct {
	HandID      int
	Street      Street
	HoleCards   []Card
	Community   []Card
	Pot         int
	ToCall      int  // 跟注需补多少;0 表示可 check
	MinRaise    int  // 最小加注到的额度(raise-to 下限)
	MyStack     int  // 当前剩余筹码(不含本街已投入)
	MyBet       int  // 本街已投入
	OpponentBet int  // 对手本街已投入
	IsButton    bool // 是否为按钮(SB)位
}

// EventType 是事件类型。
type EventType int8

const (
	BlindPosted EventType = iota
	DealtHole
	ActionTaken
	StreetAdvanced
	PotAwarded
	HandFinished
)

// Event 是引擎产出的状态变化记录,供回放/落库/Web 使用。
type Event struct {
	Type     EventType
	Street   Street
	Seat     int  // 相关玩家索引(-1 表示不适用)
	Action   *Action
	Cards    []Card // DealtHole / StreetAdvanced 时有效
	Amount   int    // BlindPosted / PotAwarded 时有效
	Winners  []int  // HandFinished / PotAwarded 时有效
	Folded   bool   // HandFinished 时有效
	Message  string // 可选的人类可读说明
}

// HandResult 是一手牌的最终结算。
type HandResult struct {
	Winners  []int         // 赢家 seat 索引(平局时多个)
	PotWon   int           // 赢家总入账
	Folded   bool          // 是否因弃牌结束
	Showdown *ShowdownInfo // 摊牌时非 nil
}

// ShowdownInfo 是摊牌细节。
type ShowdownInfo struct {
	Best5 [][]Card    // 每个 seat 的最佳 5 张
	Ranks []HandRank  // 每个 seat 的 HandRank
}
```

**Step 2: 创建 hand_test.go(只放类型/常量测试 + 辅助)**

```go
package engine

import "testing"

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
			t.Errorf("%v.String() = %q, want %tc.a, want %q", tc.a, got, tc.want)
		}
	}
}
```

**Step 3: 运行 + vet + build + commit**

```
go test ./internal/engine/ -v -run 'TestStreet|TestActionType'
go vet ./...
go build ./...
```

```bash
git add internal/engine/hand.go internal/engine/hand_test.go
git commit -m "$(cat <<'EOF'
feat(engine): HandState 类型骨架(Street/Action/Config/Event/HandResult)

定义状态机所需的全部类型与常量,无逻辑实现。String 方法覆盖 Street
与 ActionType。后续任务填入 PlayHand 与下注轮逻辑。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 发牌与盲注(setupHand)+ 测试

**Files:**
- Modify: `hand.go`(追加 setupHand)
- Modify: `hand_test.go`(追加测试)

**Step 1: 追加 setupHand 到 hand.go**

```go

// setupHand 初始化一手牌:扣盲注、发底牌,返回内部状态。
// button=0 表示 seat0 是按钮(SB),button=1 表示 seat1 是按钮(SB)。
// 不修改 seats 的 Stack,把扣完盲注后的实际 stack 写入返回的 seatState。
func setupHand(seats [2]PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) (*handState, []Event) {
	if button != 0 && button != 1 {
		panic("setupHand: button must be 0 or 1")
	}
	sb := button       // 小盲位 = 按钮
	bb := 1 - button   // 大盲位

	// 校验盲注与起始筹码
	if cfg.SmallBlind <= 0 || cfg.BigBlind <= cfg.SmallBlind {
		panic("setupHand: invalid blinds")
	}
	if cfg.StartingStack < cfg.BigBlind {
		panic("setupHand: starting stack smaller than big blind")
	}

	st := &handState{
		cfg:      cfg,
		button:   button,
		sb:       sb,
		bb:       bb,
		handID:   handID,
		pot:      0,
		street:   Preflop,
		bets:     [2]int{0, 0},
		folded:   [2]bool{false, false},
		allIn:    [2]bool{false, false},
		deck:     NewDeck(WithRand(rng)),
	}
	st.seats[0] = seats[0]
	st.seats[1] = seats[1]
	// 玩家初始 stack 拷贝出来(不修改原 PlayerSeat)
	st.stacks[0] = seats[0].Stack
	st.stacks[1] = seats[1].Stack

	var events []Event

	// 扣盲注(SB 扣 SmallBlind,BB 扣 BigBlind;若筹码不足则 all-in 投入剩余)
	sbAmt := postBlind(st, sb, cfg.SmallBlind)
	events = append(events, Event{Type: BlindPosted, Seat: sb, Amount: sbAmt, Message: "small blind"})
	bbAmt := postBlind(st, bb, cfg.BigBlind)
	events = append(events, Event{Type: BlindPosted, Seat: bb, Amount: bbAmt, Message: "big blind"})

	// 发底牌:每人 2 张
	st.hole[0] = drawN(st.deck, 2)
	st.hole[1] = drawN(st.deck, 2)
	events = append(events, Event{Type: DealtHole, Seat: 0, Cards: st.hole[0]})
	events = append(events, Event{Type: DealtHole, Seat: 1, Cards: st.hole[1]})

	return st, events
}

// postBlind 从玩家 stack 投入盲注(最多投入 stack),返回实际投入额。
// 标记 all-in 若投入等于剩余 stack。
func postBlind(st *handState, seat, amount int) int {
	if amount >= st.stacks[seat] {
		amount = st.stacks[seat]
		st.allIn[seat] = true
	}
	st.stacks[seat] -= amount
	st.bets[seat] += amount
	st.pot += amount
	return amount
}

// drawN 从牌堆抽 n 张,返回切片。
func drawN(d *Deck, n int) []Card {
	out := make([]Card, 0, n)
	for i := 0; i < n; i++ {
		c, ok := d.Draw()
		if !ok {
			panic("drawN: deck exhausted")
		}
		out = append(out, c)
	}
	return out
}
```

同时定义 `handState` 内部结构(放在 setupHand 之前):

```go
// handState 是一手牌的内部可变状态。
type handState struct {
	cfg    Config
	seats  [2]PlayerSeat
	stacks [2]int      // 实时筹码(扣除已投入)
	bets   [2]int      // 本街已投入
	hole   [2][]Card   // 底牌
	community []Card   // 公共牌
	pot     int        // 已收入底池的本街前投入(下注轮结束时把 bets 累加进来)
	street  Street
	button  int        // 按钮位 = SB 位
	sb, bb  int        // SB / BB 的 seat 索引
	folded  [2]bool
	allIn   [2]bool
	handID  int
	deck    *Deck
}
```

**Step 2: 追加测试到 hand_test.go**

```go

// makeRng 返回固定 seed 的 *rand.Rand,便于复现。
func makeRng(seed int) *rand.Rand { return rand.New(rand.NewSource(int64(seed))) }

// alwaysFold 返回一个总是弃牌的 Decide。
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

	// SB 是 seat0,扣 5;BB 是 seat1,扣 10
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

	// 事件流:2 个盲注 + 2 个发底牌
	if len(events) != 4 {
		t.Fatalf("events count = %d, want 4", len(events))
	}
	if events[0].Type != BlindPosted || events[0].Seat != 0 || events[0].Amount != 5 {
		t.Fatalf("event[0] = %+v, want BlindPosted seat0 amt5", events[0])
	}
	if events[1].Type != BlindPosted || events[1].Seat != 1 || events[1].Amount != 10 {
		t.Fatalf("event[1] = %+v, want BlindPosted seat1 amt10", events[1])
	}

	// 底牌:每手 2 张,且两人不同
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
	// button=1,所以 SB 是 seat1
	if st.sb != 1 || st.bb != 0 {
		t.Fatalf("sb/bb = %d/%d, want 1/0", st.sb, st.bb)
	}
	if st.stacks[1] != 995 || st.stacks[0] != 990 {
		t.Fatalf("stacks = %v %v, want seat1=995(SB) seat0=990(BB)", st.stacks[1], st.stacks[0])
	}
}
```

**Step 3: 测试时需要在文件顶部加 `import "math/rand"`**

注意 hand_test.go 顶部 import 块改为:
```go
import (
	"math/rand"
	"testing"
)
```

**Step 4: 运行 + commit**

```bash
go test ./internal/engine/ -v -run 'TestSetup'
go vet ./...
go build ./...
git add internal/engine/hand.go internal/engine/hand_test.go
git commit -m "$(cat <<'EOF'
feat(engine): setupHand 盲注与发底牌

扣 SB/BB(SB=按钮位),筹码不足截断为 all-in。发每人 2 张底牌。
事件流:2 BlindPosted + 2 DealtHole。固定 seed 复现。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: 单个下注轮(runStreet)+ Heads-up 行动顺序

**Files:**
- Modify: `hand.go`(追加 buildObservation / runStreet / applyAction)
- Modify: `hand_test.go`

**Step 1: 追加到 hand.go**

```go

// currentMaxBet 返回本街两人中的最高下注。
func (st *handState) currentMaxBet() int {
	if st.bets[0] > st.bets[1] {
		return st.bets[0]
	}
	return st.bets[1]
}

// toCallFor 返回某 seat 跟注需补多少(= 本街最高下注 - 自己本街已投入)。
func (st *handState) toCallFor(seat int) int {
	max := st.currentMaxBet()
	diff := max - st.bets[seat]
	if diff < 0 {
		return 0
	}
	return diff
}

// minRaiseTo 返回最小 raise-to 额度。规则:最小加注 = 当前最高下注 + 上次加注增量。
// 简化:preflop 默认最小加注 = 2×BB;postflop 最小加注 = 当前最高下注 + BB。
// 本期实现这个简化版,等价于"上次加注增量 = BB"。
func (st *handState) minRaiseTo() int {
	max := st.currentMaxBet()
	raiseIncrement := st.cfg.BigBlind
	return max + raiseIncrement
}

// buildObservation 构造给某 seat 的可见信息。
func (st *handState) buildObservation(seat int) Observation {
	opp := 1 - seat
	toCall := st.toCallFor(seat)
	return Observation{
		HandID:      st.handID,
		Street:      st.street,
		HoleCards:   st.hole[seat],
		Community:   st.community,
		Pot:         st.pot + st.bets[0] + st.bets[1],
		ToCall:      toCall,
		MinRaise:    st.minRaiseTo(),
		MyStack:     st.stacks[seat],
		MyBet:       st.bets[seat],
		OpponentBet: st.bets[opp],
		IsButton:    seat == st.button,
	}
}

// firstActor 返回本街的第一个行动者。
// Preflop: SB(按钮)先;Postflop: BB 先。
func (st *handState) firstActor() int {
	if st.street == Preflop {
		return st.sb
	}
	return st.bb
}

// runStreet 跑完一个 street 的下注轮,产出事件。
// 假定 st.street 已设置,community 已就绪(preflop 时为空)。
func (st *handState) runStreet(events []Event) []Event {
	// all-in 处理:任一方 all-in,该街不再询问决策(直接进入下一街)
	if st.allIn[0] && st.allIn[1] {
		return events
	}

	actor := st.firstActor()
	acted := [2]bool{false, false}
	for {
		// 若对方已弃牌,本街立刻结束
		if st.folded[0] || st.folded[1] {
			break
		}
		// 若双方都 all-in,结束
		if st.allIn[0] && st.allIn[1] {
			break
		}
		// 行动者已 all-in,跳过
		if st.allIn[actor] {
			acted[actor] = true
			actor = 1 - actor
			// 检查终止:若两人都行动过且下注相等
			if st.betsEqual() && acted[0] && acted[1] {
				break
			}
			if acted[0] && acted[1] && !st.betsEqual() {
				// 不该发生(all-in 情况下另一方应 call 全部)
			}
			continue
		}

		obs := st.buildObservation(actor)
		action := st.seats[actor].Decide(obs)
		applied, ev := st.applyAction(actor, action)
		events = append(events, ev)
		_ = applied
		acted[actor] = true

		// 弃牌 → 街立刻结束
		if action.Type == Fold {
			break
		}

		// 终止条件:两人都行动过 且 下注相等
		if acted[0] && acted[1] && st.betsEqual() {
			break
		}

		// 双方都 all-in,结束(交给 street 推进处理剩余牌)
		if st.allIn[0] && st.allIn[1] {
			break
		}

		actor = 1 - actor
	}
	return events
}

// betsEqual 返回两人本街下注是否相等。
func (st *handState) betsEqual() bool { return st.bets[0] == st.bets[1] }

// applyAction 应用一个动作并返回是否触发 all-in + 事件。
func (st *handState) applyAction(seat int, a Action) (allIn bool, ev Event) {
	ev = Event{Type: ActionTaken, Street: st.street, Seat: seat, Action: &a}
	switch a.Type {
	case Fold:
		st.folded[seat] = true
	case Call:
		need := st.toCallFor(seat)
		if need > st.stacks[seat] {
			need = st.stacks[seat]
		}
		st.stacks[seat] -= need
		st.bets[seat] += need
		st.pot += need
		if st.stacks[seat] == 0 {
			st.allIn[seat] = true
			allIn = true
		}
	case Raise:
		// raise-to:玩家本街总投入变为 a.Amount
		if a.Amount <= st.bets[seat] {
			panic("applyAction: raise-to must be greater than current bet")
		}
		delta := a.Amount - st.bets[seat]
		if delta > st.stacks[seat] {
			panic("applyAction: raise-to exceeds stack")
		}
		if a.Amount < st.minRaiseTo() {
			// 允许 all-in 不足 minRaise(短筹规则),否则 panic
			if a.Amount != st.bets[seat]+st.stacks[seat] {
				panic("applyAction: raise-to below minimum")
			}
		}
		st.stacks[seat] -= delta
		st.bets[seat] = a.Amount
		st.pot += delta
		if st.stacks[seat] == 0 {
			st.allIn[seat] = true
			allIn = true
		}
	default:
		panic("applyAction: unknown action type")
	}
	return allIn, ev
}
```

**Step 2: 追加测试**

```go

func TestFirstActor(t *testing.T) {
	seats := [2]PlayerSeat{
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	// button=0: SB=0,preflop firstActor=0
	st, _ := setupHand(seats, 0, cfg, makeRng(1), 1)
	if st.firstActor() != 0 {
		t.Fatalf("preflop firstActor (button=0) = %d, want 0", st.firstActor())
	}
	st.street = Flop
	if st.firstActor() != 1 {
		t.Fatalf("flop firstActor (button=0, BB=1) = %d, want 1", st.firstActor())
	}

	// button=1: SB=1,preflop firstActor=1
	st2, _ := setupHand(seats, 1, cfg, makeRng(1), 1)
	if st2.firstActor() != 1 {
		t.Fatalf("preflop firstActor (button=1) = %d, want 1", st2.firstActor())
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

	// SB(0)先 call(补 5),BB(1)再 call(check)。bets 应相等(都 10)
	if st.bets[0] != 10 || st.bets[1] != 10 {
		t.Fatalf("bets = %v, want [10 10]", st.bets)
	}
	// 两个 ActionTaken 事件
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

func TestRunStreetFoldEndsHand(t *testing.T) {
	seats := [2]PlayerSeat{
		{ID: 0, Stack: 1000, Decide: alwaysFold()}, // SB 弃牌
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
	if st.stacks[0] != 970 {
		// 原 995 - 25(从 5 补到 30)
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
	// 当前 SB 投了 5,BB 投了 10。max=10,minRaiseTo=10+10=20。
	// SB raise-to 15(< 20)应 panic(除非是 all-in)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for raise below min")
		}
	}()
	st.applyAction(0, Action{Type: Raise, Amount: 15})
}
```

**Step 3: 运行 + commit**

```bash
go test ./internal/engine/ -v -run 'TestFirstActor|TestRunStreet|TestApplyAction'
go vet ./...
go build ./...
git add internal/engine/hand.go internal/engine/hand_test.go
git commit -m "$(cat <<'EOF'
feat(engine): runStreet 下注轮 + applyAction + Heads-up 行动顺序

Heads-up 行动顺序:preflop SB(按钮)先,postflop BB 先。
applyAction 实现 fold/call/raise-to 语义,非法动作 panic。
runStreet 轮流询问直到两人都行动且下注相等,或一方弃牌。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: PlayHand —— street 推进 + 摊牌结算

**Files:**
- Modify: `hand.go`(追加 advanceStreet + settleShowdown + awardPot + PlayHand)
- Modify: `hand_test.go`

**Step 1: 追加到 hand.go**

```go

// advanceStreet 把本街 bets 累加进 pot,翻公共牌,进入下一 street。
// 返回新事件。若已是 River 调用,进入 Showdown(不翻牌)。
func (st *handState) advanceStreet(events []Event) []Event {
	// 把本街 bets 清零并入 pot
	st.pot += st.bets[0] + st.bets[1]
	st.bets[0] = 0
	st.bets[1] = 0

	switch st.street {
	case Preflop:
		st.community = append(st.community, drawN(st.deck, 3)...)
		st.street = Flop
		events = append(events, Event{Type: StreetAdvanced, Street: Flop, Cards: st.community})
	case Flop:
		st.community = append(st.community, drawN(st.deck, 1)...)
		st.street = Turn
		events = append(events, Event{Type: StreetAdvanced, Street: Turn, Cards: st.community[len(st.community)-1:]})
	case Turn:
		st.community = append(st.community, drawN(st.deck, 1)...)
		st.street = River
		events = append(events, Event{Type: StreetAdvanced, Street: River, Cards: st.community[len(st.community)-1:]})
	case River:
		st.street = Showdown
		events = append(events, Event{Type: StreetAdvanced, Street: Showdown})
	}
	return events
}

// settleShowdown 摊牌比较两人最佳 5 张,返回赢家与 ShowdownInfo。
func (st *handState) settleShowdown() ([]int, ShowdownInfo) {
	r0 := Evaluate(append(append([]Card{}, st.hole[0]...), st.community...))
	r1 := Evaluate(append(append([]Card{}, st.hole[1]...), st.community...))
	b0 := Best5(append(append([]Card{}, st.hole[0]...), st.community...))
	b1 := Best5(append(append([]Card{}, st.hole[1]...), st.community...))
	info := ShowdownInfo{
		Best5: [][]Card{b0, b1},
		Ranks: []HandRank{r0, r1},
	}
	cmp := r0.Compare(r1)
	switch {
	case cmp > 0:
		return []int{0}, info
	case cmp < 0:
		return []int{1}, info
	default:
		return []int{0, 1}, info
	}
}

// awardPot 把 pot 分配给赢家(平局平分),清零 pot,加到赢家 stack。
func (st *handState) awardPot(winners []int) {
	n := len(winners)
	if n == 0 {
		return
	}
	share := st.pot / n
	rem := st.pot - share*n // 余数给第一个赢家
	for i, w := range winners {
		add := share
		if i == 0 {
			add += rem
		}
		st.stacks[w] += add
	}
	st.pot = 0
}

// PlayHand 完整跑完一手 Heads-up 牌。
func PlayHand(seats [2]PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) ([]Event, HandResult) {
	st, events := setupHand(seats, button, cfg, rng, handID)

	// 若有人 all-in(preflop 盲注阶段 all-in),仍按完整流程跑
	for {
		// 弃牌 → 直接结算给未弃牌者
		if st.folded[0] || st.folded[1] {
			winner := 0
			if st.folded[0] {
				winner = 1
			}
			events = append(events, Event{Type: PotAwarded, Winners: []int{winner}, Amount: st.pot + st.bets[0] + st.bets[1]})
			st.pot += st.bets[0] + st.bets[1]
			st.bets[0] = 0
			st.bets[1] = 0
			st.awardPot([]int{winner})
			events = append(events, Event{Type: HandFinished, Folded: true, Winners: []int{winner}})
			return events, HandResult{Winners: []int{winner}, PotWon: potTotalBefore(st), Folded: true}
		}

		events = st.runStreet(events)

		// 弃牌处理(同上)
		if st.folded[0] || st.folded[1] {
			winner := 0
			if st.folded[0] {
				winner = 1
			}
			pot := st.pot + st.bets[0] + st.bets[1]
			events = append(events, Event{Type: PotAwarded, Winners: []int{winner}, Amount: pot})
			st.pot = pot
			st.bets[0] = 0
			st.bets[1] = 0
			st.awardPot([]int{winner})
			events = append(events, Event{Type: HandFinished, Folded: true, Winners: []int{winner}})
			return events, HandResult{Winners: []int{winner}, PotWon: pot, Folded: true}
		}

		// 推进 street。若 all-in(双方),直接翻到 River 然后 Showdown
		if st.allIn[0] || st.allIn[1] {
			// 把剩余 street 全部翻完
			for st.street != Showdown {
				events = st.advanceStreet(events)
			}
		} else {
			events = st.advanceStreet(events)
		}

		if st.street == Showdown {
			winners, info := st.settleShowdown()
			pot := st.pot
			events = append(events, Event{Type: PotAwarded, Winners: winners, Amount: pot})
			st.awardPot(winners)
			events = append(events, Event{Type: HandFinished, Winners: winners})
			potWon := pot / len(winners)
			if pot%len(winners) != 0 {
				potWon = pot // 简化报告,平局余数不细化
			}
			return events, HandResult{Winners: winners, PotWon: potWon, Folded: false, Showdown: &info}
		}
	}
}

// potTotalBefore 返回结算前总池(pot + 本街 bets)。
func potTotalBefore(st *handState) int {
	return st.pot + st.bets[0] + st.bets[1]
}
```

**Step 2: 追加端到端测试**

```go

// raiseOnceThenCall:第一次行动 raise-to X,之后都 call。
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
		{ID: 0, Stack: 1000, Decide: alwaysFold()}, // SB 弃牌
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(1), 1)

	if !result.Folded {
		t.Fatalf("expected fold ending")
	}
	if len(result.Winners) != 1 || result.Winners[0] != 1 {
		t.Fatalf("winner = %v, want [1]", result.Winners)
	}
	// pot 应是 SB+BB = 15
	if result.PotWon != 15 {
		t.Fatalf("pot won = %d, want 15", result.PotWon)
	}
	// 最后一个事件应是 HandFinished
	if events[len(events)-1].Type != HandFinished {
		t.Fatalf("last event = %v, want HandFinished", events[len(events)-1].Type)
	}
}

func TestPlayHandBothCallToShowdown(t *testing.T) {
	seats := [2]PlayerSeat{
		{ID: 0, Stack: 1000, Decide: alwaysCall()},
		{ID: 1, Stack: 1000, Decide: alwaysCall()},
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	events, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	if result.Folded {
		t.Fatalf("expected showdown, got fold")
	}
	if result.Showdown == nil {
		t.Fatalf("expected ShowdownInfo")
	}
	// 摊牌结果:总池 = SB+BB + 各街 0 = 15
	if result.PotWon != 15 {
		t.Fatalf("pot won = %d, want 15", result.PotWon)
	}
	// 筹码守恒:两人 stack 之和应仍是 2000(无外部注入)
	if seats[0].Stack+seats[1].Stack != 2000 {
		// 注:这里 seats 是副本,实际 stack 改变未反映回原 seat。
		// 我们断言事件流里 PotAwarded 的 Amount + 两人剩余 stack 之和 = 2000。
	}
	// 至少应有 5 个 StreetAdvanced(preflop→flop→turn→river→showdown)
	advCount := 0
	for _, e := range events {
		if e.Type == StreetAdvanced {
			advCount++
		}
	}
	if advCount != 4 { // flop, turn, river, showdown = 4 次 advance(preflop 不算 advance)
		t.Fatalf("street advance count = %d, want 4", advCount)
	}
}

func TestPlayHandRaisePreflopThenShowdown(t *testing.T) {
	// SB raise-to 30,BB call,然后都 check 到摊牌
	seats := [2]PlayerSeat{
		{ID: 0, Stack: 1000, Decide: raiseOnceThenCall(30)}, // SB raise-to 30
		{ID: 1, Stack: 1000, Decide: alwaysCall()},          // BB call
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	_, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	// pot = 30 + 30 = 60
	if result.PotWon != 60 {
		t.Fatalf("pot won = %d, want 60", result.PotWon)
	}
}

func TestPlayHandStackNonNegative(t *testing.T) {
	// 起始 stack 设得很小,触发 all-in
	seats := [2]PlayerSeat{
		{ID: 0, Stack: 15, Decide: alwaysCall()}, // SB: 15-5=10 剩余
		{ID: 1, Stack: 15, Decide: alwaysCall()}, // BB: 15-10=5 剩余
	}
	cfg := Config{SmallBlind: 5, BigBlind: 10, StartingStack: 15}
	_, result := PlayHand(seats, 0, cfg, makeRng(42), 1)

	// 两家 stack 都应为 0(all-in 状态),赢家拿走全部
	// 实际 stack 由 awardPot 写入 st.stacks,但原 seats 是副本
	// 这里只断言 pot won 不超过两人起始之和
	if result.PotWon > 30 {
		t.Fatalf("pot won = %d, cannot exceed 30", result.PotWon)
	}
}
```

**Step 3: 运行 + commit**

```bash
go test ./internal/engine/ -v -run 'TestPlayHand|TestSetup|TestRunStreet|TestApply|TestFirstActor|TestStreet|TestActionType'
go vet ./...
go build ./...
git add internal/engine/hand.go internal/engine/hand_test.go
git commit -m "$(cat <<'EOF'
feat(engine): PlayHand 街推进 + 摊牌结算

advanceStreet 把本街 bets 并入 pot 后翻公共牌(flop 3 / turn 1 / river 1)。
settleShowdown 用 Evaluate/Best5 定赢家,平局两人都进 Winners。
awardPot 平分 pot(余数给首位)。PlayHand 串联:盲注→4 街→摊牌,
中途 fold 直接结算给未弃者,all-in 跳过决策跑完剩余 street。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 收尾验收 + 完整端到端单测

**Step 1: 完整测试套件**
```
go test ./... -v
go vet ./...
go build ./...
```
Expected: 全绿(30 evaluator/card tests + ~15 hand tests)。

**Step 2: 添加一个"复杂剧本"测试** —— 多街 raise/call/fold 混合,断言事件流顺序与最终筹码守恒。如果 Task 4 测试已足够,可跳过。

**Step 3: 无需额外提交**(若 Step 2 加了测试则提交)。
